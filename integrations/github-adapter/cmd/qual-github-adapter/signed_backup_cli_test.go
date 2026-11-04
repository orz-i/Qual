package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func TestOperatorCLIRequiresApprovalAndIndependentBackupKey(t *testing.T) {
	temp := t.TempDir()
	root := filepath.Join(temp, "spool")
	t.Setenv("QUAL_GITHUB_REPO_ID", "1443998164")
	t.Setenv("QUAL_GITHUB_REPOSITORY", "orz-i/Qual")
	t.Setenv("QUAL_GITHUB_SPOOL_DIR", root)
	s, err := githubadapter.OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	d := githubadapter.Delivery{
		DeliveryID:   "11111111-1111-4111-8111-111111111111",
		RepositoryID: 1443998164, RepositoryName: "orz-i/Qual",
		ThreadKind: "pull_request", ExternalThreadID: "orz-i/Qual/pull/1",
		EventType: "pull_request.opened", OccurredAt: now, ReceivedAt: now,
		PayloadSHA256: strings.Repeat("a", 64), Summary: "GitHub PR #1 opened", State: "pending",
	}
	if created, err := s.Enqueue(d); err != nil || !created {
		t.Fatal(err)
	}
	archive := filepath.Join(temp, "signed.zip")
	restore := filepath.Join(temp, "isolated-restore")
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	os.Args = []string{"adapter", "backup", "--out", archive}
	if err := run(); err == nil {
		t.Fatal("backup without --approve was allowed")
	}
	os.Args = []string{"adapter", "backup", "--out", archive, "--approve"}
	t.Setenv("QUAL_GITHUB_BACKUP_HMAC_KEY", "")
	if err := run(); err == nil {
		t.Fatal("backup without independent signing key was allowed")
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatal("unapproved backup output exists")
	}
	t.Setenv("QUAL_GITHUB_BACKUP_HMAC_KEY", strings.Repeat("synthetic-local-test-only-", 2))
	if err := run(); err != nil {
		t.Fatalf("synthetic signed Operator backup failed: %v", err)
	}
	os.Args = []string{"adapter", "restore", "--file", archive, "--into", restore}
	if err := run(); err == nil {
		t.Fatal("restore without --approve was allowed")
	}
	os.Args = []string{"adapter", "restore", "--file", archive, "--into", restore, "--approve"}
	t.Setenv("QUAL_GITHUB_BACKUP_HMAC_KEY", "")
	if err := run(); err == nil {
		t.Fatal("restore without correct key was allowed")
	}
	if _, err := os.Stat(restore); !os.IsNotExist(err) {
		t.Fatal("unverified restore wrote target")
	}
	t.Setenv("QUAL_GITHUB_BACKUP_HMAC_KEY", strings.Repeat("synthetic-local-test-only-", 2))
	if err := run(); err != nil {
		t.Fatalf("isolated restore failed: %v", err)
	}
	fresh, err := githubadapter.OpenSpool(restore)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := fresh.Pending()
	if err != nil || len(pending) != 1 || pending[0].DeliveryID != d.DeliveryID {
		t.Fatalf("restored isolated spool lost original pending event: %+v %v", pending, err)
	}
}
