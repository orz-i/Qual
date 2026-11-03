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

func onePending(t *testing.T) (*Spool, Delivery, string) {
	t.Helper()
	folder := t.TempDir()
	spool, err := OpenSpool(filepath.Join(folder, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := spool.Enqueue(job); err != nil || !fresh {
		t.Fatal(err)
	}
	return spool, job, mappingFixture(t, folder, true)
}

func TestSpoolDurableAfterFreshProcessAndDelivery(t *testing.T) {
	spool, job, _ := onePending(t)
	// A new process forgets all in-memory state. Disk is authoritative.
	reopened, err := OpenSpool(spool.Root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := reopened.Enqueue(job)
	if err != nil || created {
		t.Fatalf("reopened queue redelivery duplicated: %v %v", created, err)
	}
	previous, err := reopened.Pending()
	if err != nil || len(previous) != 1 || previous[0].DeliveryID != job.DeliveryID {
		t.Fatal("pending event disappeared across restart")
	}
	job.Attempts = 1
	job.DeliveredEventID = testEventID
	if err := reopened.Update(job, true); err != nil {
		t.Fatal(err)
	}
	after, err := OpenSpool(spool.Root)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := after.Counts()
	if err != nil || counts["delivered"] != 1 {
		t.Fatalf("delivered receipts lost: %v %v", counts, err)
	}
	replay, err := after.Enqueue(previous[0])
	if err != nil || replay {
		t.Fatalf("persisted delivered event replay not idempotent: %t %v", replay, err)
	}
}

func TestAuth401AndMapping404RemainDurable(t *testing.T) {
	cases := []struct {
		status   int
		expected string
	}{
		{401, "blocked_auth"},
		{404, "blocked_mapping"},
	}
	for _, tc := range cases {
		t.Run(tc.expected, func(t *testing.T) {
			spool, job, mapping := onePending(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, QualURL: server.URL,
				Token: "test-only-integration-credential", Now: func() time.Time { return fixtureNow }}
			a, err := p.PublishOnce(context.Background())
			if err != nil || a.Attempted != 1 {
				t.Fatalf("first denial: %v %v", a, err)
			}
			fresh, err := OpenSpool(spool.Root)
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := fresh.Pending()
			if err != nil || len(jobs) != 1 || jobs[0].State != tc.expected ||
				jobs[0].DeliveryID != job.DeliveryID || jobs[0].LastHTTPStatus != tc.status {
				t.Fatalf("negative result lost across restart: %v %v", jobs, err)
			}
			if next, err := p.PublishOnce(context.Background()); err != nil || next.Attempted != 1 {
				t.Fatalf("human-remediable denial was silently dropped: %v %v", next, err)
			}
			if requests.Load() != 2 {
				t.Fatal("unexpected post count")
			}
		})
	}
}

func TestTransientSixthAttemptGoesToManualReviewWithoutDataLoss(t *testing.T) {
	spool, job, mapping := onePending(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	now := fixtureNow
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, QualURL: server.URL,
		Token: "synthetic-credential-only", Now: func() time.Time { return now }}
	for i := 0; i < 6; i++ {
		res, err := p.PublishOnce(context.Background())
		if err != nil || res.Attempted != 1 {
			t.Fatalf("transient attempt %d: %v %v", i, res, err)
		}
		now = now.Add(3 * time.Minute)
	}
	reopened, err := OpenSpool(spool.Root)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := reopened.Pending()
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 6 ||
		jobs[0].State != "manual_review" || jobs[0].DeliveryID != job.DeliveryID {
		t.Fatalf("six failed events disappeared: %v %v", jobs, err)
	}
	next, err := p.PublishOnce(context.Background())
	if err != nil || next.Attempted != 0 || next.Skipped != 1 {
		t.Fatal("manual_review was retried without review")
	}
	if err := reopened.RequeueAfterReview("not-a-delivery-uuid"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid manual requeue ID accepted: %v", err)
	}
	if err := reopened.RequeueAfterReview(job.DeliveryID); err != nil {
		t.Fatal(err)
	}
	manual, err := reopened.Pending()
	if err != nil || len(manual) != 1 || manual[0].State != "pending" || manual[0].Attempts != 6 ||
		manual[0].LastErrorCode != "MANUAL_RETRY_APPROVED" {
		t.Fatal("explicit requeue destroyed attempts or failed to resume")
	}
	if err := reopened.RequeueAfterReview(job.DeliveryID); !errors.Is(err, ErrConflict) {
		t.Fatalf("unreviewed double requeue accepted: %v", err)
	}
}

func TestPublisherRefusesUnexpected201Body(t *testing.T) {
	spool, _, mapping := onePending(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id": testEventID, "project_id": testProject, "work_item_id": testWork,
			"thread_id": testThread, "source": "github",
			"external_event_id": "00000000-0000-4000-8000-000000000001",
		})
	}))
	defer server.Close()
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, QualURL: server.URL,
		Token: "synthetic", Now: func() time.Time { return fixtureNow }}
	stats, err := p.PublishOnce(context.Background())
	if err != nil || stats.Delivered != 0 || stats.Retryable != 1 {
		t.Fatalf("fake 201 accepted: %v %v", stats, err)
	}
	jobs, err := spool.Pending()
	if err != nil || len(jobs) != 1 || jobs[0].State != "retry" {
		t.Fatal("bad 201 erased event")
	}
}

func TestPublisherNeverFollowsTokenRedirect(t *testing.T) {
	spool, _, mapping := onePending(t)
	var hits atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", second.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, QualURL: first.URL,
		Token: "synthetic-integration-token", Now: func() time.Time { return fixtureNow }}
	stats, err := p.PublishOnce(context.Background())
	if err != nil || stats.Delivered != 0 || stats.Retryable != 1 || hits.Load() != 0 {
		t.Fatalf("outbound auth token followed redirect: %v %v hits=%d", stats, err, hits.Load())
	}
}

func TestMalformedConfigurationCannotEscapeLoopback(t *testing.T) {
	_, _, mapping := onePending(t)
	// A world-readable file cannot be used to redirect events to an attacker.
	if err := os.Chmod(mapping, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMapping(mapping, fixtureRepo); err == nil {
		t.Fatal("public mapping accepted")
	}
	for _, u := range []string{"https://evil.example:443", "http://127.0.0.1:18080@evil.example:4444", "http://localhost:18080", "http://127.0.0.1:18080/../admin"} {
		if _, err := QualEndpoint(u); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", u)
		}
	}
}
