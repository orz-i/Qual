package githubadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opening an old signed snapshot must not silently "repair" immutable
// recovered facts. In an ACTIVE source, reconciling an exact crash overlap
// is safe; QUARANTINE is deliberately read-only pending trusted reconciliation.
func TestRestoredSpoolOpenNeverDeletesPendingCrashOverlap(t *testing.T) {
	source, originals := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-independent-readonly-operator-", 2))
	archive := filepath.Join(t.TempDir(), "old-snapshot.zip")
	if _, err := source.CreateSignedBackup(archive, key); err != nil {
		t.Fatal(err)
	}
	restored, _, err := RestoreSignedBackup(archive, filepath.Join(t.TempDir(), "restored"), key)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a synthetic crash-overlap Pending record corresponding exactly
	// to an archived immutable receipt (no real Qual Event exists here).
	archived, exists, err := restored.read("archived", originals[0].DeliveryID)
	if err != nil || !exists {
		t.Fatal("missing restored archived fixture", err)
	}
	pending := archived
	pending.State = "pending"
	pending.DeliveredEventID = ""
	path := filepath.Join(restored.Root, "pending", archived.DeliveryID+".json")
	raw, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(restored.Root); !errors.Is(err, ErrSpool) {
		t.Fatalf("read-only recovery silently repaired Pending overlap: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("recovered Pending disappeared during read-only inspection: %v", err)
	}
	if _, err := restored.RecoveryStatus(); err != nil {
		t.Fatal("quarantine evidence destroyed while rejecting malformed record", err)
	}
}

func TestRestoredSpoolOpenNeverCompletesIndexedHotMove(t *testing.T) {
	source, originals := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-independent-readonly-operator-", 2))
	archive := filepath.Join(t.TempDir(), "old-snapshot.zip")
	if _, err := source.CreateSignedBackup(archive, key); err != nil {
		t.Fatal(err)
	}
	restored, _, err := RestoreSignedBackup(archive, filepath.Join(t.TempDir(), "restored"), key)
	if err != nil {
		t.Fatal(err)
	}
	old, exists, err := restored.read("archived", originals[0].DeliveryID)
	if err != nil || !exists {
		t.Fatal(err)
	}
	oldPath := filepath.Join(restored.Root, "archived", old.DeliveryID+".json")
	hotPath := filepath.Join(restored.Root, "delivered", old.DeliveryID+".json")
	// This simulates a partial move crash. ACTIVE normal boot would reconcile
	// the valid SHA index. RESTORED boot must not mutate an old snapshot.
	if err := os.Rename(oldPath, hotPath); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(restored.Root); !errors.Is(err, ErrSpool) {
		t.Fatalf("quarantined read-only OpenSpool changed archived fact: %v", err)
	}
	if _, err := os.Stat(hotPath); err != nil {
		t.Fatal("read-only open moved hot receipt", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("read-only open created a new archived record")
	}
}
