//go:build integration

package integration

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Only disposable, explicitly migrated Goose13 databases may run these
// policy/completion tests. The default QUAL database is never a test target.
func gate13(t *testing.T) {
	t.Helper()
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual21_gate_") {
		t.Skip("QUAL-21 integration requires disposable qual21_gate_* database")
	}
}

func reviewTarget(t *testing.T, f *fixture, token, prefix string, linkedRun ...string) string {
	t.Helper()
	w := create(t, f, token, prefix+"-create")
	id := w["id"].(string)
	if len(linkedRun) > 0 {
		if len(linkedRun) != 1 {
			t.Fatal("only one explicit Run reference allowed")
		}
		status, run := testRequest(t, "GET", testsURL(f)+"/test-runs/"+linkedRun[0], f.agent, "", "", nil)
		if status != 200 {
			t.Fatalf("real Run missing for coverage %d", status)
		}
		items := run["items"].([]any)
		caseID := items[0].(map[string]any)["test_case_id"].(string)
		status, _ = testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
			f.agent, prefix+"-link-case-to-work", "", map[string]any{"work_item_id": id})
		if status != 201 {
			t.Fatalf("WorkItem-specific CaseVersion coverage missing %d", status)
		}
	}
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		code, updated, _ := request(t, "POST", f.url(f.project)+"/"+id+"/transitions", token,
			prefix+"-transition-"+next, "", map[string]any{"status": next, "expected_version": i + 1})
		if code != 200 || updated["status"] != next {
			t.Fatalf("prepare %s code=%d work=%v", next, code, updated)
		}
	}
	return id
}
func reviewRun(t *testing.T, f *fixture, prefix string) string {
	t.Helper()
	caseID, _ := mustCase(t, f, prefix+"-case", "QUAL-21 actual fixture TestCase")
	versionID, _ := publishCase(t, f, caseID, prefix+"-publish", 1)
	run := mustRun(t, f, []string{versionID}, prefix+"-run")
	item := run["items"].([]any)[0].(map[string]any)
	mustAttempt(t, f, item["id"].(string), f.agent, prefix+"-attempt-passed", "passed")
	return run["id"].(string)
}
func reviewCompletion(run string, expected int, mode string, self bool) map[string]any {
	return map[string]any{
		"acceptance_mode": mode, "expected_version": expected,
		"reference_kind": "test_run", "reference_value": run,
		"acknowledge_self_review": self,
		"reason":                  "Actual local fixture evidence reviewed; residual risk explicitly accepted.",
	}
}
func completeURL(f *fixture, id string) string { return f.url(f.project) + "/" + id + "/complete" }

