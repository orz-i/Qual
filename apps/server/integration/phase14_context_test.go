//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
)

// Qual27 tests must never run against the default QUAL database or a reused
// integration database. The runner creates a uniquely named disposable DB.
func gateContext27(t *testing.T) {
	t.Helper()
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual27_ctx_") {
		t.Skip("QUAL-27 requires disposable qual27_ctx_* PostgreSQL database")
	}
}

func contextURL(f *fixture, workID string) string {
	return f.url(f.project) + "/" + workID + "/context"
}

func contextRequest(t *testing.T, url, token string) (int, map[string]any) {
	t.Helper()
	code, data, _ := request(t, http.MethodGet, url, token, "", "", nil)
	return code, data
}

func sectionItems(t *testing.T, path, token string, want int) (map[string]any, []any) {
	t.Helper()
	code, data := contextRequest(t, path, token)
	if code != http.StatusOK {
		t.Fatalf("context status %d: %v", code, data)
	}
	records, ok := data["items"].([]any)
	if !ok || len(records) != want {
		t.Fatalf("context items want %d, got %v", want, data)
	}
	if data["consistency"] != "best_effort" || data["schema_version"] != float64(2) {
		t.Fatalf("context incorrectly claims transaction snapshot: %v", data)
	}
	return data, records
}

