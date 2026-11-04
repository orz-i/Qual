package githubadapter

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshSpoolRequiresTwoDurablePathBoundOriginWitnesses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fresh")
	spool, err := OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	left, err := trustedFile(filepath.Join(root, activeOriginFile))
	if err != nil {
		t.Fatal(err)
	}
	right, err := trustedFile(filepath.Join(root, activeOriginWitnessFile))
	if err != nil || !bytes.Equal(left, right) {
		t.Fatal("fresh origin witnesses missing or different")
	}
	if err := spool.requireActive(); err != nil {
		t.Fatal("newly created spool not usable", err)
	}
	if _, err := OpenSpool(root); err != nil {
		t.Fatal("valid fresh origin did not survive restart", err)
	}

	for _, mode := range []string{"remove-primary", "remove-witness", "remove-both", "alter-root"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			other := filepath.Join(parent, "active")
			_, err := OpenSpool(other)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{
				filepath.Join(other, activeOriginFile),
				filepath.Join(other, activeOriginWitnessFile),
			}
			switch mode {
			case "remove-primary":
				err = os.Remove(paths[0])
			case "remove-witness":
				err = os.Remove(paths[1])
			case "remove-both":
				err = os.Remove(paths[0])
				if err == nil {
					err = os.Remove(paths[1])
				}
			case "alter-root":
				for _, path := range paths {
					err = os.WriteFile(path, left, 0600)
					if err != nil {
						break
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenSpool(other); !errors.Is(err, ErrSpool) {
				t.Fatalf("missing/transplanted active-origin evidence accepted (%s): %v", mode, err)
			}
			direct := &Spool{Root: other}
			if err := direct.requireActive(); !errors.Is(err, ErrSpool) {
				t.Fatalf("direct Spool bypassed active-origin guard (%s): %v", mode, err)
			}
		})
	}
}

func TestRestoredOldSignedSnapshotCannotBecomeActiveByDeletingBothQuarantineFiles(t *testing.T) {
	source, restored, sha := restoredFixture(t)
	original, err := source.RecoveryStatus()
	if err != nil || original.Quarantined {
		t.Fatal("original live spool was inappropriately quarantined")
	}
	restoredState, err := restored.RecoveryStatus()
	if err != nil || !restoredState.Quarantined || restoredState.SnapshotSHA256 != sha {
		t.Fatal("restored snapshot was not quarantined")
	}
	if _, err := os.Stat(filepath.Join(restored.Root, activeOriginFile)); !os.IsNotExist(err) {
		t.Fatalf("restored snapshot unexpectedly inherited ACTIVE origin witness: %v", err)
	}
	for _, f := range []string{recoveryMarkerFile, recoveryWitnessFile} {
		if err := os.Remove(filepath.Join(restored.Root, f)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenSpool(restored.Root); !errors.Is(err, ErrSpool) {
		t.Fatalf("stripping BOTH quarantine markers activated old snapshot: %v", err)
	}
	if err := restored.requireActive(); !errors.Is(err, ErrSpool) {
		t.Fatalf("existing in-memory Spool bypassed missing origins: %v", err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := restored.Enqueue(d); fresh || !errors.Is(err, ErrSpool) {
		t.Fatalf("stripped-quarantine spool acknowledged signed replay: created=%t err=%v", fresh, err)
	}
	if _, err := NewHandler(fixtureRepo, []byte(strings.Repeat("s", 40)), restored); !errors.Is(err, ErrSpool) {
		t.Fatalf("stripped-quarantine spool created HTTP webhook handler: %v", err)
	}
	publisher := Publisher{Spool: restored, Repository: fixtureRepo, QualURL: "http://127.0.0.1:18080", Token: "synthetic-only"}
	if stats, err := publisher.PublishOnce(context.Background()); !errors.Is(err, ErrSpool) || stats.Attempted != 0 {
		t.Fatalf("stripped-quarantine spool attempted Qual write: %+v %v", stats, err)
	}
}

func TestExistingUnknownSpoolIsNeverAutoCertifiedAsFresh(t *testing.T) {
	root := filepath.Join(t.TempDir(), "preexisting-spool")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(root); !errors.Is(err, ErrSpool) {
		t.Fatalf("existing originless spool silently given trusted active identity: %v", err)
	}
	if err := os.Chtimes(root, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSpool(root); !errors.Is(err, ErrSpool) {
		t.Fatal("age or timestamps erroneously made untrusted spool active")
	}
}
