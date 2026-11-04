package githubadapter

import (
	"context"
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

func restoredFixture(t *testing.T) (*Spool, *Spool, string) {
	t.Helper()
	source, _ := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-operator-key-", 3))
	archive := filepath.Join(t.TempDir(), "old-valid.zip")
	snap, err := source.CreateSignedBackup(archive, key)
	if err != nil {
		t.Fatal(err)
	}
	restored, ret, err := RestoreSignedBackup(archive, filepath.Join(t.TempDir(), "restored"), key)
	if err != nil || ret.ArchiveSHA256 != snap.ArchiveSHA256 {
		t.Fatalf("restore: %+v %v", ret, err)
	}
	return source, restored, snap.ArchiveSHA256
}

func TestRestoredQuarantineBlocksIngressAndQualWriterAcrossRestart(t *testing.T) {
	source, restored, sha := restoredFixture(t)
	check := func(s *Spool) {
		t.Helper()
		state, err := s.RecoveryStatus()
		if err != nil || !state.Quarantined || !state.RequiresSourceReconciliation || state.SnapshotSHA256 != sha {
			t.Fatalf("restored backup not quarantined: %+v %v", state, err)
		}
		if _, err := NewHandler(fixtureRepo, []byte(strings.Repeat("w", 40)), s); !errors.Is(err, ErrRestoredQuarantine) {
			t.Fatalf("restored spool can start HTTP listener: %v", err)
		}
		d, err := ParseDelivery(fixtureBody("opened"), "77777777-7777-4777-8777-777777777777", "pull_request", fixtureRepo, fixtureNow)
		if err != nil {
			t.Fatal(err)
		}
		if created, err := s.Enqueue(d); created || !errors.Is(err, ErrRestoredQuarantine) {
			t.Fatalf("new webhook accepted by restored spool: %t %v", created, err)
		}
		var posts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); w.WriteHeader(201) }))
		defer server.Close()
		publisher := Publisher{Spool: s, Repository: fixtureRepo, QualURL: server.URL, Token: "synthetic-only"}
		if stats, err := publisher.PublishOnce(context.Background()); !errors.Is(err, ErrRestoredQuarantine) || stats.Attempted != 0 || posts.Load() != 0 {
			t.Fatalf("worker tried to post in quarantine: %+v %v posts=%d", stats, err, posts.Load())
		}
		cap, err := s.Capacity()
		if err != nil || cap.PendingUsed != 1 || !cap.IngressBlocked ||
			!cap.RestoreQuarantined || !cap.RequiresSourceReconciliation {
			t.Fatalf("read-only inspect must include the hard recovery block: %+v %v", cap, err)
		}
	}
	check(restored)
	reopened, err := OpenSpool(restored.Root)
	if err != nil {
		t.Fatal(err)
	}
	check(reopened)
	active, err := source.RecoveryStatus()
	if err != nil || active.Quarantined {
		t.Fatal("normal source unexpectedly quarantined", active, err)
	}
}

func TestQuarantineBlocksLocalMutableOperationsAndCorruptMarker(t *testing.T) {
	_, restored, _ := restoredFixture(t)
	jobs, err := restored.Pending()
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	job := jobs[0]
	job.Attempts++
	if err := restored.Update(job, false); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("Update permitted: %v", err)
	}
	target := Target{ProjectID: "11111111-1111-4111-8111-111111111111", WorkItemID: "22222222-2222-4222-8222-222222222222", ThreadID: "33333333-3333-4333-8333-333333333333"}
	if _, err := restored.PinTarget(job, target); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("Pin permitted: %v", err)
	}
	if err := restored.RequeueAfterReview(job.DeliveryID, "transient-verified"); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("requeue permitted: %v", err)
	}
	if _, err := restored.ArchiveDelivered(1); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("archive permitted: %v", err)
	}
	path := filepath.Join(restored.Root, recoveryMarkerFile)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(restored.Root); err == nil {
		t.Fatal("insecure marker accepted")
	}
}

func TestFreshSpoolStillAcceptsValidWebhook(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "fresh"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.RecoveryStatus()
	if err != nil || state.Quarantined {
		t.Fatal("fresh spool incorrectly quarantined", state, err)
	}
	secret := strings.Repeat("s", 40)
	h, err := NewHandler(fixtureRepo, []byte(secret), s)
	if err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return fixtureNow }
	raw := fixtureBody("opened")
	req := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signature(secret, raw))
	req.Header.Set("X-GitHub-Delivery", fixtureDeliveryID)
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("fresh signed webhook refused: %d", w.Code)
	}
}

// A single missing or corrupted quarantine metadata file must not silently
// promote a restored historical snapshot into an active production queue.
func TestQuarantineDualMarkerSingleLossMustFailClosed(t *testing.T) {
	for _, mode := range []string{"remove-primary", "remove-witness", "edit-witness"} {
		t.Run(mode, func(t *testing.T) {
			_, restored, _ := restoredFixture(t)
			p := filepath.Join(restored.Root, recoveryMarkerFile)
			w := filepath.Join(restored.Root, recoveryWitnessFile)
			switch mode {
			case "remove-primary":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "remove-witness":
				if err := os.Remove(w); err != nil {
					t.Fatal(err)
				}
			case "edit-witness":
				if err := os.WriteFile(w, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenSpool(restored.Root); err == nil {
				t.Fatal("missing/mismatched quarantine metadata activated a restored snapshot")
			}
		})
	}
}

// Re-backing up an old already-quarantined spool must NOT turn an old source
// into an active newly "fresh" spool. Every restore creates its own marker.
func TestRestoredQuarantineSurvivesSecondSignedBackupAndRestore(t *testing.T) {
	_, restored, _ := restoredFixture(t)
	key := []byte(strings.Repeat("synthetic-independent-key-", 3))
	bundle := filepath.Join(t.TempDir(), "second.zip")
	if _, err := restored.CreateSignedBackup(bundle, key); err != nil {
		t.Fatal(err)
	}
	second, _, err := RestoreSignedBackup(bundle, filepath.Join(t.TempDir(), "restored-again"), key)
	if err != nil {
		t.Fatal(err)
	}
	state, err := second.RecoveryStatus()
	if err != nil || !state.Quarantined || !state.RequiresSourceReconciliation {
		t.Fatalf("re-signing old queue bypassed quarantine: %+v %v", state, err)
	}
	if _, err := NewHandler(fixtureRepo, []byte(strings.Repeat("s", 40)), second); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("second restore could start listener without independent reconciliation: %v", err)
	}
}
