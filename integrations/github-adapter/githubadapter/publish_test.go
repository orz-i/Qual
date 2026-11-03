package githubadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

var testProject = "33333333-3333-4333-8333-333333333333"
var testWork = "44444444-4444-4444-8444-444444444444"
var testThread = "55555555-5555-4555-8555-555555555555"
var testEventID = "66666666-6666-4666-8666-666666666666"

func mappingFixture(t *testing.T, root string, link bool) string {
	t.Helper()
	targets := map[string]Target{}
	if link {
		targets["orz-i/Qual/pull/1"] = Target{
			ProjectID: testProject, WorkItemID: testWork, ThreadID: testThread,
		}
	}
	m := Mapping{RepositoryID: fixtureRepo.ID, RepositoryName: fixtureRepo.FullName, Links: targets}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "approved-links.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishMappingAuthThenExactIdempotentDelivery(t *testing.T) {
	directory := t.TempDir()
	spool, err := OpenSpool(filepath.Join(directory, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	created, err := spool.Enqueue(d)
	if err != nil || !created {
		t.Fatal(err)
	}
	mapping := mappingFixture(t, directory, false)
	var code atomic.Int32
	code.Store(403)
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.URL.Path != "/api/v1/projects/"+testProject+"/external-events" || r.Method != "POST" {
			t.Errorf("adapter called unexpected Qual path/method")
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-integration-only" ||
			r.Header.Get("Idempotency-Key") != "github-"+fixtureDeliveryID {
			t.Errorf("adapter did not respect its own scoped credential or idempotency key")
			w.WriteHeader(401)
			return
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["source"] != "github" || input["external_event_id"] != fixtureDeliveryID ||
			input["thread_id"] != testThread || input["work_item_id"] != testWork ||
			input["summary"] != "GitHub PR #1 opened" {
			t.Errorf("outgoing Event has unexpected or unsafe source fields")
		}
		if len(input) != 8 {
			t.Errorf("sent raw body/extra fields to Qual: %d", len(input))
		}
		if code.Load() != 201 {
			w.WriteHeader(int(code.Load()))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": testEventID, "project_id": testProject, "work_item_id": testWork,
			"thread_id": testThread, "source": "github", "external_event_id": fixtureDeliveryID,
		})
	}))
	defer server.Close()
	publisher := Publisher{
		Spool: spool, Repository: fixtureRepo, MapPath: mapping,
		QualURL: server.URL, Token: "synthetic-integration-only",
		Now: func() time.Time { return fixtureNow },
	}
	first, err := publisher.PublishOnce(context.Background())
	if err != nil || first.BlockedMapping != 1 || first.Attempted != 0 {
		t.Fatalf("unmapped source used Qual credential: %+v %v", first, err)
	}
	if received.Load() != 0 {
		t.Fatal("unmapped source unexpectedly posted to Qual")
	}
	mappingFixture(t, directory, true)
	denied, err := publisher.PublishOnce(context.Background())
	if err != nil || denied.BlockedAuth != 1 || denied.Attempted != 1 {
		t.Fatalf("403 should remain blocked: %+v %v", denied, err)
	}
	stored, err := spool.Pending()
	if err != nil || len(stored) != 1 || stored[0].State != "blocked_auth" || stored[0].Attempts != 1 {
		t.Fatal("denied Event was discarded or overwritten")
	}
	code.Store(201)
	if blocked, err := publisher.PublishOnce(context.Background()); err != nil || blocked.Attempted != 0 || blocked.Skipped != 1 {
		t.Fatalf("revoked/denied credential automatically retried after 403: %+v %v", blocked, err)
	}
	if err := spool.RequeueAfterReview(fixtureDeliveryID); err != nil {
		t.Fatal(err)
	}
	passed, err := publisher.PublishOnce(context.Background())
	if err != nil || passed.Delivered != 1 || passed.Attempted != 1 {
		t.Fatalf("authorized event not delivered: %+v %v", passed, err)
	}
	done, ok, err := spool.read("delivered", fixtureDeliveryID)
	if err != nil || !ok || done.DeliveredEventID != testEventID || done.Attempts != 2 {
		t.Fatal("successful delivery missing from durable audit receipts")
	}
	if jobs, err := spool.Pending(); err != nil || len(jobs) != 0 {
		t.Fatal("delivered event remains pending")
	}
	created, err = spool.Enqueue(d)
	if err != nil || created {
		t.Fatalf("same GitHub delivery replay inserted again: %t %v", created, err)
	}
	again, err := publisher.PublishOnce(context.Background())
	if err != nil || again.Attempted != 0 || received.Load() != 2 {
		t.Fatal("duplicate replay posted twice")
	}
}