func workOnlyContextPrincipal(t *testing.T, f *fixture) string {
	t.Helper()
	ctx := context.Background()
	var workspace uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	principal, membership, credential := uuid.New(), uuid.New(), uuid.New()
	token, hash, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO qual.principals(id,workspace_id,kind,display_name) VALUES($1,$2,'agent','Context work-only agent')", principal, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO qual.memberships(id,workspace_id,principal_id,project_id,role) VALUES($1,$2,$3,$4,'project_viewer')", membership, workspace, principal, f.project); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, credential, workspace, principal, hash[:], []string{"work:read"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestQual27ContextRecoverableFactsAndPermissions(t *testing.T) {
	gateContext27(t)
	f := setup(t)
	ctx := context.Background()
	work := create(t, f, f.agent, "qual27-context-main")
	workID := work["id"].(string)
	path := contextURL(f, workID)
	blocker := create(t, f, f.admin, "qual27-blocker")
	blockerID := blocker["id"].(string)

	code, linked, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/dependencies", f.agent,
		"qual27-link-blocker", "", map[string]any{"to_item_id": blockerID, "expected_version": 1})
	if code != 201 || linked["version"] != float64(2) {
		t.Fatalf("dependency link %d %v", code, linked)
	}
	code, commented, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/comments", f.agent,
		"qual27-comment", "", map[string]any{"body": "External PI will resume from persisted facts", "expected_version": 2})
	if code != 201 || commented["version"] != float64(3) {
		t.Fatalf("comment %d %v", code, commented)
	}

	caseID, _ := mustCase(t, f, "qual27-case", "Context history case")
	versionID, _ := publishCase(t, f, caseID, "qual27-case-publish", 1)
	run := mustRun(t, f, []string{versionID}, "qual27-run")
	runItemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	failed := mustAttempt(t, f, runItemID, f.reporter, "qual27-failed", "failed")
	passed := mustAttempt(t, f, runItemID, f.reporter, "qual27-passed", "passed")
	code, _ = testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		"qual27-coverage", "", map[string]any{"work_item_id": workID})
	if code != 201 {
		t.Fatalf("coverage link %d", code)
	}
	artifactID, digest := testUploadArtifact(t, f, workID, []byte("Qual27 isolated real S3 bytes"))
	var objectKey string
	if err := f.pool.QueryRow(ctx, "SELECT object_key FROM qual.artifacts WHERE project_id=$1 AND id=$2",
		f.project, artifactID).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Remove only this test's unique S3 object; never delete other data.
		if err := f.objects.Delete(context.Background(), objectKey); err != nil {
			t.Errorf("test S3 object cleanup: %v", err)
		}
	})
	code, ref := testRequest(t, "POST", testsURL(f)+"/external-references", f.agent,
		"qual27-external-reference", "", map[string]any{"target_kind": "work_item", "target_id": workID,
			"provider": "github", "kind": "pull_request", "external_id": "qual27/synthetic/1",
			"url": "https://example.com/synthetic/qual27"})
	if code != 201 || ref["id"] == nil {
		t.Fatalf("reference %d %v", code, ref)
	}

	overview, items := sectionItems(t, path, f.agent, 0)
	_ = items
	if overview["context_revision"] != float64(3) || overview["work_item"].(map[string]any)["key"] == "" ||
		overview["section"] != "overview" || len(overview["available_sections"].([]any)) != 8 {
		t.Fatalf("summary not usable for recovery: %v", overview)
	}
	_, deps := sectionItems(t, path+"?section=dependencies", f.agent, 1)
	if deps[0].(map[string]any)["to_item_id"] != blockerID ||
		deps[0].(map[string]any)["source_key"] == "" ||
		deps[0].(map[string]any)["from_item_id"] != workID {
		t.Fatalf("wrong or unidentifiable blocker context %v", deps)
	}
	_, comments := sectionItems(t, path+"?section=comments", f.agent, 1)
	if comments[0].(map[string]any)["source"] != "work_comments" {
		t.Fatalf("comment provenance missing %v", comments)
	}
	_, coverage := sectionItems(t, path+"?section=coverage", f.agent, 1)
	if coverage[0].(map[string]any)["test_case_id"] != caseID ||
		coverage[0].(map[string]any)["source_key"] == "" ||
		coverage[0].(map[string]any)["work_item_id"] != workID {
		t.Fatalf("coverage has no stable relationship identity %v", coverage)
	}
	// Same real blocker/TestCase can link to multiple WorkItems: the source is
	// the relationship's composite key, not the reused target UUID.
	sibling := create(t, f, f.agent, "qual27-shared-source-sibling")
	siblingID := sibling["id"].(string)
	code, _, _ = request(t, "POST", f.url(f.project)+"/"+siblingID+"/dependencies", f.agent,
		"qual27-sibling-dependency", "", map[string]any{"to_item_id": blockerID, "expected_version": 1})
	if code != 201 {
		t.Fatalf("sibling dependency %d", code)
	}
	code, _ = testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		"qual27-sibling-coverage", "", map[string]any{"work_item_id": siblingID})
	if code != 201 {
		t.Fatalf("sibling coverage %d", code)
	}
	_, siblingDeps := sectionItems(t, contextURL(f, siblingID)+"?section=dependencies", f.agent, 1)
	_, siblingCoverage := sectionItems(t, contextURL(f, siblingID)+"?section=coverage", f.agent, 1)
	if siblingDeps[0].(map[string]any)["to_item_id"] != blockerID ||
		siblingDeps[0].(map[string]any)["source_key"] == deps[0].(map[string]any)["source_key"] ||
		siblingCoverage[0].(map[string]any)["test_case_id"] != caseID ||
		siblingCoverage[0].(map[string]any)["source_key"] == coverage[0].(map[string]any)["source_key"] {
		t.Fatalf("shared source aliased distinct links: %v %v", siblingDeps, siblingCoverage)
	}
	first, attempts := sectionItems(t, path+"?section=attempts&limit=1", f.agent, 1)
	if first["has_more"] != true || first["next_cursor"] == nil ||
		attempts[0].(map[string]any)["source_id"] != passed["id"] {
		t.Fatalf("latest report or cursor missing %v", first)
	}
	cursor := first["next_cursor"].(string)
	// A new Attempt is appended independently of the WorkItem.version.
	// It must not shift the next page onto a duplicate Passed record.
	newer := mustAttempt(t, f, runItemID, f.reporter, "qual27-newer-after-page", "blocked")
	if newer["id"] == passed["id"] {
		t.Fatal("newer Attempt ID reused")
	}
	second, older := sectionItems(t, path+"?section=attempts&limit=1&cursor="+cursor, f.agent, 1)
	if second["has_more"] != false || older[0].(map[string]any)["source_id"] != failed["id"] ||
		older[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("older failed attempt lost after passed %v", older)
	}
	// A different run using the same published CaseVersion must be mixed into
	// the global ingestion order; per-run limits cannot hide its newer Attempt.
	secondRun := mustRun(t, f, []string{versionID}, "qual27-second-run")
	secondRunItem := secondRun["items"].([]any)[0].(map[string]any)["id"].(string)
	secondAttempt := mustAttempt(t, f, secondRunItem, f.reporter, "qual27-second-run-attempt", "skipped")
	newestAcross, newestRows := sectionItems(t, path+"?section=attempts&limit=1", f.agent, 1)
	if newestRows[0].(map[string]any)["source_id"] != secondAttempt["id"] || newestAcross["has_more"] != true {
		t.Fatalf("multi-run latest Attempt lost %v", newestRows)
	}
	_, followingRows := sectionItems(t, path+"?section=attempts&limit=1&cursor="+newestAcross["next_cursor"].(string), f.agent, 1)
	if followingRows[0].(map[string]any)["source_id"] != newer["id"] {
		t.Fatalf("multi-run page global ordering wrong %v", followingRows)
	}
	_, artifacts := sectionItems(t, path+"?section=artifacts", f.agent, 1)
	if artifacts[0].(map[string]any)["source_id"] != artifactID ||
		artifacts[0].(map[string]any)["sha256"] != digest {
		t.Fatalf("unverified or wrong ready artifact %v", artifacts)
	}
	_, refs := sectionItems(t, path+"?section=references", f.agent, 1)
	if refs[0].(map[string]any)["source_id"] != ref["id"] {
		t.Fatalf("external source was not preserved %v", refs)
	}
	_, acceptance := sectionItems(t, path+"?section=acceptance", f.agent, 1)
	if acceptance[0].(map[string]any)["acceptance_mode"] != "not_completed" ||
		acceptance[0].(map[string]any)["decision"] != nil {
		t.Fatalf("in-progress item invented approval %v", acceptance)
	}

	workOnly := workOnlyContextPrincipal(t, f)
	if code, _ := contextRequest(t, path, workOnly); code != 200 {
		t.Fatalf("work-only principal cannot access safe overview: %d", code)
	}
	for _, sec := range []string{"coverage", "attempts", "artifacts"} {
		code, data := contextRequest(t, path+"?section="+sec, workOnly)
		if code != 403 {
			t.Fatalf("work-only reader saw protected section %s: %d %v", sec, code, data)
		}
	}
	if code, _ := contextRequest(t, path, ""); code != 401 {
		t.Fatalf("anonymous context read = %d", code)
	}
	if code, _ := contextRequest(t, path, f.reporter); code != 403 {
		t.Fatalf("test-only reporter bypassed work:read = %d", code)
	}
	if code, _ := contextRequest(t, contextURL(f, uuid.NewString()), f.agent); code != 404 {
		t.Fatalf("missing WorkItem read = %d", code)
	}
	otherURL := f.server.URL + "/api/v1/projects/" + f.otherProject.String() + "/work-items/" + workID + "/context"
	if code, _ := contextRequest(t, otherURL+"?section=artifacts", f.agent); code != 404 {
		t.Fatalf("foreign project artifact existence leak %d", code)
	}
	for _, bad := range []string{"?section=audit", "?section=attempts&limit=101",
		"?section=attempts&cursor=bad", "?section=attempts&section=coverage",
		"?limit=1&limit=2", "?bogus=yes", "?section=attempts&limit=-1",
		"?section=overview&cursor=bad", "?section=acceptance&cursor=bad", "?section=attempts&limit=abc"} {
		code, data := contextRequest(t, path+bad, f.agent)
		if code != 400 {
			t.Fatalf("bad query %s accepted: %d %v", bad, code, data)
		}
	}
	code, updated, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/comments", f.agent,
		"qual27-comment-after-page", "", map[string]any{"body": "Newer work context version", "expected_version": 3})
	if code != 201 || updated["version"] != float64(4) {
		t.Fatalf("concurrent WorkItem version %d %v", code, updated)
	}
	if code, data := contextRequest(t, fmt.Sprintf("%s?section=attempts&limit=1&cursor=%s", path, cursor), f.agent); code != 409 || data["error"] == nil {
		t.Fatalf("stale cursor did not trigger explicit 409 %d %v", code, data)
	}
}

