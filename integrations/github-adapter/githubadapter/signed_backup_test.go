package githubadapter

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func realBackupFixture(t *testing.T) (*Spool, []Delivery) {
	t.Helper()
	s, ds := archiveFixture(t, 3)
	if _, err := s.ArchiveDelivered(2); err != nil {
		t.Fatal(err)
	}
	original, err := ParseDelivery(fixtureBody("edited"), "44444444-4444-4444-8444-444444444444",
		"pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := s.Enqueue(original); err != nil || !fresh {
		t.Fatalf("pending fixture: %v", err)
	}
	return s, ds
}

func TestSignedBackupIsolatedRestoreRetainsIdentityReplayAndFailedHistory(t *testing.T) {
	source, records := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-operator-key-", 3))
	archive := filepath.Join(t.TempDir(), "safe-spool.zip")
	created, err := source.CreateSignedBackup(archive, key)
	if err != nil || created.RecordCount != 6 {
		t.Fatalf("signed snapshot: %+v %v", created, err)
	}
	dest := filepath.Join(t.TempDir(), "isolated-restored-spool")
	restored, result, err := RestoreSignedBackup(archive, dest, key)
	if err != nil || restored == nil || result.RecordCount != created.RecordCount {
		t.Fatalf("isolated restore missing real source facts: %+v %v", result, err)
	}
	capacity, err := restored.Capacity()
	if err != nil || capacity.PendingUsed != 1 || capacity.DeliveredUsed != 1 || capacity.ArchivedUsed != 2 {
		t.Fatalf("restored state differs from source: %+v %v", capacity, err)
	}
	for _, d := range records[:2] {
		entry, found, err := restored.read("archived", d.DeliveryID)
		if err != nil || !found || entry.DeliveredEventID != d.DeliveredEventID {
			t.Fatalf("restored audit receipt changed: %+v %v", entry, err)
		}
		if created, err := restored.Enqueue(d); created || !errors.Is(err, ErrRestoredQuarantine) {
			t.Fatalf("restored spool accepted even same ID before independent reconciliation: %t %v", created, err)
		}
		replay := d
		replay.DeliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
		replay.State = "pending"
		replay.Attempts = 0
		replay.History = nil
		replay.DeliveredEventID = ""
		if fresh, err := restored.Enqueue(replay); fresh || !errors.Is(err, ErrRestoredQuarantine) {
			t.Fatalf("restored HMAC body incorrectly acknowledged before reconciliation: %t %v", fresh, err)
		}
		tamper := d
		tamper.PayloadSHA256 = strings.Repeat("f", 64)
		if _, err := restored.Enqueue(tamper); !errors.Is(err, ErrRestoredQuarantine) {
			t.Fatalf("restored spoofed body bypassed quarantine: %v", err)
		}
	}
	// The producer's historical spool remains intact, not replaced by restore.
	if before, err := source.Capacity(); err != nil || before.ArchivedUsed != 2 {
		t.Fatalf("backup unexpectedly modified source: %+v %v", before, err)
	}
}

func TestSignedBackupRejectsTamperMissingSignatureWrongKeyAndTargetOverwrite(t *testing.T) {
	s, _ := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-private-operator-key-", 2))
	backup := filepath.Join(t.TempDir(), "bundle.zip")
	if _, err := s.CreateSignedBackup(backup, key); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"wrong-key", "tampered-zip", "missing-signature", "public-signature", "target-exists"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			local := filepath.Join(dir, "snapshot.zip")
			data, err := os.ReadFile(backup)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "tampered-zip" {
				data[len(data)-4] ^= 1
			}
			if err := os.WriteFile(local, data, 0600); err != nil {
				t.Fatal(err)
			}
			sig, err := os.ReadFile(backup + ".sig")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "missing-signature" {
				if err := os.WriteFile(local+".sig", sig, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "public-signature" {
				if err := os.Chmod(local+".sig", 0644); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(dir, "destination")
			if mode == "target-exists" {
				if err := os.Mkdir(dest, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dest, "keep"), []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			useKey := key
			if mode == "wrong-key" {
				useKey = []byte(strings.Repeat("different-synthetic-key-", 2))
			}
			if _, _, err := RestoreSignedBackup(local, dest, useKey); err == nil {
				t.Fatal("signature failure or existing target accepted")
			}
			if mode == "target-exists" {
				original, err := os.ReadFile(filepath.Join(dest, "keep"))
				if err != nil || string(original) != "existing" {
					t.Fatal("restore modified existing target")
				}
			} else if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("failed verify wrote a target before trust: %v", err)
			}
		})
	}
}

func TestSignedRestoreRefusesSignedZipSlipAndInvalidInputPaths(t *testing.T) {
	s, _ := realBackupFixture(t)
	key := []byte(strings.Repeat("synthetic-op-key-", 3))
	archive := filepath.Join(t.TempDir(), "official.zip")
	for _, bad := range []string{"relative.zip", "../backup.zip", s.Root} {
		if _, err := s.CreateSignedBackup(bad, key); err == nil {
			t.Fatalf("unsafe backup destination accepted: %s", bad)
		}
	}
	if _, err := s.CreateSignedBackup(archive, []byte("weak")); err == nil {
		t.Fatal("short HMAC signing key accepted")
	}
	if _, err := s.CreateSignedBackup(archive, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSignedBackup(archive, key); err == nil {
		t.Fatal("existing signed backup was overwritten")
	}
	// A malicious ZIP signed with the test key is still invalid on structure.
	out := filepath.Join(t.TempDir(), "malicious.zip")
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	entry, err := zw.Create("../outside.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(b.Bytes())
	if err := os.WriteFile(out+".sig", []byte(hex.EncodeToString(mac.Sum(nil))+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restore")
	if _, _, err := RestoreSignedBackup(out, dest, key); err == nil {
		t.Fatal("signed malicious path escaped archive entry validation")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("zip-slip restore created target: %v", err)
	}
}