func TestPhase11ExplicitReviewGateIndependentAndTransactional(t *testing.T) {
	gate13(t)
	f := setup(t)
	ctx := context.Background()
	runID := reviewRun(t, f, "gate21-independent")
	id := reviewTarget(t, f, f.agent, "gate21-independent", runID)
	base := f.url(f.project) + "/" + id
	completion := completeURL(f, id)
	data := reviewCompletion(runID, 5, "independent_review", false)

	// A privileged reviewer without the required real Reviewer:passed may
	// not complete merely by holding work:review.
	if code, _, _ := request(t, "POST", completion, f.admin, "gate21-no-review", "", reviewCompletion(runID, 4, "independent_review", false)); code != 422 {
		t.Fatalf("no reviewer completed %d", code)
	}
	if code, _, _ := request(t, "POST", completion, f.agent, "gate21-low-scope", "", reviewCompletion(runID, 4, "independent_review", false)); code != 403 {
		t.Fatalf("unprivileged Agent complete %d", code)
	}
	if code, _, _ := request(t, "POST", completion, "", "gate21-anonymous", "", reviewCompletion(runID, 4, "independent_review", false)); code != 401 {
		t.Fatalf("anonymous complete %d", code)
	}
	if code, _, _ := request(t, "POST", f.url(f.otherProject)+"/"+id+"/complete", f.agent, "gate21-cross-project", "", reviewCompletion(runID, 4, "independent_review", false)); code != 404 {
		t.Fatalf("unauthorized foreign project complete %d", code)
	}
	if code, _, _ := request(t, "POST", base+"/transitions", f.admin, "gate21-old-done-bypass", "",
		map[string]any{"status": "done", "expected_version": 4}); code != 422 {
		t.Fatalf("Goose13 legacy transition bypassed gate %d", code)
	}
	if code, _, _ := request(t, "POST", completion, f.admin, "gate21-untrusted-fields", "",
		map[string]any{"acceptance_mode": "owner_acceptance", "actor_id": uuid.NewString(), "expected_version": 4}); code != 400 {
		t.Fatalf("client forged Actor accepted %d", code)
	}
	// A valid same-project Run without explicit WorkItem coverage is still
	// unrelated evidence and cannot authorize a Done decision.
	unlinked := reviewTarget(t, f, f.agent, "gate21-unlinked")
	code, _, _ := request(t, "POST", roleEndpoint(f, unlinked), f.admin,
		"gate21-unlinked-review", "", roleInput("reviewer", "passed", "test_run", runID,
			"Independent Reviewer cited unrelated same-project Run", 4))
	if code != 201 {
		t.Fatalf("unlinked reviewer setup %d", code)
	}
	code, _, _ = request(t, "POST", completeURL(f, unlinked), f.admin,
		"gate21-unlinked-denied", "", reviewCompletion(runID, 5, "independent_review", false))
	if code != 422 {
		t.Fatalf("unrelated Run evidence accepted %d", code)
	}
	// Merely creating an empty TestRun is not evidence of a real execution.
	emptyCase, _ := mustCase(t, f, "gate21-empty-case", "No external attempts exist")
	emptyVersion, _ := publishCase(t, f, emptyCase, "gate21-empty-publish", 1)
	emptyRun := mustRun(t, f, []string{emptyVersion}, "gate21-empty-run")
	emptyWork := reviewTarget(t, f, f.agent, "gate21-empty", emptyRun["id"].(string))
	code, _, _ = request(t, "POST", roleEndpoint(f, emptyWork), f.admin,
		"gate21-empty-run-reviewed", "", roleInput("reviewer", "passed", "test_run", emptyRun["id"].(string), "Review label does not create test results", 4))
	if code != 201 {
		t.Fatalf("real reviewer entry setup rejected %d", code)
	}
	code, _, _ = request(t, "POST", completeURL(f, emptyWork), f.admin,
		"gate21-no-attempt-ref", "", reviewCompletion(emptyRun["id"].(string), 5, "independent_review", false))
	if code != 422 {
		t.Fatalf("empty TestRun was accepted as executed evidence %d", code)
	}

	code, reviewer, _ := request(t, "POST", roleEndpoint(f, id), f.admin, "gate21-independent-review", "",
		roleInput("reviewer", "passed", "test_run", runID, "Independent Human reviewed actual same-project run", 4))
	if code != 201 || reviewer["self_review"] != false || reviewer["work_version"] != float64(5) {
		t.Fatalf("independent Reviewer not recorded %d %v", code, reviewer)
	}
	if code, _, _ := request(t, "POST", completion, f.admin, "gate21-cas-stale", "", reviewCompletion(runID, 4, "independent_review", false)); code != 409 {
		t.Fatalf("old version not rejected %d", code)
	}
	// Verify one-shot success and idempotent replay with no duplicate writes.
	code, finished, _ := request(t, "POST", completion, f.admin, "gate21-independent-complete", "", data)
	if code != 200 || finished["status"] != "done" || finished["version"] != float64(6) {
		t.Fatalf("independent completion %d %v", code, finished)
	}
	code, replay, _ := request(t, "POST", completion, f.admin, "gate21-independent-complete", "", data)
	if code != 200 || replay["version"] != finished["version"] {
		t.Fatalf("idempotency replay %d %v", code, replay)
	}
	wrong := reviewCompletion(runID, 5, "independent_review", true)
	if code, _, _ := request(t, "POST", completion, f.admin, "gate21-independent-complete", "", wrong); code != 409 {
		t.Fatalf("reused key with different body %d", code)
	}
	var decisions, audit, activity, outbox int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.work_acceptance_decisions
      WHERE work_item_id=$1 AND acceptance_mode='independent_review' AND independent=true
        AND explicit_self_review=false AND reviewer_entry_id=$2`, id, reviewer["id"]).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		table, column string
		out           *int
	}{
		{"audit_records", "entity_id", &audit},
		{"activity_records", "entity_id", &activity},
		{"outbox_events", "aggregate_id", &outbox},
	} {
		err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual."+target.table+
			" WHERE "+target.column+"=$1 AND "+
			map[string]string{"audit_records": "operation", "activity_records": "operation", "outbox_events": "event_type"}[target.table]+"='work_item.completed'", id).Scan(target.out)
		if err != nil {
			t.Fatal(err)
		}
	}
	if decisions != 1 || audit != 1 || activity != 1 || outbox != 1 {
		t.Fatalf("not exactly one atomic record decision/audit/activity/outbox=%d/%d/%d/%d", decisions, audit, activity, outbox)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE qual.work_acceptance_decisions SET reason='tamper' WHERE work_item_id=$1`, id); err == nil {
		t.Fatal("immutable acceptance UPDATE accepted")
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, id); err == nil {
		t.Fatal("immutable acceptance DELETE accepted")
	}
}

