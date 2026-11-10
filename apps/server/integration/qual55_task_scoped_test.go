//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// QUAL-55: tests only use synthetic Principals in an isolated Goose18 database.
// The real Qual project remains Goose17/legacy. Public activation is deferred
// while QUAL-56~58 implement remaining scoped evidence/review paths.
func TestQual55TaskScopedWorkAuthorization(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var schema bool
	if err := f.pool.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !schema {
		t.Skip("Goose18 isolated task policy required")
	}
	a, b, adminID := principalFor(t, f, f.agent), principalFor(t, f, f.agent2), principalFor(t, f, f.admin)
	onlyClaim := createAccessToken(t, f, a, []string{"work:read", "work:claim"})
	onlyClaimB := createAccessToken(t, f, b, []string{"work:read", "work:claim"})
	x := create(t, f, f.agent, "qual55-before-scoped-create-x")
	y := create(t, f, f.agent, "qual55-before-scoped-create-y")
	xURL := f.url(f.project) + "/" + x["id"].(string)
	yURL := f.url(f.project) + "/" + y["id"].(string)
	// Legacy preserves broad project write even on an unclaimed task.
	if status, _, _ := request(t, "PATCH", yURL, f.agent, "qual55-legacy-update-y", "\"1\"", map[string]any{"title": "Legacy global write"}); status != 200 {
		t.Fatalf("legacy project write must continue: %d", status)
	}
	// Test-only policy seed. No API/Agent is allowed to silently enable a
	// partially rolled-out dynamic policy in the real self-hosted project.
	var workspace uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies
		(workspace_id,project_id,mode,version,changed_by)
		VALUES($1,$2,'task_scoped',1,$3)`, workspace, f.project, adminID); err != nil {
		t.Fatal(err)
	}
	// QUAL-63: a member holding all three explicit project creation scopes
	// may create a NEW item, but gains no write relation until Claim.
	status, createdScoped, _ := request(t, "POST", f.url(f.project), f.agent, "qual55-scoped-creator-with-project-write", "", map[string]any{
		"kind": "task", "title": "Authorized new scoped task", "priority": "medium",
	})
	if status != 201 || createdScoped["created_by"] != a.String() || createdScoped["assignee_id"] != nil {
		t.Fatalf("scoped project creator should create unclaimed item: %d %v", status, createdScoped)
	}
	if code, _, _ := request(t, "PATCH", f.url(f.project)+"/"+createdScoped["id"].(string), f.agent,
		"qual55-scoped-creator-no-claim-update", `"1"`, map[string]any{"title": "Creator still needs Claim"}); code != 403 {
		t.Fatalf("scoped creator edited its new unclaimed WorkItem: %d", code)
	}
	if code, _, _ := request(t, "POST", f.url(f.project), onlyClaim,
		"qual55-scoped-minimal-participant-create-denied", "", map[string]any{
			"kind": "task", "title": "Missing work write permission", "priority": "medium",
		}); code != 403 {
		t.Fatalf("minimal task participant created project WorkItem: %d", code)
	}
	if status, _, _ := request(t, "PATCH", yURL, f.agent, "qual55-scoped-cannot-write-unclaimed-y", "\"2\"", map[string]any{"title": "Try global bypass"}); status != 403 {
		t.Fatalf("broad work:write escaped task relationship: %d", status)
	}
	claim := func(url, token, key string, version int) map[string]any {
		t.Helper()
		status, data, _ := request(t, "POST", url+"/claim", token, key, "", map[string]any{"expected_version": version})
		if status != 200 {
			t.Fatalf("claim %s: %d %v", key, status, data)
		}
		return data
	}
	xState := claim(xURL, onlyClaim, "qual55-claim-x-agentA", 1)
	claim(yURL, onlyClaimB, "qual55-claim-y-agentB", 2)
	if status, _, _ := request(t, "PATCH", yURL, onlyClaim, "qual55-agentA-edit-y", "\"3\"", map[string]any{"title": "B's task"}); status != 403 {
		t.Fatalf("Agent A modified B task: %d", status)
	}
	if status, _, _ := request(t, "POST", yURL+"/comments", onlyClaim, "qual55-agentA-comment-y", "", map[string]any{"body": "Cross task", "expected_version": 3}); status != 403 {
		t.Fatalf("Agent A comment bypassed task isolation: %d", status)
	}
	if status, _, _ := request(t, "POST", yURL+"/transitions", onlyClaim, "qual55-agentA-transition-y", "", map[string]any{"status": "in_progress", "expected_version": 3}); status != 403 {
		t.Fatalf("Agent A transition bypassed task isolation: %d", status)
	}
	// The minimal participant credential contains no work:write, yet may
	// mutate its claimed task, and later record real role facts on that task.
	status, xState, _ = request(t, "PATCH", xURL, onlyClaim, "qual55-a-update-x", "\"2\"", map[string]any{"title": "Scoped edit by A"})
	if status != 200 || xState["version"] != float64(3) {
		t.Fatalf("scoped edit %d %v", status, xState)
	}
	if status, _, _ := request(t, "POST", xURL+"/comments", onlyClaim, "qual55-a-comment-x", "", map[string]any{"body": "A's task", "expected_version": 3}); status != 201 {
		t.Fatalf("scoped comment denied: %d", status)
	}
	if status, _, _ := request(t, "POST", xURL+"/transitions", onlyClaim, "qual55-a-transition-x-todo", "", map[string]any{"status": "todo", "expected_version": 4}); status != 200 {
		t.Fatalf("scoped backlog->todo denied: %d", status)
	}
	if status, _, _ := request(t, "POST", xURL+"/transitions", onlyClaim, "qual55-a-transition-x-progress", "", map[string]any{"status": "in_progress", "expected_version": 5}); status != 200 {
		t.Fatalf("scoped todo->in_progress denied: %d", status)
	}
	status, _, _ = request(t, "POST", xURL+"/role-entries", onlyClaim, "qual55-a-executor-role", "", map[string]any{
		"capacity": "executor", "outcome": "observed", "summary": "Real external test fixture observes a scoped executor record", "reference_kind": "none", "reference_value": "", "expected_version": 6,
	})
	if status != 201 {
		t.Fatalf("scoped executor record denied: %d", status)
	}
	// Reviewer isn't assigned until QUAL-57. Even a broadly privileged
	// project reviewer cannot bypass the missing task reviewer relationship.
	if status, _, _ := request(t, "POST", xURL+"/transitions", onlyClaim, "qual55-a-submit-review", "", map[string]any{"status": "in_review", "expected_version": 7}); status != 200 {
		t.Fatalf("scoped submit-review denied: %d", status)
	}
	status, _, _ = request(t, "POST", xURL+"/role-entries", f.admin, "qual55-unclaimed-reviewer", "", map[string]any{
		"capacity": "reviewer", "outcome": "passed", "summary": "Must not fabricate an independent reviewer in scoped mode", "reference_kind": "git_commit", "reference_value": "20eea48cbcfad6956a3428df7b29f6bed8e71d7d", "expected_version": 8,
	})
	if status != 403 {
		t.Fatalf("unclaimed reviewer granted: %d", status)
	}
	if status, _, _ := request(t, "PATCH", xURL, onlyClaim, "qual55-review-frozen-edit", "\"8\"", map[string]any{"title": "Mutate after submit"}); status != 403 {
		t.Fatalf("in_review content edit was permitted: %d", status)
	}
	if status, _, _ := request(t, "POST", xURL+"/comments", onlyClaim, "qual55-review-comment", "", map[string]any{"body": "Non material review coordination", "expected_version": 8}); status != 201 {
		t.Fatalf("in_review comment should remain possible: %d", status)
	}
	if status, _, _ := request(t, "POST", xURL+"/transitions", onlyClaim, "qual55-unassigned-return", "", map[string]any{"status": "in_progress", "expected_version": 9}); status != 403 {
		t.Fatalf("unassigned reviewer returned work: %d", status)
	}
	// On another claim-backed task, an explicit project Deny wins even if
	// the actor is assignee and has the old broad write permission.
	if status, _ := setPermission(t, f, f.admin, b, "work:write", "deny", 0, "qual55-deny-b-write"); status != 200 {
		t.Fatalf("failed to set work:write deny: %d", status)
	}
	if status, _, _ := request(t, "PATCH", yURL, onlyClaimB, "qual55-b-denied-edit", "\"3\"", map[string]any{"title": "Denied after claim"}); status != 403 {
		t.Fatalf("project deny did not override executor: %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, a, "work:claim", "deny", 0, "qual55-deny-a-claim"); status != 200 {
		t.Fatalf("failed to set work:claim deny: %d", status)
	}
	if status, _, _ := request(t, "POST", xURL+"/comments", onlyClaim, "qual55-a-denied-by-claim", "", map[string]any{"body": "Should fail", "expected_version": 9}); status != 403 {
		t.Fatalf("claim deny did not revoke participation: %d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject)+"/"+x["id"].(string), onlyClaim, "", "", nil); status != 404 {
		t.Fatalf("cross project visibility leaked: %d", status)
	}
	// No failed action may have produced an Activity/Audit/Outbox event.
	var version int
	if err := f.pool.QueryRow(ctx, "SELECT version FROM qual.work_items WHERE id=$1 AND project_id=$2", x["id"], f.project).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 9 {
		t.Fatalf("denied writes changed WorkItem version=%d", version)
	}
	var audits, outbox int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2`, f.project, x["id"]).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2`, f.project, x["id"]).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if audits != outbox {
		t.Fatalf("audit/outbox mismatch %d/%d", audits, outbox)
	}
	t.Logf("task-scoped isolated X v%d, matched audit/outbox=%d; Agent A/B isolated, denial and review freeze passed", version, audits)
}

func TestQual55TaskModeDefaultAndRevocation(t *testing.T) {
	f := setup(t)
	var installed bool
	if err := f.pool.QueryRow(context.Background(), "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Goose18 policy required")
	}
	actor := principalFor(t, f, f.agent)
	minimal := createAccessToken(t, f, actor, []string{"work:read", "work:claim"})
	x := create(t, f, f.agent, "qual55-mode-test-x")
	xURL := f.url(f.project) + "/" + x["id"].(string)
	// In default legacy, lacking work:write cannot update an unclaimed task.
	if status, _, _ := request(t, "PATCH", xURL, minimal, "qual55-legacy-minimal-denied", "\"1\"", map[string]any{"title": "Denied"}); status != 403 {
		t.Fatalf("default unexpectedly escalated %d", status)
	}
	var workspace uuid.UUID
	if err := f.pool.QueryRow(context.Background(), "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.project_task_authorization_policies (workspace_id,project_id,mode,version,changed_by) VALUES ($1,$2,'task_scoped',1,$3)`, workspace, f.project, principalFor(t, f, f.admin)); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request(t, "POST", xURL+"/claim", minimal, "qual55-minimal-claim", "", map[string]any{"expected_version": 1}); status != 200 {
		t.Fatalf("minimal claim %d", status)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.credentials SET revoked_at=now() WHERE principal_id=$1 AND scopes=ARRAY['work:read','work:claim']::text[]`, actor); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request(t, "PATCH", xURL, minimal, "qual55-revoked-minimal", "\"2\"", map[string]any{"title": "Revoked"}); status != 401 {
		t.Fatalf("revoked token unexpectedly worked %d", status)
	}
	// Legacy switch for fixture demonstrates no magical task-derived grant.
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.project_task_authorization_policies SET mode='legacy',version=2 WHERE project_id=$1`, f.project); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request(t, "PATCH", xURL, f.agent, "qual55-fallback-old-write", "\"2\"", map[string]any{"title": "Legacy returns old project write"}); status != 200 {
		t.Fatalf("legacy explicit fallback did not restore old write %d", status)
	}
}

