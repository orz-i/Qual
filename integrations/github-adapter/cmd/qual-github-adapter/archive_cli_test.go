package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func TestArchiveCommandRequiresExplicitApprovalAndPreservesReceipts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	t.Setenv("QUAL_GITHUB_REPO_ID", "1443998164")
	t.Setenv("QUAL_GITHUB_REPOSITORY", "orz-i/Qual")
	t.Setenv("QUAL_GITHUB_SPOOL_DIR", root)
	spool, err := githubadapter.OpenSpool(root)
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
	if fresh, err := spool.Enqueue(d); err != nil || !fresh {
		t.Fatalf("fixture enqueue: %v", err)
	}
	d.Attempts = 1
	d.DeliveredEventID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := spool.Update(d, true); err != nil {
		t.Fatal(err)
	}
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"adapter", "archive", "1"} // no --approve
	if err := run(); err == nil {
		t.Fatal("operator archive accepted missing --approve")
	}
	if cap, err := spool.Capacity(); err != nil || cap.DeliveredUsed != 1 {
		t.Fatalf("unapproved CLI mutated queue: %+v %v", cap, err)
	}
	os.Args = []string{"adapter", "archive", "1", "--approve"}
	if err := run(); err != nil {
		t.Fatalf("approved bounded archive failed: %v", err)
	}
	restarted, err := githubadapter.OpenSpool(root)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := restarted.Capacity()
	if err != nil || cap.DeliveredUsed != 0 || cap.ArchivedUsed != 1 {
		t.Fatalf("archiving lost receipt or failed to free hot capacity: %+v %v", cap, err)
	}
}