func TestPhase11DoneGateStaleReviewerAndMaterialChange(t *testing.T) {
	gate13(t)
	f := setup(t)
	run := reviewRun(t, f, "gate21-stale")
	t.Run("latest needs_work supersedes Passed", func(t *testing.T) {
		id := reviewTarget(t, f, f.agent, "gate21-needswork", run)
		for i, outcome := range []string{"passed", "needs_work"} {
			status, _, _ := request(t, "POST", roleEndpoint(f, id), f.admin,
				"gate21-review-"+outcome, "", roleInput("reviewer", outcome, "test_run", run, "real Reviewer verdict", 4+i))
			if status != 201 {
				t.Fatalf("%s reviewer %d", outcome, status)
			}
		}
		code, _, _ := request(t, "POST", completeURL(f, id), f.admin, "gate21-old-passed-ignored", "", reviewCompletion(run, 6, "independent_review", false))
		if code != 422 {
			t.Fatalf("latest needs_work bypassed %d", code)
		}
	})
	t.Run("material update invalidates old passed", func(t *testing.T) {
		id := reviewTarget(t, f, f.agent, "gate21-material", run)
		status, _, _ := request(t, "POST", roleEndpoint(f, id), f.admin, "gate21-material-review", "", roleInput("reviewer", "passed", "test_run", run, "approved before update", 4))
		if status != 201 {
			t.Fatal(status)
		}
		code, updated, _ := request(t, "PATCH", f.url(f.project)+"/"+id, f.agent, "gate21-material-changed", `"5"`,
			map[string]any{"title": "changed material task after review"})
		if code != 200 || updated["version"] != float64(6) {
			t.Fatalf("update %d %v", code, updated)
		}
		code, _, _ = request(t, "POST", completeURL(f, id), f.admin, "gate21-old-material-review", "", reviewCompletion(run, 6, "independent_review", false))
		if code != 422 {
			t.Fatalf("material edit ignored %d", code)
		}
	})
	t.Run("owner's self review is never independent", func(t *testing.T) {
		id := reviewTarget(t, f, f.admin, "gate21-self", run)
		code, entry, _ := request(t, "POST", roleEndpoint(f, id), f.admin, "gate21-self-review", "", roleInput("reviewer", "passed", "test_run", run, "self review under real Human", 4))
		if code != 201 || entry["self_review"] != true {
			t.Fatalf("not self-review %d %v", code, entry)
		}
		code, _, _ = request(t, "POST", completeURL(f, id), f.admin, "gate21-self-independence", "", reviewCompletion(run, 5, "independent_review", false))
		if code != 422 {
			t.Fatalf("self review impersonated independent %d", code)
		}
	})
}

