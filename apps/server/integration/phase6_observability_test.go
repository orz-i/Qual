//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPhase6OperationalReadinessAndPrivateMetrics(t *testing.T) {
	f := setup(t)
	w := create(t, f, f.admin, "phase6 observability probe")
	url := f.url(f.project) + "/" + w["id"].(string)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.viewer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("business read %d", resp.StatusCode)
	}
	for _, name := range []string{"X-Frame-Options", "Referrer-Policy", "X-Content-Type-Options", "Cache-Control"} {
		if resp.Header.Get(name) == "" {
			t.Errorf("missing security header %s", name)
		}
	}
	ready, err := http.Get(f.server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = ready.Body.Close()
	if ready.StatusCode != 200 {
		t.Fatalf("readyz status %d", ready.StatusCode)
	}
	for _, token := range []string{"", f.agent, f.viewer} {
		req, err := http.NewRequest("GET", f.server.URL+"/internal/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		result, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = result.Body.Close()
		if result.StatusCode != 401 {
			t.Fatalf("project credential read metrics %d", result.StatusCode)
		}
	}
	req, err = http.NewRequest("GET", f.server.URL+"/internal/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("m", 40))
	result, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(result.Body)
	_ = result.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 200 {
		t.Fatalf("operator metrics response %d", result.StatusCode)
	}
	data := string(raw)
	if !strings.Contains(data, `route="GET /api/v1/projects/{projectID}/work-items/{itemID}"`) {
		t.Fatalf("route template not recorded: %s", data)
	}
	if strings.Contains(data, w["id"].(string)) || strings.Contains(data, f.project.String()) ||
		strings.Contains(data, f.viewer) {
		t.Fatal("high cardinality ID or token leaked to metrics")
	}
}

func TestPhase6LocalReadLatencyBaseline(t *testing.T) {
	f := setup(t)
	w := create(t, f, f.admin, "phase6 load fixture")
	target := f.url(f.project) + "/" + w["id"].(string)
	const concurrent = 16
	const requests = 400
	type sample struct {
		duration time.Duration
		status   int
		err      error
	}
	out := make(chan sample, requests)
	jobs := make(chan int, requests)
	start := make(chan struct{})
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{MaxIdleConns: concurrent, MaxIdleConnsPerHost: concurrent},
	}
	defer client.CloseIdleConnections()
	var wg sync.WaitGroup
	for k := 0; k < concurrent; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
				if err != nil {
					cancel()
					out <- sample{err: err}
					continue
				}
				req.Header.Set("Authorization", "Bearer "+f.viewer)
				began := time.Now()
				resp, err := client.Do(req)
				duration := time.Since(began)
				if err != nil {
					cancel()
					out <- sample{duration: duration, err: err}
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				out <- sample{duration: duration, status: resp.StatusCode}
				cancel()
			}
		}()
	}
	for i := 0; i < requests; i++ {
		jobs <- i
	}
	close(jobs)
	began := time.Now()
	close(start)
	wg.Wait()
	close(out)
	elapsed := time.Since(began)
	durations := make([]time.Duration, 0, requests)
	for s := range out {
		if s.err != nil || s.status != 200 {
			t.Fatalf("load request failed status=%d err=%v", s.status, s.err)
		}
		durations = append(durations, s.duration)
	}
	if len(durations) != requests {
		t.Fatalf("requests=%d want %d", len(durations), requests)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p := func(f float64) time.Duration {
		index := int(float64(len(durations)) * f)
		if index >= len(durations) {
			index = len(durations) - 1
		}
		return durations[index]
	}
	t.Logf("Phase6 local loopback read baseline: requests=%d concurrency=%d elapsed=%s throughput=%.1f req/s p50=%s p95=%s p99=%s",
		requests, concurrent, elapsed, float64(requests)/elapsed.Seconds(), p(.5), p(.95), p(.99))
	// Baseline, not a production SLA. Fail only when requests time out or return errors.
}
