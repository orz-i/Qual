//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func qual57Schema(t *testing.T, f *fixture) {
	t.Helper()
	var present bool
	if err := f.pool.QueryRow(context.Background(),
		"SELECT to_regclass('qual.work_review_assignments') IS NOT NULL").Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skip("Goose19 current review assignment relation required")
	}
}
func qual57SeedMode(t *testing.T, f *fixture) {
	t.Helper()
	var ws uuid.UUID
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies
		(workspace_id,project_id,mode,version,changed_by)
		VALUES($1,$2,'task_scoped',1,$3)`, ws, f.project, principalFor(t, f, f.admin)); err != nil {
		t.Fatal(err)
	}
}
func qual57ReviewAction(t *testing.T, f *fixture, token, id, action, key string, version int) (int, map[string]any) {
	t.Helper()
	code, data, _ := request(t, "POST", f.url(f.project)+"/"+id+"/"+action, token, key, "",
		map[string]any{"expected_version": version})
	return code, data
}
func qual57Prepare(t *testing.T, f *fixture, prefix string) (string, string, string, string, uuid.UUID, uuid.UUID) {
	t.Helper()
	qual57Schema(t, f)
	work := create(t, f, f.agent, prefix+"-create")
	id := work["id"].(string)
	run := qual75ReviewRunForWork(t, f, prefix+"-run", id)
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	ta := createAccessToken(t, f, a, []string{"work:read", "work:claim"})
	tb := createAccessToken(t, f, b, []string{"work:read", "work:claim"})
	qual57SeedMode(t, f)
	qual56Claim(t, f, ta, id, prefix+"-claim", 1)
	for i, status := range []string{"todo", "in_progress", "in_review"} {
		qual56Status(t, f, ta, id, status, fmt.Sprintf("%s-to-%s", prefix, status), 2+i)
	}
	return id, run, ta, tb, a, b
}
func qual57Review(t *testing.T, f *fixture, token, id, run, key string, version int, outcome string) (int, map[string]any) {
	t.Helper()
	returnCode, item, _ := request(t, "POST", roleEndpoint(f, id), token, key, "",
		roleInput("reviewer", outcome, "test_run", run, "Real isolated reviewer evaluated bound TestRun", version))
	return returnCode, item
}
func TestQual57IndependentReviewerClaimToDone(t *testing.T) {
	f := setup(t)
	id, run, ta, tb, a, b := qual57Prepare(t, f, "qual57-independent")
	path := f.url(f.project) + "/" + id
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-b-before-claim", 5, "passed"); code != 403 {
		t.Fatalf("unassigned Reviewer wrote independent approval %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, ta, id, "review-claim", "qual57-a-self-claim", 5); code != 403 {
		t.Fatalf("executor falsely claimed independent review %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-stale", 4); code != 409 {
		t.Fatalf("stale review claim %d", code)
	}
	code, claimed := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-claims", 5)
	if code != 200 || claimed["version"] != float64(6) || claimed["status"] != "in_review" {
		t.Fatalf("claim %d %v", code, claimed)
	}
	code, replay := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-claims", 5)
	if code != 200 || replay["version"] != float64(6) {
		t.Fatalf("claim replay %d %v", code, replay)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-claims", 4); code != 409 {
		t.Fatalf("idempotency changed request %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, f.admin, id, "review-claim", "qual57-c-second-claim", 6); code != 409 {
		t.Fatalf("second review claim %d", code)
	}
	if code, _, _ := request(t, "PATCH", path, tb, "qual57-reviewer-cannot-edit", "\"6\"", map[string]any{"title": "Reviewer not executor"}); code != 403 {
		t.Fatalf("reviewer gained executor edit %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/complete", ta, "qual57-executor-cannot-complete", "", reviewCompletion(run, 6, "independent_review", false)); code != 403 {
		t.Fatalf("executor tried own independent completion %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/complete", tb, "qual57-no-passed-completion", "", reviewCompletion(run, 6, "independent_review", false)); code != 422 {
		t.Fatalf("done without actual review %d", code)
	}
	code, record := qual57Review(t, f, tb, id, run, "qual57-b-real-passed", 6, "passed")
	if code != 201 || record["self_review"] != false || record["actor_id"] != b.String() {
		t.Fatalf("real review role %d %v", code, record)
	}
	if code, _, _ := request(t, "POST", path+"/complete", ta, "qual57-a-reuse-b-review", "", reviewCompletion(run, 7, "independent_review", false)); code != 403 {
		t.Fatalf("executor used B's review %d", code)
	}
	code, done, _ := request(t, "POST", path+"/complete", tb, "qual57-b-independent-done", "", reviewCompletion(run, 7, "independent_review", false))
	if code != 200 || done["status"] != "done" || done["version"] != float64(8) {
		t.Fatalf("independent done %d %v", code, done)
	}
	var assigned int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.work_review_assignments WHERE project_id=$1 AND work_item_id=$2", f.project, id).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 0 {
		t.Fatal("done retained active Reviewer relation")
	}
	code, receipt, _ := request(t, "GET", path+"/acceptances", tb, "", "", nil)
	if code != 200 || receipt["decision"] == nil {
		t.Fatalf("real acceptance receipt %d %v", code, receipt)
	}
	decision := receipt["decision"].(map[string]any)
	if decision["independent"] != true || decision["explicit_self_review"] != false ||
		decision["actor_id"] != b.String() || decision["reviewer_entry_id"] != record["id"] {
		t.Fatalf("independent acceptance falsified %v", decision)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-release", "qual57-done-release", 8); code != 403 && code != 422 {
		t.Fatalf("done reviewer still privileged %d", code)
	}
	if code, _, _ := request(t, "PATCH", path, ta, "qual57-done-executor-denied", "\"8\"", map[string]any{"title": "After Done"}); code != 403 {
		t.Fatalf("done executor still privileged %d", code)
	}
	var audit, activity, outbox int
	for _, r := range []struct {
		table, col, kind string
		out              *int
	}{
		{"audit_records", "entity_id", "operation", &audit},
		{"activity_records", "entity_id", "operation", &activity},
		{"outbox_events", "aggregate_id", "event_type", &outbox},
	} {
		err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM qual."+r.table+
			" WHERE project_id=$1 AND "+r.col+"=$2 AND "+r.kind+"='work_item.review_claim'", f.project, id).Scan(r.out)
		if err != nil {
			t.Fatal(err)
		}
	}
	if audit != 1 || activity != 1 || outbox != 1 {
		t.Fatalf("claim audit/activity/outbox %d/%d/%d", audit, activity, outbox)
	}
	_ = a
}
func TestQual57ReviewReleaseReclaimStalePassedAndReturn(t *testing.T) {
	f := setup(t)
	id, run, ta, tb, _, _ := qual57Prepare(t, f, "qual57-release")
	path := f.url(f.project) + "/" + id
	code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-initial-b-claim", 5)
	if code != 200 {
		t.Fatalf("review claim %d", code)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-passed-before-release", 6, "passed"); code != 201 {
		t.Fatalf("real reviewer record %d", code)
	}
	code, released := qual57ReviewAction(t, f, tb, id, "review-release", "qual57-b-release", 7)
	if code != 200 || released["version"] != float64(8) {
		t.Fatalf("release %d %v", code, released)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-released-reviewer-record", 8, "passed"); code != 403 {
		t.Fatalf("released B still reviews %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/complete", tb, "qual57-released-complete", "", reviewCompletion(run, 8, "independent_review", false)); code != 403 {
		t.Fatalf("released B completed %d", code)
	}
	code, claimed := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-reclaim", 8)
	if code != 200 || claimed["version"] != float64(9) {
		t.Fatalf("reclaim %d %v", code, claimed)
	}
	if code, _, _ := request(t, "POST", path+"/complete", tb, "qual57-old-passed-after-reclaim", "", reviewCompletion(run, 9, "independent_review", false)); code != 422 {
		t.Fatalf("prior claim session's passed reused %d", code)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-new-needs-work", 9, "needs_work"); code != 201 {
		t.Fatalf("needs_work %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/complete", tb, "qual57-needs-work-denies-complete", "", reviewCompletion(run, 10, "independent_review", false)); code != 422 {
		t.Fatalf("latest needs_work bypassed %d", code)
	}
	qual56Status(t, f, tb, id, "in_progress", "qual57-b-return-to-execution", 10)
	var count int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.work_review_assignments WHERE project_id=$1 AND work_item_id=$2", f.project, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("return_to_work left %d active reviewers", count)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-released-after-return", 11, "passed"); code != 403 {
		t.Fatalf("old Reviewer still reviews after return %d", code)
	}
	code, updated, _ := request(t, "PATCH", path, ta, "qual57-executor-fixes-after-return", "\"11\"", map[string]any{"title": "Updated after needs_work"})
	if code != 200 || updated["version"] != float64(12) {
		t.Fatalf("executor not restored after review rejection %d %v", code, updated)
	}
	qual56Status(t, f, ta, id, "in_review", "qual57-resubmit-after-fix", 12)
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-b-third-claim", 13); code != 200 {
		t.Fatalf("review claimant after resubmit %d", code)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-third-review-passed", 14, "passed"); code != 201 {
		t.Fatalf("new passed %d", code)
	}
	if code, done, _ := request(t, "POST", path+"/complete", tb, "qual57-fresh-valid-done", "", reviewCompletion(run, 15, "independent_review", false)); code != 200 || done["status"] != "done" {
		t.Fatalf("fresh actual independent Done Gate refused %d %v", code, done)
	}
}
func TestQual57FormerExecutorCannotClaimIndependentReview(t *testing.T) {
	f := setup(t)
	qual57Schema(t, f)
	b := principalFor(t, f, f.agent2)
	ta := createAccessToken(t, f, principalFor(t, f, f.agent), []string{"work:read", "work:claim"})
	tb := createAccessToken(t, f, b, []string{"work:read", "work:claim"})
	item := create(t, f, f.admin, "qual57-original-owner-admin")
	id := item["id"].(string)
	qual57SeedMode(t, f)
	qual56Claim(t, f, tb, id, "qual57-b-initial-executor", 1)
	// B is executor historically, though task is now A's.
	if status, _ := qual56Write(t, f, tb, id, "reassign", "qual57-b-to-a", 2, principalFor(t, f, f.agent)); status != 200 {
		t.Fatalf("handoff failed %d", status)
	}
	qual56Status(t, f, ta, id, "todo", "qual57-former-todo", 3)
	qual56Status(t, f, ta, id, "in_progress", "qual57-former-progress", 4)
	qual56Status(t, f, ta, id, "in_review", "qual57-former-review", 5)
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-former-b-claim-denied", 6); code != 403 {
		t.Fatalf("historical executor B became independent Reviewer after transfer %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, ta, id, "review-claim", "qual57-current-a-claim-denied", 6); code != 403 {
		t.Fatalf("current assignee claimed review %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, f.admin, id, "review-claim", "qual57-accountable-owner-denied", 6); code != 403 {
		t.Fatalf("created/owner Human claimed independent review %d", code)
	}
	var records int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.work_review_assignments WHERE project_id=$1 AND work_item_id=$2", f.project, id).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 0 {
		t.Fatal("denied reviewer claims created relationship")
	}
}
func TestQual57ReviewClaimCASConcurrentAndHTTPGuards(t *testing.T) {
	f := setup(t)
	id, _, ta, tb, _, _ := qual57Prepare(t, f, "qual57-cas")
	path := f.url(f.project) + "/" + id
	if code, _, _ := request(t, "POST", path+"/review-claim", tb, "", "", map[string]any{"expected_version": 5}); code != 428 {
		t.Fatalf("missing idempotency %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/review-claim", tb, "qual57-no-version", "", map[string]any{}); code != 428 {
		t.Fatalf("missing version %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/review-claim", tb, "qual57-self-claimed-field", "", map[string]any{"expected_version": 5, "reviewer_id": uuid.NewString()}); code != 400 {
		t.Fatalf("untrusted reviewer_id field %d", code)
	}
	if code, _, _ := request(t, "POST", path+"/review-claim", "", "qual57-no-auth", "", map[string]any{"expected_version": 5}); code != 401 {
		t.Fatalf("anonymous review %d", code)
	}
	if code, _, _ := request(t, "POST", f.url(f.otherProject)+"/"+id+"/review-claim", tb, "qual57-cross-project", "", map[string]any{"expected_version": 5}); code != 404 {
		t.Fatalf("cross-project review disclosed %d", code)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0], _ = qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-concurrent-b", 5)
	}()
	go func() {
		defer wg.Done()
		codes[1], _ = qual57ReviewAction(t, f, f.admin, id, "review-claim", "qual57-concurrent-admin", 5)
	}()
	wg.Wait()
	if !((codes[0] == 200 && codes[1] == 409) || (codes[0] == 409 && codes[1] == 200)) {
		t.Fatalf("concurrent claims %v", codes)
	}
	var reviewer uuid.UUID
	var claimedVersion, workVersion int64
	if err := f.pool.QueryRow(context.Background(),
		"SELECT reviewer_id,claimed_work_version FROM qual.work_review_assignments WHERE project_id=$1 AND work_item_id=$2", f.project, id).Scan(&reviewer, &claimedVersion); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(),
		"SELECT version FROM qual.work_items WHERE project_id=$1 AND id=$2", f.project, id).Scan(&workVersion); err != nil {
		t.Fatal(err)
	}
	if claimedVersion != 6 || workVersion != 6 {
		t.Fatalf("CAS race status claim=%d work=%d", claimedVersion, workVersion)
	}
	var audit, activity, outbox int
	for _, r := range []struct {
		table, col, kind string
		out              *int
	}{
		{"audit_records", "entity_id", "operation", &audit},
		{"activity_records", "entity_id", "operation", &activity},
		{"outbox_events", "aggregate_id", "event_type", &outbox},
	} {
		err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM qual."+r.table+
			" WHERE project_id=$1 AND "+r.col+"=$2 AND "+r.kind+"='work_item.review_claim'", f.project, id).Scan(r.out)
		if err != nil {
			t.Fatal(err)
		}
	}
	if audit != 1 || activity != 1 || outbox != 1 {
		t.Fatalf("claim race audit/activity/outbox %d/%d/%d", audit, activity, outbox)
	}
	if reviewer == uuid.Nil {
		t.Fatal("successful claimant not persisted")
	}
	_ = ta
}
func TestQual57LegacyOwnerReviewNoRegression(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "qual57-legacy-unchanged")
	id := work["id"].(string)
	if code, _ := qual57ReviewAction(t, f, f.admin, id, "review-claim", "qual57-legacy-claim-denied", 1); code != 403 {
		t.Fatalf("new Reviewer claim changed legacy %d", code)
	}
	// Legacy project-wide Reviewer role is still explicitly controlled by
	// its credential + project scope, not by the new relationship.
	if code, _, _ := request(t, "POST", roleEndpoint(f, id), f.admin, "qual57-legacy-review-role", "",
		roleInput("reviewer", "observed", "none", "", "Legacy review fact remains project permission governed", 1)); code != 201 {
		t.Fatalf("legacy role record regressed %d", code)
	}
}

func TestQual57OwnerCanReturnButHighRiskRequiresIndependent(t *testing.T) {
	f := setup(t)
	qual57Schema(t, f)
	base := "/api/v1/projects/" + f.project.String() + "/review-policy"
	status, _, _ := request(t, "PUT", f.server.URL+base, f.admin, "qual57-project-owner-policy", "", map[string]any{
		"default_mode": "owner_acceptance", "owner_acceptance_allowed": true,
		"force_independent_for_high_risk": true, "expected_version": 0,
		"reason": "Human workspace administrator enables explicit routine Owner signoff with high risk independent bound",
	})
	if status != 200 {
		t.Fatalf("project policy set %d", status)
	}
	routine := create(t, f, f.agent, "qual57-owner-routine")
	status, urgent, _ := request(t, "POST", f.url(f.project), f.agent, "qual57-urgent-create", "", map[string]any{
		"kind": "task", "priority": "urgent", "title": "Explicit urgent independent-review gate",
		"acceptance_criteria": []string{"Independent reviewer must approve"},
	})
	if status != 201 {
		t.Fatalf("urgent fixture create %d %v", status, urgent)
	}
	qual57SeedMode(t, f)
	owner := createAccessToken(t, f, principalFor(t, f, f.agent), []string{"work:read", "work:claim"})
	reviewer := createAccessToken(t, f, principalFor(t, f, f.agent2), []string{"work:read", "work:claim"})
	for _, v := range []struct {
		id       string
		highRisk bool
	}{
		{routine["id"].(string), false}, {urgent["id"].(string), true},
	} {
		qual56Claim(t, f, owner, v.id, "qual57-owner-claim-"+v.id, 1)
		qual56Status(t, f, owner, v.id, "todo", "qual57-owner-todo-"+v.id, 2)
		qual56Status(t, f, owner, v.id, "in_progress", "qual57-owner-progress-"+v.id, 3)
		qual56Status(t, f, owner, v.id, "in_review", "qual57-owner-review-"+v.id, 4)
		if !v.highRisk {
			if code, _ := qual57ReviewAction(t, f, reviewer, v.id, "review-claim", "qual57-owner-no-independent-claim", 5); code != 403 {
				t.Fatalf("Owner-mode task permitted independent review claim %d", code)
			}
			qual56Status(t, f, owner, v.id, "in_progress", "qual57-owner-return", 5)
			continue
		}
		if code, _, _ := request(t, "POST", f.url(f.project)+"/"+v.id+"/transitions", owner,
			"qual57-urgent-owner-reject", "", map[string]any{"status": "in_progress", "expected_version": 5}); code != 403 {
			t.Fatalf("urgent owner bypassed independent review to return %d", code)
		}
		if code, claimed := qual57ReviewAction(t, f, reviewer, v.id, "review-claim", "qual57-urgent-independent-claim", 5); code != 200 || claimed["version"] != float64(6) {
			t.Fatalf("urgent independently reviewable claim %d %v", code, claimed)
		}
	}
}
func TestQual57PriorReassignmentAndTesterFactsExcludeReviewer(t *testing.T) {
	f := setup(t)
	qual57Schema(t, f)
	agentA := principalFor(t, f, f.agent)
	agentB := principalFor(t, f, f.agent2)
	claimA := createAccessToken(t, f, agentA, []string{"work:read", "work:claim"})
	claimB := createAccessToken(t, f, agentB, []string{"work:read", "work:claim"})
	// Create evidence before activating scoped mode; QUAL-58 separately
	// covers task-related TestAttempt creation under dynamic permissions.
	caseID, _ := mustCase(t, f, "qual57-tester-history-case", "Tested before Reviewer claim")
	vID, _ := publishCase(t, f, caseID, "qual57-tester-history-publish", 1)
	run := mustRun(t, f, []string{vID}, "qual57-tester-history-run")
	tested := create(t, f, f.agent, "qual57-tested-work")
	testedID := tested["id"].(string)
	if code, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		"qual57-tested-work-coverage", "", map[string]any{"work_item_id": testedID}); code != 201 {
		t.Fatalf("test coverage %d", code)
	}
	runItem := run["items"].([]any)[0].(map[string]any)["id"].(string)
	mustAttempt(t, f, runItem, f.agent2, "qual57-b-test-attempt-before-review", "passed")
	work := create(t, f, f.agent, "qual57-reassigned-work")
	id := work["id"].(string)
	qual57SeedMode(t, f)
	qual56Claim(t, f, claimA, id, "qual57-reassigned-claim-a", 1)
	if code, _ := qual56Write(t, f, claimA, id, "reassign", "qual57-a-to-b-historical", 2, agentB); code != 200 {
		t.Fatalf("reassign to B %d", code)
	}
	if code, _ := qual56Write(t, f, claimB, id, "reassign", "qual57-b-back-a", 3, agentA); code != 200 {
		t.Fatalf("reassign back to A %d", code)
	}
	qual56Status(t, f, claimA, id, "todo", "qual57-reassign-todo", 4)
	qual56Status(t, f, claimA, id, "in_progress", "qual57-reassign-progress", 5)
	qual56Status(t, f, claimA, id, "in_review", "qual57-reassign-review", 6)
	if code, _ := qual57ReviewAction(t, f, claimB, id, "review-claim", "qual57-prior-assignee-history-block", 7); code != 403 {
		t.Fatalf("reassigned prior executor became independent %d", code)
	}
	qual56Claim(t, f, claimA, testedID, "qual57-tested-claim-a", 1)
	qual56Status(t, f, claimA, testedID, "todo", "qual57-tested-todo", 2)
	qual56Status(t, f, claimA, testedID, "in_progress", "qual57-tested-progress", 3)
	qual56Status(t, f, claimA, testedID, "in_review", "qual57-tested-review", 4)
	if code, _ := qual57ReviewAction(t, f, claimB, testedID, "review-claim", "qual57-test-reporter-independent-block", 5); code != 403 {
		t.Fatalf("actual prior TestAttempt reporter became independent reviewer %d", code)
	}
}
func TestQual57ReviewerProjectDenyAndRevokedCredential(t *testing.T) {
	f := setup(t)
	id, run, _, tb, _, b := qual57Prepare(t, f, "qual57-credential")
	if status, _ := setPermission(t, f, f.admin, b, "work:review", "deny", 0, "qual57-review-project-deny"); status != 200 {
		t.Fatal(status)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-review-denied", 5); code != 403 {
		t.Fatalf("work:review project deny bypassed %d", code)
	}
	if status, _ := setPermission(t, f, f.admin, b, "work:review", "inherit", 1, "qual57-review-project-inherit"); status != 200 {
		t.Fatal(status)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-review-allowed", 5); code != 200 {
		t.Fatalf("cannot claim after restoring review permission %d", code)
	}
	if status, _ := setPermission(t, f, f.admin, b, "work:review", "deny", 2, "qual57-review-deny-after-claim"); status != 200 {
		t.Fatal(status)
	}
	if code, _ := qual57Review(t, f, tb, id, run, "qual57-current-reviewer-denied", 6, "passed"); code != 403 {
		t.Fatalf("current reviewer bypassed project deny %d", code)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-release", "qual57-review-release-denied", 6); code != 403 {
		t.Fatalf("release bypassed review deny %d", code)
	}
	if status, _ := setPermission(t, f, f.admin, b, "work:review", "inherit", 3, "qual57-review-reenabled"); status != 200 {
		t.Fatal(status)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.credentials SET revoked_at=now() WHERE principal_id=$1 AND scopes=ARRAY['work:read','work:claim']::text[]`, b); err != nil {
		t.Fatal(err)
	}
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-release", "qual57-revoked-reviewer-release", 6); code != 401 {
		t.Fatalf("revoked reviewer credential still authorized %d", code)
	}
	var version int
	if err := f.pool.QueryRow(context.Background(), "SELECT version FROM qual.work_items WHERE project_id=$1 AND id=$2", f.project, id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("revoked or denied work mutated version to %d", version)
	}
}