// Competing Agents use one saved WorkItem version: one Claim commits, one
// receives a CAS or already-claimed 409, without duplicate audit/outbox.
func TestQual55ScopedClaimRaceAndIdempotency(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var installed bool
	if err := f.pool.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Goose18 required")
	}
	item := create(t, f, f.agent, "qual55-race-source")
	var workspace uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies(workspace_id,project_id,mode,version,changed_by)
	 VALUES($1,$2,'task_scoped',1,$3)`, workspace, f.project, principalFor(t, f, f.admin)); err != nil {
		t.Fatal(err)
	}
	agents := []string{f.agent, f.agent2}
	results := make([]int, 2)
	var wg sync.WaitGroup
	for i := range agents {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], _, _ = request(t, "POST", f.url(f.project)+"/"+item["id"].(string)+"/claim",
				agents[index], fmt.Sprintf("qual55-race-claim-%d", index), "", map[string]any{"expected_version": 1})
		}(i)
	}
	wg.Wait()
	if !((results[0] == 200 && results[1] == 409) || (results[1] == 200 && results[0] == 409)) {
		t.Fatalf("expected 200+409, observed %v", results)
	}
	var workVersion int
	if err := f.pool.QueryRow(ctx, "SELECT version FROM qual.work_items WHERE id=$1", item["id"]).Scan(&workVersion); err != nil {
		t.Fatal(err)
	}
	if workVersion != 2 {
		t.Fatalf("claim race modified version more than once: %d", workVersion)
	}
	var auditCount, outboxCount int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2 AND operation='work_item.claim'`, f.project, item["id"]).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2 AND event_type='work_item.claim'`, f.project, item["id"]).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || outboxCount != 1 {
		t.Fatalf("claim audit/outbox=%d/%d", auditCount, outboxCount)
	}
}

// An assigned Owner has scoped completion authority without a permanent
// work:review token; the existing Done Gate still verifies same-work evidence.
func TestQual55ScopedOwnerAcceptanceUsesExistingDoneGate(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var installed bool
	if err := f.pool.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Goose18 required")
	}
	principal := principalFor(t, f, f.agent)
	minimal := createAccessToken(t, f, principal, []string{"work:read", "work:claim"})
	item := create(t, f, f.agent, "qual55-owner-scoped-source")
	itemURL := f.url(f.project) + "/" + item["id"].(string)

	// Goose20 uses explicit WorkItem-bound immutable TestRun evidence,
	// whereas Goose18/19 continue using the legacy coverage-based fixture.
	runID := qual75ReviewRunForWork(t, f, "qual55-owner", item["id"].(string))
	policy := "/api/v1/projects/" + f.project.String() + "/review-policy"
	status, changed, _ := request(t, "PUT", f.server.URL+policy, f.admin, "qual55-owner-allow-policy", "", map[string]any{
		"default_mode": "owner_acceptance", "owner_acceptance_allowed": true,
		"force_independent_for_high_risk": true, "expected_version": 0,
		"reason": "Isolated test fixture approves Owner acceptance with actual API evidence",
	})
	if status != 200 || changed["default_mode"] != "owner_acceptance" {
		t.Fatalf("owner policy set failed %d %v", status, changed)
	}
	var workspace uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies
	 (workspace_id,project_id,mode,version,changed_by)
	 VALUES($1,$2,'task_scoped',1,$3)`, workspace, f.project, principalFor(t, f, f.admin)); err != nil {
		t.Fatal(err)
	}
	status, _, _ = request(t, "POST", itemURL+"/claim", minimal,
		"qual55-owner-scoped-claim", "", map[string]any{"expected_version": 1})
	if status != 200 {
		t.Fatalf("minimal scoped claim denied %d", status)
	}
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		status, _, _ = request(t, "POST", itemURL+"/transitions", minimal,
			fmt.Sprintf("qual55-owner-scoped-step-%d", i), "", map[string]any{
				"status": next, "expected_version": i + 2,
			})
		if status != 200 {
			t.Fatalf("minimal scoped transition %s denied %d", next, status)
		}
	}
	status, done, _ := request(t, "POST", itemURL+"/complete", minimal,
		"qual55-owner-scoped-done", "", reviewCompletion(runID, 5, "owner_acceptance", true))
	if status != 200 || done["status"] != "done" {
		t.Fatalf("minimal scoped Owner could not complete %d %v", status, done)
	}
	status, receipt, _ := request(t, "GET", itemURL+"/acceptances", minimal, "", "", nil)
	if status != 200 || receipt["acceptance_mode"] != "owner_acceptance" {
		t.Fatalf("Owner acceptance receipt missing %d %v", status, receipt)
	}
	decision := receipt["decision"].(map[string]any)
	if decision["independent"] != false || decision["explicit_self_review"] != true {
		t.Fatalf("Owner self-review misrepresented as independent %v", decision)
	}
	status, _, _ = request(t, "POST", itemURL+"/comments", minimal,
		"qual55-owner-done-comment-deny", "", map[string]any{
			"body": "No task write after Done", "expected_version": 6,
		})
	if status != 403 {
		t.Fatalf("task write survived Done %d", status)
	}
}
