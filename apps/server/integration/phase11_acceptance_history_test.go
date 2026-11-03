//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPhase11ReadAcceptanceHistoryAndLegacyDone(t *testing.T) {
	gate13(t)
	f := setup(t)
	base := f.url(f.project)
	run := reviewRun(t, f, "acceptance-read")
	itemID := reviewTarget(t, f, f.agent, "acceptance-read-work", run)
	readPath := base + "/" + itemID + "/acceptances"
	code, initial, _ := request(t, "GET", readPath, f.agent, "", "", nil)
	if code != 200 || initial["acceptance_mode"] != "not_completed" || initial["decision"] != nil {
		t.Fatalf("uncompleted WorkItem assigned imaginary reviewer %d %#v", code, initial)
	}
	if code, _, _ := request(t, "GET", readPath, "", "", "", nil); code != 401 {
		t.Fatalf("anonymous read=%d", code)
	}
	if code, _, _ := request(t, "GET", f.url(f.otherProject)+"/"+itemID+"/acceptances", f.agent, "", "", nil); code != 404 {
		t.Fatalf("cross-project acceptance read=%d", code)
	}
	code, reviewer, _ := request(t, "POST", roleEndpoint(f, itemID), f.admin,
		"acceptance-read-review", "", roleInput("reviewer", "passed", "test_run", run, "Actual independent Reviewer", 4))
	if code != 201 || reviewer["self_review"] != false {
		t.Fatalf("independent review failed %d %#v", code, reviewer)
	}
	code, done, _ := request(t, "POST", completeURL(f, itemID), f.admin,
		"acceptance-read-complete", "", reviewCompletion(run, 5, "independent_review", false))
	if code != 200 || done["status"] != "done" {
		t.Fatalf("complete failed %d %#v", code, done)
	}
	code, h, _ := request(t, "GET", readPath, f.agent, "", "", nil)
	if code != 200 || h["acceptance_mode"] != "independent_review" || h["work_status"] != "done" {
		t.Fatalf("verified completion not returned %d %#v", code, h)
	}
	dec, ok := h["decision"].(map[string]any)
	if !ok || dec["independent"] != true || dec["explicit_self_review"] != false ||
		dec["acceptance_mode"] != "independent_review" || dec["reviewer_entry_id"] != reviewer["id"] ||
		dec["reference_value"] != run || dec["work_version_after"] != float64(6) {
		t.Fatalf("completion decision not grounded in actual reviewer %#v", h)
	}
	if _, hasCredential := dec["credential_id"]; hasCredential {
		t.Fatal("read endpoint exposed credential id")
	}
	// A legitimate later comment increments the aggregate version but MUST
	// NOT invalidate or rewrite the immutable v6 acceptance decision.
	code, commented, _ := request(t, "POST", base+"/"+itemID+"/comments", f.agent,
		"acceptance-read-post-done-comment", "",
		map[string]any{"body": "Post-acceptance note, not a second review", "expected_version": 6})
	if code != 201 || commented["version"] != float64(7) || commented["status"] != "done" {
		t.Fatalf("post-Done comment failed %d %#v", code, commented)
	}
	code, post, _ := request(t, "GET", readPath, f.agent, "", "", nil)
	if code != 200 || post["work_version"] != float64(7) || post["acceptance_mode"] != "independent_review" {
		t.Fatalf("real acceptance disappeared after comment %d %#v", code, post)
	}
	decision, ok := post["decision"].(map[string]any)
	if !ok || decision["work_version_after"] != float64(6) || decision["id"] != dec["id"] {
		t.Fatalf("post-Done comment rewrote immutable original reviewer acceptance %#v", post)
	}

	// Disposable only: represent pre-migration done history with no decision.
	old := create(t, f, f.admin, "historical completed before Goose13")
	oldID := old["id"].(string)
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE qual.work_items SET status='done' WHERE id=$1 AND project_id=$2`,
		oldID, f.project); err != nil {
		t.Fatal(err)
	}
	code, legacy, _ := request(t, "GET", base+"/"+oldID+"/acceptances", f.agent, "", "", nil)
	if code != 200 || legacy["acceptance_mode"] != "legacy_unclassified" || legacy["decision"] != nil {
		t.Fatalf("legacy Done was fabricated as approved %#v", legacy)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, oldID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("migrated test rewrote old acceptance history %d %v", count, err)
	}
}

func TestPhase11ReadAcceptanceHistoryFromPreMigrationGoose12(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual21_history12_") {
		t.Skip("only disposable Goose12 history test database")
	}
	f := setup(t)
	old := create(t, f, f.admin, "Goose12 existing historic Done")
	id := old["id"].(string)
	for i, to := range []string{"todo", "in_progress", "in_review", "done"} {
		code, _, _ := request(t, "POST", f.url(f.project)+"/"+id+"/transitions", f.admin,
			"history12-"+to, "", map[string]any{"status": to, "expected_version": i + 1})
		if code != 200 {
			t.Fatalf("Goose12 historic transition failed %s %d", to, code)
		}
	}
	code, h, _ := request(t, "GET", f.url(f.project)+"/"+id+"/acceptances", f.agent, "", "", nil)
	if code != 200 || h["acceptance_mode"] != "legacy_unclassified" || h["decision"] != nil {
		t.Fatalf("Goose12 history upgraded old Done to fake independent review %d %#v", code, h)
	}
	if _, err := uuid.Parse(h["work_item_id"].(string)); err != nil {
		t.Fatal(err)
	}
}