func ownerPolicyInput(expected int) map[string]any {
	return map[string]any{
		"default_mode":                    "owner_acceptance",
		"owner_acceptance_allowed":        true,
		"force_independent_for_high_risk": true,
		"expected_version":                expected,
		"reason":                          "Real authorized test Human Workspace Admin enables explicit Owner acceptance.",
	}
}

func authorizeOwnerPolicy(t *testing.T, f *fixture, key string) {
	t.Helper()
	endpoint := testsURL(f) + "/review-policy"
	code, before, _ := request(t, "GET", endpoint, f.agent, "", "", nil)
	if code != 200 || before["version"] != float64(0) ||
		before["default_mode"] != "independent_review" ||
		before["owner_acceptance_allowed"] != false {
		t.Fatalf("conservative policy default %d %v", code, before)
	}
	if code, _, _ := request(t, "PUT", endpoint, f.agent, key+"-agent-deny", "", ownerPolicyInput(0)); code != 403 {
		t.Fatalf("ordinary PI Agent changed project policy %d", code)
	}
	code, changed, _ := request(t, "PUT", endpoint, f.admin, key+"-admin-enable", "", ownerPolicyInput(0))
	if code != 200 || changed["version"] != float64(1) ||
		changed["default_mode"] != "owner_acceptance" ||
		changed["owner_acceptance_allowed"] != true {
		t.Fatalf("Human policy enable failed %d %v", code, changed)
	}
	if code, again, _ := request(t, "PUT", endpoint, f.admin, key+"-admin-enable", "", ownerPolicyInput(0)); code != 200 ||
		again["version"] != float64(1) {
		t.Fatalf("idempotent policy replay failed %d %v", code, again)
	}
	if code, _, _ := request(t, "PUT", endpoint, f.admin, key+"-stale", "", ownerPolicyInput(0)); code != 409 {
		t.Fatalf("stale policy CAS accepted %d", code)
	}
	changedBody := ownerPolicyInput(0)
	changedBody["reason"] = "different authorized reason for the same idempotency key"
	if code, _, _ := request(t, "PUT", endpoint, f.admin, key+"-admin-enable", "", changedBody); code != 409 {
		t.Fatalf("policy idempotency collision accepted %d", code)
	}
	unknownActor := ownerPolicyInput(1)
	unknownActor["actor_id"] = uuid.NewString()
	if code, _, _ := request(t, "PUT", endpoint, f.admin, key+"-spoof-actor", "", unknownActor); code != 400 {
		t.Fatalf("untrusted policy Actor accepted %d", code)
	}
	if code, after, _ := request(t, "GET", endpoint, f.agent, "", "", nil); code != 200 ||
		after["version"] != float64(1) || after["source"] != "project" {
		t.Fatalf("policy readback failed %d %v", code, after)
	}
	ctx := context.Background()
	var changes, audit, activity, outbox int
	for _, row := range []struct {
		table, column, op string
		count             *int
	}{
		{"review_policy_changes", "project_id", "", &changes},
		{"audit_records", "entity_id", "operation='project.review_policy_changed'", &audit},
		{"activity_records", "entity_id", "operation='project.review_policy_changed'", &activity},
		{"outbox_events", "aggregate_id", "event_type='project.review_policy_changed'", &outbox},
	} {
		query := "SELECT count(*) FROM qual." + row.table + " WHERE " + row.column + "=$1"
		if row.op != "" {
			query += " AND " + row.op
		}
		if err := f.pool.QueryRow(ctx, query, f.project).Scan(row.count); err != nil {
			t.Fatal(err)
		}
	}
	if changes != 1 || audit != 1 || activity != 1 || outbox != 1 {
		t.Fatalf("policy change did not atomically persist change/audit/activity/outbox %d/%d/%d/%d", changes, audit, activity, outbox)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM qual.review_policy_changes WHERE project_id=$1`, f.project); err == nil {
		t.Fatal("policy change history was not immutable")
	}
}

func TestPhase11AssignedAgentOwnerAcceptanceUnderExplicitProjectPermission(t *testing.T) {
	gate13(t)
	f := setup(t)
	ctx := context.Background()

	// Human administrator configures an auditable policy once. Subsequent
	// assigned Agent tasks need no additional Human reviewer per item.
	authorizeOwnerPolicy(t, f, "agent-owner-once")
	run := reviewRun(t, f, "agent-owner")
	w := create(t, f, f.agent, "agent-owner-created-by-agent")
	id := w["id"].(string)
	workURL := f.url(f.project) + "/" + id
	var accountable uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(accountable_owner_id, '00000000-0000-0000-0000-000000000000'::uuid)
        FROM qual.work_items WHERE id=$1`, id).Scan(&accountable); err != nil {
		t.Fatal(err)
	}
	if accountable != uuid.Nil {
		t.Fatal("fixture unexpectedly has explicit accountable owner")
	}

	// Attach the real, same-project published TestCase/Attempt to this task.
	code, r, _ := request(t, "GET", testsURL(f)+"/test-runs/"+run, f.agent, "", "", nil)
	if code != 200 {
		t.Fatal("run missing", code)
	}
	runItem := r["items"].([]any)[0].(map[string]any)
	caseID := runItem["test_case_id"].(string)
	if code, _, _ = request(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, "agent-owner-coverage", "", map[string]any{"work_item_id": id}); code != 201 {
		t.Fatalf("coverage missing: %d", code)
	}
	if code, _, _ = request(t, "POST", workURL+"/transitions",
		f.agent, "agent-owner-todo", "", map[string]any{"status": "todo", "expected_version": 1}); code != 200 {
		t.Fatalf("todo: %d", code)
	}
	if code, _, _ = request(t, "POST", workURL+"/claim", f.agent,
		"agent-owner-claim", "", map[string]any{"expected_version": 2}); code != 200 {
		t.Fatalf("assigned Agent claim rejected: %d", code)
	}
	for i, next := range []string{"in_progress", "in_review"} {
		if code, _, _ = request(t, "POST", workURL+"/transitions", f.agent,
			"agent-owner-"+next, "", map[string]any{"status": next, "expected_version": 3 + i}); code != 200 {
			t.Fatalf("agent transition %s: %d", next, code)
		}
	}
	agentID := principalFor(t, f, f.agent)
	// The existing PAT deliberately lacks work:review even after project Allow.
	if code, grant := setPermission(t, f, f.admin, agentID, "work:review", "allow", 0,
		"agent-owner-delegate-review"); code != 200 {
		t.Fatalf("Human administrator delegation rejected: %d %v", code, grant)
	}
	decision := reviewCompletion(run, 5, "owner_acceptance", true)

	if code, _, _ = request(t, "POST", completeURL(f, id), f.agent,
		"agent-owner-old-PAT-denied", "", decision); code != 403 {
		t.Fatalf("Agent's original limited PAT bypassed work:review: %d", code)
	}
	agentReviewer := createAccessToken(t, f, agentID, []string{"work:read", "work:review"})
	if code, _, _ = request(t, "POST", completeURL(f, id), f.admin,
		"agent-owner-human-not-assignee", "", decision); code != 422 {
		t.Fatalf("human creator replaced assigned Agent without explicit owner: %d", code)
	}
	// No second Reviewer:passed entry needed; this is explicitly non-independent.
	if code, completed, _ := request(t, "POST", completeURL(f, id), agentReviewer,
		"agent-owner-accepted", "", decision); code != 200 || completed["status"] != "done" {
		t.Fatalf("authorized assigned Agent acceptance rejected: %d %v", code, completed)
	}
	var mode string
	var independent, selfReview bool
	var author uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT acceptance_mode,independent,explicit_self_review,actor_id
       FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, id).
		Scan(&mode, &independent, &selfReview, &author); err != nil {
		t.Fatal(err)
	}
	if mode != "owner_acceptance" || independent || !selfReview || author != agentID {
		t.Fatalf("autonomous Agent acceptance provenance incorrect: %s, %v, %v, %v", mode, independent, selfReview, author)
	}
	if code, _, _ = request(t, "POST", completeURL(f, id), agentReviewer,
		"agent-owner-stale-version", "", decision); code != 409 {
		t.Fatalf("stale Agent accepted again: %d", code)
	}
}

