package githubadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A captured HMAC signature authenticates the JSON bytes, but does not sign
// X-GitHub-Delivery; an attacker must not be able to create another Event by
// substituting a new syntactically valid delivery UUID on the same payload.
func TestSignedPayloadCannotReplayWithAnotherUnsignedDeliveryID(t *testing.T) {
	spool, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 36)
	handler, err := NewHandler(fixtureRepo, []byte(secret), spool)
	if err != nil {
		t.Fatal(err)
	}
	handler.Now = func() time.Time { return fixtureNow }
	raw := fixtureBody("opened")
	sig := signature(secret, raw)
	for i, id := range []string{fixtureDeliveryID, "22222222-2222-4222-8222-222222222222"} {
		req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", id)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 202 {
			t.Fatalf("signed payload %d rejected: HTTP%d", i, rr.Code)
		}
		var got map[string]bool
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["new"] != (i == 0) {
			t.Fatalf("signed replay created a second delivery (iteration=%d, result=%v)", i, got)
		}
	}
	pending, err := spool.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("same payload created %d events: %v", len(pending), err)
	}
}

func TestCrashAfterDeliveredFsyncDoesNotOverwriteOrReplay(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "spool")
	spool, err := OpenSpool(folder)
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
	d.State = "delivered"
	d.Attempts = 1
	d.DeliveredEventID = testEventID
	path, _ := spool.path("delivered", d.DeliveryID)
	// Fault injection: a completed record has been synced, but the process
	// died before deleting its still-present pending representation.
	if err := spool.atomicWrite(path, d); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenSpool(folder)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Pending()
	if err != nil || len(got) != 0 {
		t.Fatalf("already delivered fact replayed after crash: %v, %v", got, err)
	}
	saved, found, err := restarted.read("delivered", fixtureDeliveryID)
	if err != nil || !found || saved.DeliveredEventID != testEventID {
		t.Fatalf("original receipt lost: %v %+v", err, saved)
	}
}

func TestRetriedDeliveryCannotBeReboundToDifferentApprovedTarget(t *testing.T) {
	folder := t.TempDir()
	spool, err := OpenSpool(filepath.Join(folder, "spool"))
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
	mapping := mappingFixture(t, folder, true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	now := fixtureNow
	p := Publisher{Spool: spool, Repository: fixtureRepo, MapPath: mapping, QualURL: server.URL,
		Token: "synthetic-integration-only", Now: func() time.Time { return now }}
	first, err := p.PublishOnce(context.Background())
	if err != nil || first.Attempted != 1 {
		t.Fatalf("first write failed: %+v %v", first, err)
	}
	other := Mapping{RepositoryID: fixtureRepo.ID, RepositoryName: fixtureRepo.FullName, Links: map[string]Target{
		"orz-i/Qual/pull/1": {
			ProjectID:  "77777777-7777-4777-8777-777777777777",
			WorkItemID: "88888888-8888-4888-8888-888888888888",
			ThreadID:   "99999999-9999-4999-8999-999999999999",
		},
	}}
	content, _ := json.Marshal(other)
	if err := os.WriteFile(mapping, content, 0600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	next, err := p.PublishOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next.Attempted != 0 || calls.Load() != 1 {
		t.Fatalf("silent cross-project mapping rebind after prior POST: %+v", next)
	}
	pending, err := spool.Pending()
	if err != nil || len(pending) != 1 || pending[0].State != "blocked_mapping" {
		t.Fatalf("mapping change not quarantined: %+v, %v", pending, err)
	}
}

func TestManualRequeueRequiresMatchedReasonAndRetainsHistory(t *testing.T) {
	spool, d, _ := onePending(t)
	d.State = "blocked_auth"
	d.Attempts = 1
	d.LastHTTPStatus = 403
	d.LastErrorCode = "QUAL_CREDENTIAL_OR_PROJECT_SCOPE_DENIED"
	if err := spool.Update(d, false); err != nil {
		t.Fatal(err)
	}
	if err := spool.RequeueAfterReview(d.DeliveryID, "link-repaired"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong reason accepted: %v", err)
	}
	if err := spool.RequeueAfterReview(d.DeliveryID, "auth-restored"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSpool(spool.Root)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := reopened.Pending()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("lost review event: %+v %v", jobs, err)
	}
	item := jobs[0]
	if item.State != "pending" || item.Attempts != 1 || len(item.History) != 2 ||
		item.History[0].State != "blocked_auth" ||
		item.History[1].Kind != "operator_requeue" ||
		item.History[1].ErrorCode != "QUAL_CREDENTIAL_OR_PROJECT_SCOPE_DENIED" ||
		item.History[1].ReasonCode != "auth-restored" {
		t.Fatalf("manual remediation erased prior failure or reason: %+v", item)
	}
	// The public update API must never accept silently removed history.
	item.History = nil
	item.Attempts = 2
	if err := reopened.Update(item, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed attempt/review history was accepted: %v", err)
	}
}

func TestCapacityInspectReportsBoundedUsageWithoutDeletion(t *testing.T) {
	spool, _, _ := onePending(t)
	capacity, err := spool.Capacity()
	if err != nil {
		t.Fatal(err)
	}
	if capacity.PendingUsed != 1 || capacity.DeliveredUsed != 0 ||
		capacity.PendingLimit != MaxPending || capacity.DeliveredLimit != MaxDelivered ||
		capacity.NearLimit || capacity.IngressBlocked {
		t.Fatalf("capacity reported inconsistent or fabricated state: %+v", capacity)
	}
}