func TestPublisherHTTP409IsQuarantined(t *testing.T) {
	root := t.TempDir()
	spool, err := OpenSpool(filepath.Join(root, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = spool.Enqueue(d)
	if err != nil {
		t.Fatal(err)
	}
	mapping := mappingFixture(t, root, true)
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		w.WriteHeader(409)
	}))
	defer server.Close()
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, Token: "synthetic",
		QualURL: server.URL, Now: func() time.Time { return fixtureNow }}
	a, err := p.PublishOnce(context.Background())
	if err != nil || a.Conflict != 1 || a.Attempted != 1 {
		t.Fatal("409 not quarantined", a, err)
	}
	b, err := p.PublishOnce(context.Background())
	if err != nil || b.Skipped != 1 || sent.Load() != 1 {
		t.Fatal("conflicting immutable Event retried", b, err)
	}
	done, err := spool.Pending()
	if err != nil || len(done) != 1 || done[0].State != "conflict" {
		t.Fatal("409 not preserved")
	}
}

func TestPublisherRetryableErrorBackoffAndRecovery(t *testing.T) {
	root := t.TempDir()
	spool, err := OpenSpool(filepath.Join(root, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spool.Enqueue(d); err != nil {
		t.Fatal(err)
	}
	mapping := mappingFixture(t, root, true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := calls.Add(1)
		if count == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": testEventID, "project_id": testProject, "work_item_id": testWork,
			"thread_id": testThread, "source": "github", "external_event_id": fixtureDeliveryID,
		})
	}))
	defer server.Close()
	clock := fixtureNow
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, Token: "synthetic",
		QualURL: server.URL, Now: func() time.Time { return clock }}
	a, err := p.PublishOnce(context.Background())
	if err != nil || a.Retryable != 1 {
		t.Fatal("transient server error lost", a, err)
	}
	b, err := p.PublishOnce(context.Background())
	if err != nil || b.Skipped != 1 || calls.Load() != 1 {
		t.Fatal("backoff ignored", b, err)
	}
	clock = clock.Add(3 * time.Second)
	c, err := p.PublishOnce(context.Background())
	if err != nil || c.Delivered != 1 || calls.Load() != 2 {
		t.Fatal("resumed retry did not deliver", c, err)
	}
}

func TestQualEndpointAndMappingSecurity(t *testing.T) {
	for _, url := range []string{
		"http://evil.example:18080", "https://127.0.0.1:18080",
		"http://user:secret@127.0.0.1:18080", "http://127.0.0.1:18080/unsafe",
		"http://127.0.0.1:9", "http://[::1]:18080",
	} {
		if _, err := QualEndpoint(url); !errors.Is(err, ErrInvalid) {
			t.Errorf("unsafe adapter destination accepted (%s): %v", url, err)
		}
	}
	if _, err := QualEndpoint("http://127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := mappingFixture(t, root, true)
	if _, err := ReadMapping(path, fixtureRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMapping(path, fixtureRepo); !errors.Is(err, ErrSpool) {
		t.Fatalf("world-readable explicit Project mapping accepted: %v", err)
	}
}