func TestPhase11OwnerAcceptanceExplicitAndHighRisk(t *testing.T) {
	gate13(t)
	f := setup(t)
	ctx := context.Background()
	owner := principalFor(t, f, f.admin)
	authorizeOwnerPolicy(t, f, "gate21-owner-policy")
	run := reviewRun(t, f, "gate21-owner")
	id := reviewTarget(t, f, f.admin, "gate21-owner", run)
	url := completeURL(f, id)
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing confirmation", func(x map[string]any) { x["acknowledge_self_review"] = false }},
		{"short reason", func(x map[string]any) { x["reason"] = "fine" }},
		{"invalid reference", func(x map[string]any) { x["reference_value"] = uuid.NewString() }},
		{"wrong mode", func(x map[string]any) { x["acceptance_mode"] = "independent_review" }},
	} {
		d := reviewCompletion(run, 4, "owner_acceptance", true)
		tc.change(d)
		code, _, _ := request(t, "POST", url, f.admin, "gate21-owner-denied-"+tc.name, "", d)
		if code != 422 {
			t.Fatalf("%s bypassed owner check %d", tc.name, code)
		}
	}
	// Even a delegated Agent carrying a new work:review PAT cannot claim to
	// be the actual Human Accountable Owner without a server policy delegation.
	agentID := principalFor(t, f, f.agent)
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.project_permission_overrides
       (workspace_id,project_id,principal_id,permission,effect,version,changed_by)
       SELECT workspace_id,id,$2,'work:review','allow',1,$3 FROM qual.projects WHERE id=$1`,
		f.project, agentID, owner); err != nil {
		t.Fatal(err)
	}
	token := createAccessToken(t, f, agentID, []string{"work:read", "work:review"})
	if code, _, _ := request(t, "POST", url, token, "gate21-agent-not-owner", "", reviewCompletion(run, 4, "owner_acceptance", true)); code != 422 {
		t.Fatalf("Agent with explicit review Scope replaced Owner %d", code)
	}
	code, accepted, _ := request(t, "POST", url, f.admin, "gate21-owner-accepted", "", reviewCompletion(run, 4, "owner_acceptance", true))
	if code != 200 || accepted["status"] != "done" {
		t.Fatalf("explicit Owner acceptance %d %v", code, accepted)
	}
	var independent, self bool
	var mode string
	var actor uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT acceptance_mode,independent,explicit_self_review,actor_id
       FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, id).Scan(&mode, &independent, &self, &actor); err != nil {
		t.Fatal(err)
	}
	if mode != "owner_acceptance" || independent || !self || actor != owner {
		t.Fatalf("acceptance falsely independent mode=%s independent=%v self=%v", mode, independent, self)
	}
	// Explicit project switch cannot make urgent work into Owner acceptance.
	urgentStatus, urgent, _ := request(t, "POST", f.url(f.project), f.admin, "gate21-urgent-create", "", map[string]any{
		"kind": "task", "title": "High-risk urgent task requires independent review", "priority": "urgent",
	})
	if urgentStatus != 201 {
		t.Fatalf("urgent create %d %v", urgentStatus, urgent)
	}
	uid := urgent["id"].(string)
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		code, _, _ := request(t, "POST", f.url(f.project)+"/"+uid+"/transitions", f.admin,
			"gate21-urgent-"+next, "", map[string]any{"status": next, "expected_version": i + 1})
		if code != 200 {
			t.Fatal("urgent prepare", code)
		}
	}
	if code, _, _ := request(t, "POST", completeURL(f, uid), f.admin, "gate21-urgent-owner-denied", "", reviewCompletion(run, 4, "owner_acceptance", true)); code != 422 {
		t.Fatalf("high-risk owner bypass %d", code)
	}
	// Human Admin can delegate access:manage, but the Agent manager cannot
	// further relax the high-risk/Owner acceptance safety policy.
	manageAgent := createAccessToken(t, f, agentID, []string{"work:read", "access:manage"})
	if status, _ := setPermission(t, f, f.admin, agentID, "access:manage", "allow", 0, "gate21-delegate-agent-manager"); status != 200 {
		t.Fatalf("Human grant of manager to Agent fixture failed %d", status)
	}
	policyURL := testsURL(f) + "/review-policy"
	loosen := ownerPolicyInput(1)
	loosen["force_independent_for_high_risk"] = false
	if code, _, _ := request(t, "PUT", policyURL, manageAgent, "gate21-agent-loosen-denied", "", loosen); code != 403 {
		t.Fatalf("delegated Agent manager loosened Owner policy %d", code)
	}
	tighten := map[string]any{
		"default_mode": "independent_review", "owner_acceptance_allowed": false,
		"force_independent_for_high_risk": true, "expected_version": 1,
		"reason": "Delegated Agent manager is permitted to tighten Owner acceptance policy.",
	}
	if code, policy, _ := request(t, "PUT", policyURL, manageAgent, "gate21-agent-tighten-allowed", "", tighten); code != 200 ||
		policy["version"] != float64(2) || policy["default_mode"] != "independent_review" {
		t.Fatalf("authorized tighter project policy rejected %d %v", code, policy)
	}
}

func TestPhase11ConcurrentCompleteOnlyOneWinner(t *testing.T) {
	gate13(t)
	f := setup(t)
	ctx := context.Background()
	authorizeOwnerPolicy(t, f, "gate21-concurrent-policy")
	run := reviewRun(t, f, "gate21-concurrent")
	id := reviewTarget(t, f, f.admin, "gate21-concurrent", run)
	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := range statuses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			statuses[index], _, _ = request(t, "POST", completeURL(f, id), f.admin,
				"gate21-race-attempt-"+string(rune('a'+index)), "", reviewCompletion(run, 4, "owner_acceptance", true))
		}(i)
	}
	close(start)
	wg.Wait()
	winners, conflicts := 0, 0
	for _, code := range statuses {
		if code == 200 {
			winners++
		}
		if code == 409 {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent complete codes: %v", statuses)
	}
	var err error
	for _, table := range []string{"work_acceptance_decisions", "audit_records", "activity_records", "outbox_events"} {
		var count int
		if table == "work_acceptance_decisions" {
			err = f.pool.QueryRow(ctx,
				`SELECT count(*) FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, id).Scan(&count)
		} else {
			column, operation := "entity_id", "operation"
			if table == "outbox_events" {
				column, operation = "aggregate_id", "event_type"
			}
			err = f.pool.QueryRow(ctx, "SELECT count(*) FROM qual."+table+" WHERE "+column+"=$1 AND "+operation+"='work_item.completed'", id).Scan(&count)
		}
		if err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
