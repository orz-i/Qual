//go:build integration

package integration

import (
	"fmt"
	"testing"
)

func TestPhase10ReviewQueueScopeStatusAndLatestReviewerDecision(t *testing.T) {
	f := setup(t)
	root := f.server.URL + "/api/v1/projects/" + f.project.String()
	queue := root + "/review-queue"
	toStatus := func(item map[string]any, status string, ver int, key string) {
		t.Helper()
		path := root + "/work-items/" + item["id"].(string) + "/transitions"
		code, updated, _ := request(t, "POST", path, f.agent, key, "",
			map[string]any{"status": status, "expected_version": ver})
		if code != 200 || updated["status"] != status {
			t.Fatalf("transition %s: %d %v", status, code, updated)
		}
	}
	toActive := func(item map[string]any, suffix string) {
		toStatus(item, "todo", 1, "p10-q16-todo-"+suffix)
		toStatus(item, "in_progress", 2, "p10-q16-progress-"+suffix)
	}
	// Independent Human reviewer requests changes while an Agent implements.
	needs := create(t, f, f.agent, "p10-q16-needs-work")
	toActive(needs, "a")
	needsID := needs["id"].(string)
	reviewer := principalFor(t, f, f.admin).String()
	path := roleEndpoint(f, needsID)
	code, review, _ := request(t, "POST", path, f.admin, "p10-q16-review-needs-work", "",
		roleInput("reviewer", "needs_work", "git_commit", "abc1234", "Human needs a correction; no approval", 3))
	if code != 201 || review["self_review"] != false {
		t.Fatalf("independent Reviewer needs-work failed %d %v", code, review)
	}
	// This WorkItem is in_review even if no reviewer decision exists yet.
	ready := create(t, f, f.agent, "p10-q16-awaiting-review")
	toActive(ready, "b")
	toStatus(ready, "in_review", 3, "p10-q16-review-transition")
	// A real reviewer may also be the original author; that is explicitly self_review.
	self := create(t, f, f.admin, "p10-q16-self-review")
	selfID := self["id"].(string)
	selfPath := roleEndpoint(f, selfID)
	for i, state := range []string{"todo", "in_progress"} {
		code, _, _ := request(t, "POST", root+"/work-items/"+selfID+"/transitions", f.admin, fmt.Sprintf("p10-q16-self-state-%d", i), "",
			map[string]any{"status": state, "expected_version": i + 1})
		if code != 200 {
			t.Fatalf("self work transition %s %d", state, code)
		}
	}
	code, selfReview, _ := request(t, "POST", selfPath, f.admin, "p10-q16-self-review-entry", "",
		roleInput("reviewer", "needs_work", "none", "", "Same original Human reviews own work; not independent", 3))
	if code != 201 || selfReview["self_review"] != true {
		t.Fatalf("self-review not preserved %d %v", code, selfReview)
	}
	// In Review + latest needs_work MUST remain in the整改 lane even if an
	// executor has not yet performed the authorized CAS transition to in_progress.
	contradictory := create(t, f, f.agent, "p10-q16-review-stale-needs-work")
	toActive(contradictory, "stale")
	toStatus(contradictory, "in_review", 3, "p10-q16-stale-in-review")
	contradictoryID := contradictory["id"].(string)
	code, _, _ = request(t, "POST", roleEndpoint(f, contradictoryID), f.admin, "p10-q16-stale-review-decision", "",
		roleInput("reviewer", "needs_work", "none", "", "Review rejected; WorkItem status not yet reverted", 4))
	if code != 201 {
		t.Fatalf("cannot record contradictory needs_work %d", code)
	}

	// Independent passed while WorkItem stays in_review is NOT Done. It is
	// a separate pending status-transition lane.
	independentPassed := create(t, f, f.agent, "p10-q16-approved-but-not-done")
	toActive(independentPassed, "approved")
	toStatus(independentPassed, "in_review", 3, "p10-q16-approved-in-review")
	independentPassedID := independentPassed["id"].(string)
	code, approved, _ := request(t, "POST", roleEndpoint(f, independentPassedID), f.admin, "p10-q16-approve-still-pending", "",
		roleInput("reviewer", "passed", "git_commit", "abc1234", "Independent review passed, no Done mutation", 4))
	if code != 201 || approved["self_review"] != false {
		t.Fatalf("independent passed not recorded %d %v", code, approved)
	}

	// Passing one's own task is insufficient for independent approval.
	selfPassed := create(t, f, f.admin, "p10-q16-self-passed-in-review")
	selfPassedID := selfPassed["id"].(string)
	for i, st := range []string{"todo", "in_progress", "in_review"} {
		code, _, _ = request(t, "POST", root+"/work-items/"+selfPassedID+"/transitions", f.admin, fmt.Sprintf("p10-q16-self-passed-status-%d", i), "",
			map[string]any{"status": st, "expected_version": i + 1})
		if code != 200 {
			t.Fatalf("self passed setup %d", code)
		}
	}
	code, own, _ := request(t, "POST", roleEndpoint(f, selfPassedID), f.admin, "p10-q16-self-passed-review", "",
		roleInput("reviewer", "passed", "git_commit", "abc1234", "I reviewed my own task, not independent", 4))
	if code != 201 || own["self_review"] != true {
		t.Fatalf("self passed flag wrong %d %v", code, own)
	}

	// A real, later in_progress -> in_review CAS transition after an old
	// needs_work requests a fresh review. It MUST show as awaiting, while
	// retaining the immutable needs_work RoleEntry in latest_review.
	// A comment/version bump alone must NOT be interpreted as re-submission.
	resubmitted := create(t, f, f.agent, "p10-q16-resubmit-after-needs-work")
	toActive(resubmitted, "resubmit")
	resubmittedID := resubmitted["id"].(string)
	toStatus(resubmitted, "in_review", 3, "p10-q16-original-review")
	code, originalNeeds, _ := request(t, "POST", roleEndpoint(f, resubmittedID), f.admin,
		"p10-q16-original-needs-work", "", roleInput("reviewer", "needs_work", "none", "",
			"Human explicitly requested a correction before re-review", 4))
	if code != 201 || originalNeeds["work_version"] != float64(5) {
		t.Fatalf("original needs_work not preserved %d %v", code, originalNeeds)
	}
	toStatus(resubmitted, "in_progress", 5, "p10-q16-rework")
	code, _, _ = request(t, "POST", root+"/work-items/"+resubmittedID+"/comments",
		f.agent, "p10-q16-fix-note", "", map[string]any{
			"body":             "Executor records a concrete remediation before re-submitting; not an approval",
			"expected_version": 6,
		})
	if code != 201 {
		t.Fatalf("executor remediation comment failed %d", code)
	}
	toStatus(resubmitted, "in_review", 7, "p10-q16-actual-resubmit")

	// In progress with no review, or backlog with an old review, is not a queue item.
	noReview := create(t, f, f.agent, "p10-q16-unreviewed")
	toActive(noReview, "c")
	_ = create(t, f, f.agent, "p10-q16-backlog")

	for _, scenario := range []struct {
		name, url, token string
		expect           int
	}{
		{"anonymous", queue, "", 401},
		{"test-only reporter", queue, f.reporter, 403},
		{"cross-project", f.server.URL + "/api/v1/projects/" + f.otherProject.String() + "/review-queue", f.agent, 404},
		{"invalid limit", queue + "?limit=101", f.viewer, 400},
		{"duplicate limit", queue + "?limit=1&limit=2", f.viewer, 400},
		{"invalid offset", queue + "?offset=-1", f.viewer, 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			status, _, _ := request(t, "GET", scenario.url, scenario.token, "", "", nil)
			if status != scenario.expect {
				t.Fatalf("expected %d got %d", scenario.expect, status)
			}
		})
	}
	status, page, headers := request(t, "GET", queue+"?limit=100", f.viewer, "", "", nil)
	if status != 200 || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer queue %d cache=%v %v", status, headers.Get("Cache-Control"), page)
	}
	all := page["items"].([]any)
	if len(all) != 7 || page["has_more"] != false {
		t.Fatalf("queue should have three scoped items %v", page)
	}
	seen := map[string]map[string]any{}
	for _, v := range all {
		row := v.(map[string]any)
		seen[row["work_item_id"].(string)] = row
		if len(row) > 12 {
			t.Fatalf("review projection includes unexpected data %v", row)
		}
	}
	if len(seen) != 7 || seen[needsID]["status"] != "in_progress" ||
		seen[ready["id"].(string)]["status"] != "in_review" || seen[selfID]["status"] != "in_progress" {
		t.Fatalf("incorrect queue entries %v", seen)
	}
	for workID, expected := range map[string]string{
		needsID: "needs_work", selfID: "needs_work", contradictoryID: "needs_work",
		independentPassedID: "ready_to_close", selfPassedID: "awaiting", ready["id"].(string): "awaiting",
		resubmittedID: "awaiting",
	} {
		if seen[workID]["review_lane"] != expected {
			t.Fatalf("review lane for %s: %v want %s", workID, seen[workID], expected)
		}
	}
	if seen[contradictoryID]["status"] != "in_review" {
		t.Fatal("status was rewritten while classifying review")
	}
	if seen[resubmittedID]["status"] != "in_review" ||
		seen[resubmittedID]["resubmitted_after_review"] != true ||
		seen[resubmittedID]["latest_review"].(map[string]any)["outcome"] != "needs_work" ||
		seen[contradictoryID]["resubmitted_after_review"] != false {
		t.Fatalf("explicit resubmission or immutable former reviewer history missing %v", seen)
	}
	if _, has := seen[noReview["id"].(string)]; has {
		t.Fatal("nonreviewed active WorkItem in queue")
	}
	latest := seen[needsID]["latest_review"].(map[string]any)
	if latest["outcome"] != "needs_work" || latest["self_review"] != false || latest["actor_id"] != reviewer ||
		latest["reference_kind"] != "git_commit" || latest["reference_value"] != "abc1234" {
		t.Fatalf("false independent Reviewer outcome %v", latest)
	}
	if seen[selfID]["latest_review"].(map[string]any)["self_review"] != true {
		t.Fatal("self review marker lost")
	}
	if _, has := seen[ready["id"].(string)]["latest_review"]; has {
		t.Fatal("invented a review for pending WorkItem")
	}
	if status, _, _ := request(t, "GET", root+"/audit", f.viewer, "", "", nil); status != 403 {
		t.Fatalf("queue read elevated audit scope %d", status)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "p10-q16-forbidden-review", "",
		roleInput("reviewer", "passed", "git_commit", "abc1234", "Agent cannot review", 4)); status != 403 {
		t.Fatalf("queue read elevated work:review %d", status)
	}
	for i := 0; i < 7; i++ {
		status, p, _ := request(t, "GET", fmt.Sprintf("%s?limit=1&offset=%d", queue, i), f.viewer, "", "", nil)
		if status != 200 || len(p["items"].([]any)) != 1 || (p["has_more"] != (i < 6)) {
			t.Fatalf("paged queue %d %v", status, p)
		}
		if i < 6 && p["next_offset"] != float64(i+1) {
			t.Fatalf("pagination incorrect %v", p)
		}
	}
	// A newer independent passed decision replaces latest needs_work in the
	// projection without erasing the original immutable role entry.
	status, passed, _ := request(t, "POST", path, f.admin, "p10-q16-review-now-passed", "",
		roleInput("reviewer", "passed", "git_commit", "abc1234", "Issue reviewed, current latest no longer needs-work", 4))
	if status != 201 || passed["self_review"] != false {
		t.Fatalf("follow-up decision failed %d %v", status, passed)
	}
	status, page, _ = request(t, "GET", queue, f.viewer, "", "", nil)
	if status != 200 || len(page["items"].([]any)) != 6 {
		t.Fatalf("needs-work not removed after later decision: %d %v", status, page)
	}
	if status, p, _ := request(t, "GET", path, f.viewer, "", "", nil); status != 200 || len(p["items"].([]any)) != 2 {
		t.Fatalf("append-only history not preserved %d %v", status, p)
	}
	// Once a NEW Reviewer rejects the re-submitted work, the waiting state
	// changes back to needs_work without overwriting the earlier decision.
	status, newestRejection, _ := request(t, "POST", roleEndpoint(f, resubmittedID),
		f.admin, "p10-q16-reviewed-resubmission-and-rejected", "",
		roleInput("reviewer", "needs_work", "none", "",
			"New Human reviewer still requests one additional correction", 8))
	if status != 201 || newestRejection["work_version"] != float64(9) {
		t.Fatalf("new re-review decision missing %d %v", status, newestRejection)
	}
	status, page, _ = request(t, "GET", queue, f.viewer, "", "", nil)
	if status != 200 {
		t.Fatalf("queue after new rejection %d", status)
	}
	var resubmittedLatest map[string]any
	for _, raw := range page["items"].([]any) {
		item := raw.(map[string]any)
		if item["work_item_id"] == resubmittedID {
			resubmittedLatest = item
		}
	}
	if resubmittedLatest == nil || resubmittedLatest["review_lane"] != "needs_work" ||
		resubmittedLatest["resubmitted_after_review"] != false ||
		resubmittedLatest["latest_review"].(map[string]any)["id"] != newestRejection["id"] {
		t.Fatalf("new Reviewer needs_work did not supersede prior re-submission: %v", resubmittedLatest)
	}
	// Project denial is effective immediately for a previous work:read viewer.
	viewerID := principalFor(t, f, f.viewer)
	if code, _ := setPermission(t, f, f.admin, viewerID, "work:read", "deny", 0, "p10-q16-deny-viewer-work"); code != 200 {
		t.Fatalf("cannot revoke viewer %d", code)
	}
	if status, _, _ := request(t, "GET", queue, f.viewer, "", "", nil); status != 403 {
		t.Fatalf("revoked viewer continued reading queue %d", status)
	}
}
