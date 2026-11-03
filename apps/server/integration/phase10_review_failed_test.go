//go:build integration

package integration

import (
	"testing"
)

// TestPhase10ReviewQueueIncludesLatestFailedReviewer is a contract regression
// for QUAL-19. All fixtures exist in the disposable integration PostgreSQL
// database. No simulated or synthetic failed Reviewer is written to QUAL.
func TestPhase10ReviewQueueIncludesLatestFailedReviewer(t *testing.T) {
	f := setup(t)
	root := f.server.URL + "/api/v1/projects/" + f.project.String()
	queue := root + "/review-queue"

	transition := func(item map[string]any, status string, key string) {
		t.Helper()
		getStatus, current, _ := request(t, "GET", root+"/work-items/"+item["id"].(string), f.agent, "", "", nil)
		if getStatus != 200 {
			t.Fatalf("get %s %d", key, getStatus)
		}
		ver := int(current["version"].(float64))
		actor := f.agent
		if item["created_by"] == principalFor(t, f, f.admin).String() {
			actor = f.admin
		}
		st, updated, _ := request(t, "POST", root+"/work-items/"+item["id"].(string)+"/transitions", actor, key, "",
			map[string]any{"status": status, "expected_version": ver})
		if st != 200 || updated["status"] != status {
			t.Fatalf("transition %s %s: %d %v", key, status, st, updated)
		}
	}
	active := func(item map[string]any, key string) {
		transition(item, "todo", key+"-todo")
		transition(item, "in_progress", key+"-progress")
	}
	review := func(item map[string]any, outcome string) map[string]any {
		t.Helper()
		st, record, _ := request(t, "GET", root+"/work-items/"+item["id"].(string), f.admin, "", "", nil)
		if st != 200 {
			t.Fatal("work record not readable")
		}
		ver := int(record["version"].(float64))
		st, role, _ := request(t, "POST", roleEndpoint(f, item["id"].(string)), f.admin,
			"qual19-review-"+outcome+"-"+item["id"].(string), "",
			roleInput("reviewer", outcome, "git_commit", "abc1234",
				"Actual isolated PostgreSQL role fixture: verdict "+outcome, ver))
		if st != 201 {
			t.Fatalf("review %s returned %d %v", outcome, st, role)
		}
		return role
	}
	// In-progress latest reviewer failed is the branch that old SQL omits.
	failedProgress := create(t, f, f.agent, "qual19-failed-progress")
	active(failedProgress, "qual19-failed-progress")
	if record := review(failedProgress, "failed"); record["self_review"] != false {
		t.Fatalf("independent failed record incorrect %v", record)
	}

	failedReview := create(t, f, f.agent, "qual19-failed-review")
	active(failedReview, "qual19-failed-review")
	transition(failedReview, "in_review", "qual19-failed-review-pending")
	review(failedReview, "failed")

	needs := create(t, f, f.agent, "qual19-needs-work")
	active(needs, "qual19-needs")
	review(needs, "needs_work")

	approved := create(t, f, f.agent, "qual19-passed-review")
	active(approved, "qual19-pass")
	transition(approved, "in_review", "qual19-pass-pending")
	if recorded := review(approved, "passed"); recorded["self_review"] != false {
		t.Fatal("independent passed not recognized")
	}

	self := create(t, f, f.admin, "qual19-self-review")
	transition(self, "todo", "qual19-self-todo")
	transition(self, "in_progress", "qual19-self-progress")
	transition(self, "in_review", "qual19-self-pending")
	if recorded := review(self, "passed"); recorded["self_review"] != true {
		t.Fatal("self review not marked")
	}

	awaiting := create(t, f, f.agent, "qual19-awaiting")
	active(awaiting, "qual19-await")
	transition(awaiting, "in_review", "qual19-await-pending")

	done := create(t, f, f.agent, "qual19-done")
	active(done, "qual19-done")
	transition(done, "in_review", "qual19-done-pending")
	review(done, "passed")
	st, current, _ := request(t, "GET", root+"/work-items/"+done["id"].(string), f.admin, "", "", nil)
	if st != 200 {
		t.Fatal("done read failed")
	}
	ver := int(current["version"].(float64))
	// Preserve the forbidden legacy status transition as a regression check.
	st, _, _ = request(t, "POST", root+"/work-items/"+done["id"].(string)+"/transitions",
		f.admin, "qual19-done-legacy-denied", "", map[string]any{"status": "done", "expected_version": ver})
	if st != 422 {
		t.Fatalf("legacy Done Gate bypass should be rejected, got %d", st)
	}
	completeWithIndependentEvidence(t, f, done["id"].(string), f.admin, "qual19-verified-done")

	unreviewed := create(t, f, f.agent, "qual19-unreviewed")
	active(unreviewed, "qual19-unreviewed")

	status, page, headers := request(t, "GET", queue+"?limit=100", f.viewer, "", "", nil)
	if status != 200 || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer cannot read secured queue %d", status)
	}
	results := page["items"].([]any)
	seen := map[string]map[string]any{}
	for _, value := range results {
		record := value.(map[string]any)
		seen[record["work_item_id"].(string)] = record
	}
	expected := map[string]string{
		failedProgress["id"].(string): "needs_work",
		failedReview["id"].(string):   "needs_work",
		needs["id"].(string):          "needs_work",
		approved["id"].(string):       "ready_to_close",
		self["id"].(string):           "awaiting",
		awaiting["id"].(string):       "awaiting",
	}
	if len(seen) != len(expected) {
		t.Fatalf("QUAL-19 P0: expected %d review queue entries; got %d: %v", len(expected), len(seen), page)
	}
	for id, lane := range expected {
		row, ok := seen[id]
		if !ok {
			t.Errorf("QUAL-19 missing work item %s with expected lane %s", id, lane)
			continue
		}
		if row["review_lane"] != lane {
			t.Errorf("wrong lane for %s: got %v want %s", id, row["review_lane"], lane)
		}
	}
	for _, id := range []string{done["id"].(string), unreviewed["id"].(string)} {
		if _, found := seen[id]; found {
			t.Fatalf("closed/unreviewed %s unexpectedly in queue", id)
		}
	}
	if status, _, _ := request(t, "GET", queue, f.reporter, "", "", nil); status != 403 {
		t.Errorf("test-only reporter unexpectedly gained work:read: %d", status)
	}
	if status, _, _ := request(t, "GET", root+"/audit", f.viewer, "", "", nil); status != 403 {
		t.Errorf("work:read elevated audit:read: %d", status)
	}
	if status, _, _ := request(t, "POST", roleEndpoint(f, failedProgress["id"].(string)), f.agent,
		"qual19-unprivileged-review", "", roleInput("reviewer", "passed", "git_commit", "abc1234", "Agent cannot approve after queue GET", 3)); status != 403 {
		t.Errorf("read-only queue elevated reviewer to agent: %d", status)
	}
}
