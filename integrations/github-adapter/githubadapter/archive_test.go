package githubadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func archiveFixture(t *testing.T, count int) (*Spool, []Delivery) {
	t.Helper()
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	var out []Delivery
	for i := 0; i < count; i++ {
		var payload map[string]any
		if err := json.Unmarshal(fixtureBody("opened"), &payload); err != nil {
			t.Fatal(err)
		}
		payload["audit_unique_nonce"] = i
		bytes, _ := json.Marshal(payload)
		id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", i+1)
		d, err := ParseDelivery(bytes, id, "pull_request", fixtureRepo, fixtureNow.Add(time.Duration(i+1)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		d.ReceivedAt = fixtureNow.Add(time.Duration(i) * time.Minute)
		if created, err := s.Enqueue(d); err != nil || !created {
			t.Fatalf("enqueue %d: %t %v", i, created, err)
		}
		d.Attempts = 1
		d.DeliveredEventID = fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012x", i+1)
		if err := s.Update(d, true); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return s, out
}

func TestArchiveFreesHotCapacityWithoutLosingDeliveryIDOrSignedBodyDedupe(t *testing.T) {
	s, original := archiveFixture(t, 6)
	before, err := s.Capacity()
	if err != nil || before.DeliveredUsed != 6 || before.ArchivedUsed != 0 {
		t.Fatalf("capacity before: %+v %v", before, err)
	}
	result, err := s.ArchiveDelivered(4)
	if err != nil || result.Archived != 4 {
		t.Fatalf("operator archive failed: %+v %v", result, err)
	}
	after, err := s.Capacity()
	if err != nil || after.DeliveredUsed != 2 || after.ArchivedUsed != 4 ||
		after.PendingUsed != 0 {
		t.Fatalf("after: %+v %v", after, err)
	}
	for _, d := range original[:4] {
		saved, exists, err := s.read("archived", d.DeliveryID)
		if err != nil || !exists || saved.DeliveredEventID != d.DeliveredEventID {
			t.Fatalf("archive lost immutable delivered Event ID: %+v %v", saved, err)
		}
		if _, exists, err := s.read("delivered", d.DeliveryID); err != nil || exists {
			t.Fatalf("archived receipt still occupies hot delivered slot: %t %v", exists, err)
		}
		// Same original signed bytes but a different unsigned delivery ID.
		replay := d
		replay.DeliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
		replay.State = "pending"
		replay.Attempts = 0
		replay.DeliveredEventID = ""
		replay.History = nil
		if created, err := s.Enqueue(replay); err != nil || created {
			t.Fatalf("archived SHA allowed a replay under new delivery ID: %t %v", created, err)
		}
		if created, err := s.Enqueue(d); err != nil || created {
			t.Fatalf("archived ID no longer deduped: %t %v", created, err)
		}
		changed := d
		changed.PayloadSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		if _, err := s.Enqueue(changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("archived delivery ID rebound: %v", err)
		}
	}
	reopened, err := OpenSpool(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if capacity, err := reopened.Capacity(); err != nil || capacity.ArchivedUsed != 4 {
		t.Fatalf("reopened archive vanished: %+v %v", capacity, err)
	}
	newBody := original[0]
	newBody.DeliveryID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	newBody.PayloadSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	newBody.State = "pending"
	newBody.DeliveredEventID = ""
	newBody.Attempts = 0
	newBody.History = nil
	if created, err := reopened.Enqueue(newBody); err != nil || !created {
		t.Fatalf("new event unexpectedly rejected after hot compaction: %t %v", created, err)
	}
}

func TestArchiveRecoversIndexFsyncBeforeDeliveredMove(t *testing.T) {
	s, ds := archiveFixture(t, 1)
	index, err := s.archiveIndexPath(ds[0].PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	// Fault injection at the exact crash boundary: a fully synced hash index
	// exists but the old Delivered file has not yet moved into Archived.
	saved, found, err := s.read("delivered", ds[0].DeliveryID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err := s.atomicWrite(index, saved); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenSpool(s.Root)
	if err != nil {
		t.Fatalf("crash recovery rejected: %v", err)
	}
	if _, exists, err := restarted.read("delivered", ds[0].DeliveryID); err != nil || exists {
		t.Fatalf("crash recovery did not finalize archive move: exists=%t err=%v", exists, err)
	}
	a, exists, err := restarted.read("archived", ds[0].DeliveryID)
	if err != nil || !exists || a.DeliveredEventID != ds[0].DeliveredEventID {
		t.Fatalf("crash recovery lost durable event: %+v %v", a, err)
	}
	replay := a
	replay.DeliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	replay.State = "pending"
	replay.Attempts = 0
	replay.DeliveredEventID = ""
	replay.History = nil
	if created, err := restarted.Enqueue(replay); err != nil || created {
		t.Fatalf("recovery reopened unsigned replay: %t %v", created, err)
	}
}

func TestArchiveFailsClosedIfReceiptOrIndexIsMissingOrTampered(t *testing.T) {
	for _, change := range []string{"missing-index", "unsafe-index-mode", "mismatched-index", "symlink-index"} {
		t.Run(change, func(t *testing.T) {
			s, ds := archiveFixture(t, 1)
			if _, err := s.ArchiveDelivered(1); err != nil {
				t.Fatal(err)
			}
			index, err := s.archiveIndexPath(ds[0].PayloadSHA256)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing-index":
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
			case "unsafe-index-mode":
				if err := os.Chmod(index, 0644); err != nil {
					t.Fatal(err)
				}
			case "mismatched-index":
				if err := os.WriteFile(index, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-index":
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(s.Root, "archived", ds[0].DeliveryID+".json"), index); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := OpenSpool(s.Root); err == nil {
				t.Fatal("archive corruption allowed live ingress to restart")
			}
		})
	}
}

func TestArchiveOnlyMovesCommittedDeliveriesAndRejectsDangerousArgs(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(d); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{-1, 0, 501} {
		if _, err := s.ArchiveDelivered(n); !errors.Is(err, ErrInvalid) {
			t.Fatalf("operator archive accepted unsafe limit %d: %v", n, err)
		}
	}
	res, err := s.ArchiveDelivered(2)
	if err != nil || res.Archived != 0 {
		t.Fatalf("pending fact archived: %+v %v", res, err)
	}
	pending, err := s.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending fact vanished: %+v %v", pending, err)
	}
}

// Two previously supported crash boundaries may overlap: a completed receipt
// was written but its Pending copy remains, and an archive SHA index was later
// created. Startup must reconcile Pending before moving the completed receipt.
func TestArchiveAndPendingDualCrashRecoveryNeverResurrectsWork(t *testing.T) {
	for _, phase := range []string{"index-before-move", "already-moved-to-archive"} {
		t.Run(phase, func(t *testing.T) {
			s, records := archiveFixture(t, 1)
			d := records[0]
			pending := d
			pending.State = "pending"
			pending.Attempts = 0
			pending.DeliveredEventID = ""
			pending.History = nil
			src, err := s.path("pending", d.DeliveryID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.atomicWrite(src, pending); err != nil {
				t.Fatal(err)
			}
			if phase == "index-before-move" {
				dst, err := s.archiveIndexPath(d.PayloadSHA256)
				if err != nil {
					t.Fatal(err)
				}
				saved, exists, err := s.read("delivered", d.DeliveryID)
				if err != nil || !exists {
					t.Fatal(err)
				}
				if err := s.atomicWrite(dst, saved); err != nil {
					t.Fatal(err)
				}
			} else {
				// For the already archived case, perform the move first.
				// Directly use the code responsible for the completed record;
				// it must not silently erase an independent Pending fact.
				dst, err := s.archiveIndexPath(d.PayloadSHA256)
				if err != nil {
					t.Fatal(err)
				}
				saved, exists, err := s.read("delivered", d.DeliveryID)
				if err != nil || !exists {
					t.Fatal(err)
				}
				if err := s.atomicWrite(dst, saved); err != nil {
					t.Fatal(err)
				}
				if err := s.moveArchivedLocked(saved); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := OpenSpool(s.Root)
			if err != nil {
				t.Fatal(err)
			}
			pendingList, err := reopened.Pending()
			if err != nil || len(pendingList) != 0 {
				t.Fatalf("previously delivered event resurrected after overlapping crashes: %+v %v", pendingList, err)
			}
			receipt, exists, err := reopened.read("archived", d.DeliveryID)
			if err != nil || !exists || receipt.DeliveredEventID != d.DeliveredEventID {
				t.Fatalf("previously delivered Qual ID altered: %+v %v", receipt, err)
			}
		})
	}
}