// Reproduces both high fanout and sparse unrelated project histories via
// truly isolated HTTP+PG requests, not fake successful responses.
func TestQual27ContextWideFanoutAndHonestScanBudget(t *testing.T) {
	gateContext27(t)
	f := setup(t)
	ctx := context.Background()
	work := create(t, f, f.agent, "qual27-wide-fanout")
	workID := work["id"].(string)
	path := contextURL(f, workID) + "?section=attempts&limit=1"
	targetCase, _ := mustCase(t, f, "qual27-wide-case", "Qual27 fanout coverage")
	targetVersion, _ := publishCase(t, f, targetCase, "qual27-wide-publish", 1)
	code, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+targetCase+"/coverage", f.agent,
		"qual27-wide-coverage", "", map[string]any{"work_item_id": workID})
	if code != 201 {
		t.Fatalf("wide coverage %d", code)
	}
	var older, newer string
	for i := 0; i < 33; i++ {
		run := mustRun(t, f, []string{targetVersion}, fmt.Sprintf("qual27-wide-run-%02d", i))
		runItem := run["items"].([]any)[0].(map[string]any)["id"].(string)
		if i == 0 {
			older = mustAttempt(t, f, runItem, f.reporter, "qual27-wide-failed", "failed")["id"].(string)
		}
		if i == 32 {
			newer = mustAttempt(t, f, runItem, f.reporter, "qual27-wide-passed", "passed")["id"].(string)
		}
	}
	first, items := sectionItems(t, path, f.agent, 1)
	if items[0].(map[string]any)["source_id"] != newer || first["has_more"] != true {
		t.Fatalf("wide first page incorrect %v", first)
	}
	_, items = sectionItems(t, path+"&cursor="+first["next_cursor"].(string), f.agent, 1)
	if items[0].(map[string]any)["source_id"] != older || items[0].(map[string]any)["status"] != "failed" {
		t.Fatalf("old failure dropped by wide path %v", items)
	}

	// 700 newer but unrelated Attempts require two candidate batches; valid
	// history must still be returned with correct page cursor, not pre-limited.
	irrelevantCase, _ := mustCase(t, f, "qual27-unrelated-case", "No WorkItem coverage")
	irrelevantVersion, _ := publishCase(t, f, irrelevantCase, "qual27-unrelated-publish", 1)
	irrelevantRun := mustRun(t, f, []string{irrelevantVersion}, "qual27-unrelated-run")
	irrelevantRunID := irrelevantRun["id"].(string)
	irrelevantItem := irrelevantRun["items"].([]any)[0].(map[string]any)["id"].(string)
	var reporter uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT source_principal_id FROM qual.test_runs WHERE id=$1 AND project_id=$2", irrelevantRunID, f.project).Scan(&reporter); err != nil {
		t.Fatal(err)
	}
	insertIrrelevant := func(from, to int) {
		t.Helper()
		_, err := f.pool.Exec(ctx, `INSERT INTO qual.test_attempts(id,project_id,run_item_id,attempt_no,status,note,observed_at,ingested_at,reported_by,source_kind)
    SELECT gen_random_uuid(),$1,$2,g,'passed','isolated unrelated run',now(),now()+g*interval '1 millisecond',$3,'manual'
    FROM generate_series($4::integer,$5::integer) g`, f.project, irrelevantItem, reporter, from, to)
		if err != nil {
			t.Fatalf("insert isolated unrelated Attempts: %v", err)
		}
	}
	insertIrrelevant(1, 700)
	first, items = sectionItems(t, path, f.agent, 1)
	if items[0].(map[string]any)["source_id"] != newer || first["has_more"] != true {
		t.Fatalf("batch continuation lost matching Attempt: %v", first)
	}
	_, items = sectionItems(t, path+"&cursor="+first["next_cursor"].(string), f.agent, 1)
	if items[0].(map[string]any)["source_id"] != older {
		t.Fatalf("keyset batch continuation lost old source: %v", items)
	}

	// 17,200 unrelated newer Attempts exceed the explicit scan budget. The
	// endpoint must FAIL 503, not claim items=[] / has_more=false.
	insertIrrelevant(701, 17200)
	status, response := contextRequest(t, path, f.agent)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("scan budget must fail closed, got %d: %v", status, response)
	}
	fault, ok := response["error"].(map[string]any)
	if !ok || fault["code"] != "CONTEXT_QUERY_BUDGET" || response["items"] != nil {
		t.Fatalf("budget failure disguised as valid complete history: %v", response)
	}
	// The legitimate old failed row has not disappeared from the underlying
	// authoritative Attempt table, nor changed the WorkItem state.
	var preserved int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.test_attempts WHERE project_id=$1 AND id=$2", f.project, older).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("failed Attempt was lost: count=%d err=%v", preserved, err)
	}
	// A truly sparse WorkItem with ONE distinct covered RunItem must use
	// the narrow source index, not scan 17,200 newer unrelated project rows.
	narrowWork := create(t, f, f.agent, "qual27-narrow-with-unrelated-history")
	narrowWorkID := narrowWork["id"].(string)
	narrowCase, _ := mustCase(t, f, "qual27-narrow-case", "Sparse WorkItem source")
	narrowVersion, _ := publishCase(t, f, narrowCase, "qual27-narrow-publish", 1)
	code, _ = testRequest(t, "POST", testsURL(f)+"/test-cases/"+narrowCase+"/coverage", f.agent,
		"qual27-narrow-coverage", "", map[string]any{"work_item_id": narrowWorkID})
	if code != 201 {
		t.Fatalf("narrow coverage status %d", code)
	}
	narrowRun := mustRun(t, f, []string{narrowVersion}, "qual27-narrow-run")
	narrowItem := narrowRun["items"].([]any)[0].(map[string]any)["id"].(string)
	narrowAttempt := mustAttempt(t, f, narrowItem, f.reporter, "qual27-narrow-result", "passed")
	_, narrowRows := sectionItems(t, contextURL(f, narrowWorkID)+"?section=attempts&limit=1", f.agent, 1)
	if narrowRows[0].(map[string]any)["source_id"] != narrowAttempt["id"] {
		t.Fatalf("narrow query lost sparse fact behind unrelated project history: %v", narrowRows)
	}
}