func TestQual57LateTesterFactInvalidatesEarlierIndependentReview(t *testing.T) {
	f := setup(t)
	id, run, _, tb, _, b := qual57Prepare(t, f, "qual57-late-tester")
	if code, _ := qual57ReviewAction(t, f, tb, id, "review-claim", "qual57-late-test-review-claim", 5); code != 200 {
		t.Fatalf("review claim %d", code)
	}
	code, role := qual57Review(t, f, tb, id, run, "qual57-late-test-review-passed", 6, "passed")
	if code != 201 || role["self_review"] != false {
		t.Fatalf("independent review fact setup %d %v", code, role)
	}
	// Until QUAL-58 fully scopes test writers, the current project-level Test
	// endpoints can still append attempts. Such a newly discovered self-test
	// MUST make the earlier independent reviewer conclusion unusable at Done.
	// Goose19's legacy unbound Run is a project-level resource, requiring
	// explicit management permission in task_scoped mode. Goose20 binds it to
	// the WorkItem and the current Executor can read it directly.
	reader := f.agent
	var currentGoose int
	if err := f.pool.QueryRow(context.Background(), "SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version WHERE is_applied").Scan(&currentGoose); err != nil {
		t.Fatal(err)
	}
	if currentGoose < 20 {
		reader = f.admin
	}
	status, runData := testRequest(t, "GET", testsURL(f)+"/test-runs/"+run, reader, "", "", nil)
	if status != 200 {
		t.Fatalf("read linked run %d", status)
	}
	runItemID := runData["items"].([]any)[0].(map[string]any)["id"].(string)
	status, attempt := testRequest(t, "POST", attemptURL(f, runItemID), f.agent2,
		"qual57-late-reviewer-test-attempt", "",
		map[string]any{"status": "passed", "note": "Observed by isolated fixture", "source_kind": "external"})
	// Before Goose20, old unbound TestRuns are project-level resources:
	// a Reviewer holding only task participation must not write one.
	if currentGoose < 20 {
		if status != 403 {
			t.Fatalf("scoped unbound Run allowed an Attempt: %d", status)
		}
		return
	}
	// On Goose20 a Reviewer may append a task-bound TestAttempt, but this
	// disqualifies their earlier Passed as independent completion evidence.
	if status != 201 || attempt["reported_by"] != b.String() {
		t.Fatalf("later actual Reviewer-as-Tester event not recorded: %d %v", status, attempt)
	}
	status, _, _ = request(t, "POST", completeURL(f, id), tb,
		"qual57-late-tester-must-not-complete", "",
		reviewCompletion(run, 7, "independent_review", false))
	if status != 422 {
		t.Fatalf("reviewer self-tested after Passed but still completed %d", status)
	}
	var state string
	if err := f.pool.QueryRow(context.Background(),
		"SELECT status FROM qual.work_items WHERE project_id=$1 AND id=$2",
		f.project, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "in_review" {
		t.Fatalf("late Tester conflict modified WorkItem to %s", state)
	}
}
