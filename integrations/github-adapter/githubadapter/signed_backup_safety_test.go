package githubadapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotDestinationBudgetFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stats   filesystemStats
		payload uint64
		records int
		fail    bool
	}{
		{name: "no-stats", stats: filesystemStats{}, payload: 1024, records: 1, fail: true},
		{name: "below-reserve", stats: filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 32 << 20}, payload: 1024, records: 1, fail: true},
		{name: "payload-cannot-fit", stats: filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 128 << 20}, payload: 100 << 20, records: 30000, fail: true},
		{name: "can-fit", stats: filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 256 << 20}, payload: 16 << 20, records: 100, fail: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBackupDestinationSpace(tc.stats, tc.payload, tc.records)
			if (err != nil) != tc.fail {
				t.Fatalf("snapshot accepted unsafe/free amount or refused adequate storage: %v", err)
			}
		})
	}
}

func TestSnapshotNoReplacePublishNeverClobbersOwnerFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "temporary")
	dst := filepath.Join(root, "existing-snapshot.sig")
	if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("authoritative old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishSnapshotFileNoReplace(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("snapshot overwrote existing output: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil || string(content) != "authoritative old" {
		t.Fatal("existing archive signature changed")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("failed no-replace publish destroyed staged source")
	}
}
