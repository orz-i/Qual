package githubadapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpoolAndMappingRejectWorldWritableParent(t *testing.T) {
	root := t.TempDir()
	untrusted := filepath.Join(root, "untrusted-parent")
	if err := os.Mkdir(untrusted, 0700); err != nil {
		t.Fatal(err)
	}
	// Simulate an attacker-controlled parent that could replace entries.
	if err := os.Chmod(untrusted, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(filepath.Join(untrusted, "spool")); !errors.Is(err, ErrSpool) {
		t.Fatalf("world-writable parent accepted for private spool: %v", err)
	}
	file := filepath.Join(untrusted, "mapping.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMapping(file, fixtureRepo); !errors.Is(err, ErrSpool) {
		t.Fatalf("world-writable mapping parent accepted: %v", err)
	}
	// Repairing its permissions makes the path usable again.
	if err := os.Chmod(untrusted, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(filepath.Join(untrusted, "spool")); err != nil {
		t.Fatalf("private owner directory unexpectedly denied: %v", err)
	}
}

func TestSpoolMetadataCannotBeReboundByUpdateOrReplay(t *testing.T) {
	spool, err := OpenSpool(filepath.Join(t.TempDir(), "private-spool"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := spool.Enqueue(original); err != nil || !inserted {
		t.Fatal(err)
	}
	// The natural key is not the entire immutable record. A different summary
	// or source name with the same raw body/delivery ID must still be rejected.
	for _, change := range []func(*Delivery){
		func(d *Delivery) { d.Summary = "Different derived summary" },
		func(d *Delivery) { d.RepositoryName = "attacker/Qual" },
		func(d *Delivery) { d.ThreadKind = "issue" },
	} {
		attempt := original
		change(&attempt)
		if fresh, err := spool.Enqueue(attempt); !errors.Is(err, ErrConflict) || fresh {
			t.Fatalf("immutable duplicate metadata overwritten: created=%v err=%v", fresh, err)
		}
	}
	for _, change := range []func(*Delivery){
		func(d *Delivery) { d.RepositoryID++ },
		func(d *Delivery) { d.RepositoryName = "attacker/Qual" },
		func(d *Delivery) { d.ThreadKind = "issue" },
		func(d *Delivery) { d.Summary = "tampered" },
		func(d *Delivery) { d.ReceivedAt = d.ReceivedAt.Add(time.Hour) },
		func(d *Delivery) { d.Attempts = -1 },
	} {
		attempt := original
		change(&attempt)
		if err := spool.Update(attempt, false); !errors.Is(err, ErrConflict) {
			t.Fatalf("tampered persisted metadata update accepted: %v", err)
		}
	}
	if err := spool.Update(original, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("completion without valid persisted Qual Event UUID accepted: %v", err)
	}
	pending, err := spool.Pending()
	if err != nil || len(pending) != 1 || pending[0].Summary != original.Summary ||
		pending[0].Attempts != 0 || pending[0].State != "pending" {
		t.Fatalf("immutable source fact was changed or erased: %v %v", pending, err)
	}
}
