package githubadapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A previously delivered original Body must never be silently re-admitted
// merely because BOTH persistent archive directories were removed before
// an adapter restart. This is a genuine local history-loss condition.
func TestArchiveDirectoriesLostNeverRegenerateOldSourceAsFresh(t *testing.T) {
	s, delivered := archiveFixture(t, 1)
	result, err := s.ArchiveDelivered(1)
	if err != nil || result.Archived != 1 {
		t.Fatalf("fixture archive: %+v %v", result, err)
	}
	for _, name := range []string{"archived", "archive-index"} {
		if err := os.RemoveAll(filepath.Join(s.Root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenSpool(s.Root); !errors.Is(err, ErrSpool) {
		t.Fatalf("restart silently regenerated erased archives: %v", err)
	}
	if _, err := OpenExistingSpool(s.Root); !errors.Is(err, ErrSpool) {
		t.Fatalf("existing-only restart silently repaired missing tombstone dirs: %v", err)
	}
	// The listener may have already been started when the directories were
	// lost; even direct Enqueue through that live Spool must now fail closed.
	replay := delivered[0]
	replay.DeliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	replay.State = "pending"
	replay.Attempts = 0
	replay.DeliveredEventID = ""
	replay.History = nil
	if created, err := s.Enqueue(replay); created || !errors.Is(err, ErrSpool) {
		t.Fatalf("lost SHA tombstones accepted signed Body under new UUID: new=%t err=%v", created, err)
	}
	if err := s.IngressReadiness(); !errors.Is(err, ErrSpool) {
		t.Fatalf("lost archive history advertised ready: %v", err)
	}
	for _, name := range []string{"archived", "archive-index"} {
		if _, err := os.Stat(filepath.Join(s.Root, name)); !os.IsNotExist(err) {
			t.Fatalf("fail-closed reopen recreated %s: %v", name, err)
		}
	}
}

func TestNewSpoolMustBeInitializedExplicitlyByOperator(t *testing.T) {
	root := filepath.Join(t.TempDir(), "new-source")
	if _, err := OpenExistingSpool(root); !errors.Is(err, ErrSpool) {
		t.Fatalf("existing-only opening initialized an unknown empty source: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("existing-only opening mutated nonexistent source directory: %v", err)
	}
	s, err := InitNewSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.IngressReadiness(); err != nil {
		t.Fatalf("explicitly initialized source unavailable: %v", err)
	}
	if _, err := InitNewSpool(root); err == nil {
		t.Fatal("second explicit init overwrote an existing history")
	}
	if _, err := OpenExistingSpool(root); err != nil {
		t.Fatalf("legitimate initialized source unreadable: %v", err)
	}
}
