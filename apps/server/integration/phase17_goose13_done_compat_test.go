//go:build integration

package integration

import "testing"

// completeWithIndependentEvidence is the Goose13+ replacement for the old
// /transitions status=done shortcut. Every completion is backed by a real
// published TestCaseVersion, reported Attempt, WorkItem coverage and a
// distinct authenticated reviewer. It never weakens the actual Done Gate.
func completeWithIndependentEvidence(t *testing.T, f *fixture, itemID, completionToken, prefix string) map[string]any {
	t.Helper()
	caseID, _ := mustCase(t, f, prefix+"-case", "Explicit test evidence for legacy integration regression")
	versionID, _ := publishCase(t, f, caseID, prefix+"-publish", 1)
	run := mustRun(t, f, []string{versionID}, prefix+"-run")
	runID := run["id"].(string)
	runItemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	mustAttempt(t, f, runItemID, f.agent, prefix+"-real-attempt", "passed")

	status, linked := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, prefix+"-coverage", "", map[string]any{"work_item_id": itemID})
	if status != 201 {
		t.Fatalf("link actual TestRun case to WorkItem: %d %#v", status, linked)
	}
	path := f.url(f.project) + "/" + itemID
	status, item, _ := request(t, "GET", path, f.agent, "", "", nil)
	if status != 200 || item["status"] != "in_review" {
		t.Fatalf("work not in_review for completion %d %#v", status, item)
	}
	version := int(item["version"].(float64))
	status, reviewer, _ := request(t, "POST", roleEndpoint(f, itemID), f.admin,
		prefix+"-independent-review", "",
		roleInput("reviewer", "passed", "test_run", runID,
			"Independent test fixture reviewer verified real immutable evidence", version))
	if status != 201 || reviewer["self_review"] != false || reviewer["outcome"] != "passed" {
		t.Fatalf("independent Reviewer not persisted %d %#v", status, reviewer)
	}
	status, completed, _ := request(t, "POST", path+"/complete", completionToken,
		prefix+"-complete", "", reviewCompletion(runID, version+1, "independent_review", false))
	if status != 200 || completed["status"] != "done" {
		t.Fatalf("explicit independent Done Gate failed %d %#v", status, completed)
	}
	status, acceptance, _ := request(t, "GET", path+"/acceptances", f.admin, "", "", nil)
	if status != 200 || acceptance["acceptance_mode"] != "independent_review" ||
		acceptance["decision"] == nil {
		t.Fatalf("unverified or fabricated acceptance %d %#v", status, acceptance)
	}
	decision := acceptance["decision"].(map[string]any)
	if decision["independent"] != true || decision["explicit_self_review"] != false ||
		decision["reference_value"] != runID || decision["reviewer_entry_id"] != reviewer["id"] {
		t.Fatalf("acceptance not linked to real independent Reviewer/Run: %#v", decision)
	}
	return completed
}