func TestQual27ContextReviewAndActualAcceptanceHistory(t *testing.T) {
	gateContext27(t)
	f := setup(t)
	runID := reviewRun(t, f, "context27-review-run")
	itemID := reviewTarget(t, f, f.agent, "context27-review-work", runID)
	path := contextURL(f, itemID)

	// A real Reviewer reports needs_work first; even after later approval
	// the earlier result must remain visible in paged history.
	code, needs, _ := request(t, "POST", roleEndpoint(f, itemID), f.admin,
		"context27-review-needs-work", "",
		roleInput("reviewer", "needs_work", "none", "", "Independent reviewer requested changes", 4))
	if code != 201 || needs["outcome"] != "needs_work" || needs["self_review"] != false {
		t.Fatalf("needs_work %d %v", code, needs)
	}
	_, reviews := sectionItems(t, path+"?section=reviews", f.viewer, 1)
	if reviews[0].(map[string]any)["outcome"] != "needs_work" {
		t.Fatalf("latest reviewer history missing %v", reviews)
	}
	for _, step := range []struct {
		status  string
		version int
	}{{"in_progress", 5}, {"in_review", 6}} {
		code, data, _ := request(t, "POST", f.url(f.project)+"/"+itemID+"/transitions", f.agent,
			"context27-resubmit-"+step.status, "",
			map[string]any{"status": step.status, "expected_version": step.version})
		if code != 200 || data["status"] != step.status {
			t.Fatalf("resubmit %s: %d %v", step.status, code, data)
		}
	}
	code, passedReview, _ := request(t, "POST", roleEndpoint(f, itemID), f.admin,
		"context27-review-passed", "",
		roleInput("reviewer", "passed", "test_run", runID, "Actual independent review after resubmission", 7))
	if code != 201 || passedReview["self_review"] != false {
		t.Fatalf("reviewer passed %d %v", code, passedReview)
	}
	code, done, _ := request(t, "POST", completeURL(f, itemID), f.admin,
		"context27-review-complete", "", reviewCompletion(runID, 8, "independent_review", false))
	if code != 200 || done["status"] != "done" {
		t.Fatalf("independent Done Gate %d %v", code, done)
	}
	overview, _ := sectionItems(t, path, f.viewer, 0)
	h, ok := overview["acceptance"].(map[string]any)
	if !ok || h["acceptance_mode"] != "independent_review" {
		t.Fatalf("real acceptance not referenced %v", overview)
	}
	decision, ok := h["decision"].(map[string]any)
	if !ok || decision["independent"] != true || decision["explicit_self_review"] != false ||
		decision["reference_value"] != runID {
		t.Fatalf("lost independent decision or TestRun evidence %v", h)
	}
	_, history := sectionItems(t, path+"?section=reviews&limit=2", f.viewer, 2)
	if history[0].(map[string]any)["outcome"] != "passed" ||
		history[1].(map[string]any)["outcome"] != "needs_work" {
		t.Fatalf("new approval erased old needs_work %v", history)
	}
	_, decisions := sectionItems(t, path+"?section=acceptance", f.viewer, 1)
	if decisions[0].(map[string]any)["acceptance_mode"] != "independent_review" {
		t.Fatalf("explicit independent decision lost %v", decisions)
	}

	// Synthetic legacy row only in this disposable database: historical
	// Done without an acceptance decision must remain unclassified.
	legacy := create(t, f, f.admin, "context27-legacy-done")
	legacyID := legacy["id"].(string)
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.work_items SET status='done' WHERE project_id=$1 AND id=$2`, f.project, legacyID); err != nil {
		t.Fatal(err)
	}
	legacyView, _ := sectionItems(t, contextURL(f, legacyID), f.viewer, 0)
	legacyHistory, ok := legacyView["acceptance"].(map[string]any)
	if !ok || legacyHistory["acceptance_mode"] != "legacy_unclassified" ||
		legacyHistory["decision"] != nil {
		t.Fatalf("synthetic old Done fabricated approval %v", legacyView)
	}
}
