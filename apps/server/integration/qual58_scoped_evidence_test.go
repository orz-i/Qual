//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
)

// Goose20 introduces a single persisted WorkItem authority per TestRun. A
// shared TestCase coverage edge by itself must never grant an Agent ownership
// of another WorkItem's Run, Attempt, Artifact or RoleEntry.
func qual58Fixture(t *testing.T) (*fixture, string, string, string, string, string, string) {
	t.Helper()
	f := setup(t)
	var version int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version WHERE is_applied").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 20 {
		t.Skip("Goose20 isolated TestRun binding required")
	}
	caseID, _ := mustCase(t, f, "qual58-case-"+uuid.NewString()[:8], "QUAL-58 reusable published test")
	vid, _ := publishCase(t, f, caseID, "qual58-publish-"+uuid.NewString()[:8], 1)
	x := create(t, f, f.agent, "qual58-executor-x-"+uuid.NewString()[:8])
	y := create(t, f, f.agent, "qual58-unrelated-y-"+uuid.NewString()[:8])
	xid, yid := x["id"].(string), y["id"].(string)
	// Coverage is project-wide Case/TestCase governance and is configured by
	// a real authorized Agent before activating a scoped project.
	status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, "qual58-case-coverage-x-"+uuid.NewString(), "", map[string]any{"work_item_id": xid})
	if status != 201 {
		t.Fatalf("coverage x setup %d", status)
	}
	ta := createAccessToken(t, f, principalFor(t, f, f.agent), []string{"work:read", "work:claim"})
	tb := createAccessToken(t, f, principalFor(t, f, f.agent2), []string{"work:read", "work:claim"})
	qual57SeedMode(t, f)
	qual56Claim(t, f, ta, xid, "qual58-claim-x-"+uuid.NewString(), 1)
	qual56Status(t, f, ta, xid, "todo", "qual58-todo-"+uuid.NewString(), 2)
	qual56Status(t, f, ta, xid, "in_progress", "qual58-progress-"+uuid.NewString(), 3)
	return f, xid, yid, caseID, vid, ta, tb
}
func qual58Run(t *testing.T, f *fixture, token, workID, vid, key string) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"name": "Actual isolated external test observation", "case_version_ids": []string{vid}, "source_system": "external-qual58", "external_run_key": key, "environment": "isolated"}
	if workID != "" {
		body["work_item_id"] = workID
	}
	status, data := testRequest(t, "POST", testsURL(f)+"/test-runs", token, key, "", body)
	return status, data
}

// Reusable scoped-review fixture for QUAL-55/57 regression. The legacy
// Goose18/19 boundary uses existing unbound Run + Case coverage; Goose20
// creates an authoritative bound Run only AFTER coverage is recorded.
func reviewRunForWork(t *testing.T, f *fixture, prefix, itemID string) string {
	t.Helper()
	var schema bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	caseID, _ := mustCase(t, f, prefix+"-case", "Bound scoped review evidence fixture")
	versionID, _ := publishCase(t, f, caseID, prefix+"-publish", 1)
	status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, prefix+"-coverage", "", map[string]any{"work_item_id": itemID})
	if status != 201 {
		t.Fatalf("WorkItem review coverage setup %d", status)
	}
	body := map[string]any{"name": "Synthetic external run for actual policy tests",
		"source_system": "external-ci", "external_run_key": prefix + "-run",
		"case_version_ids": []string{versionID}, "environment": "isolated fixture", "build_ref": "qual58-bound"}
	if schema {
		body["work_item_id"] = itemID
	}
	status, run := testRequest(t, "POST", testsURL(f)+"/test-runs", f.agent, prefix+"-run", "", body)
	if status != 201 {
		t.Fatalf("scoped review Run setup %d %v", status, run)
	}
	item := run["items"].([]any)[0].(map[string]any)
	mustAttempt(t, f, item["id"].(string), f.agent, prefix+"-attempt", "passed")
	return run["id"].(string)
}

func TestQual58BoundRunAndAttemptTaskPermissions(t *testing.T) {
	f, x, y, caseID, vid, ta, tb := qual58Fixture(t)
	status, run := qual58Run(t, f, ta, x, vid, "qual58-bound-run-x")
	if status != 201 || run["work_item_id"] != x {
		t.Fatalf("bound Run failed %d %v", status, run)
	}
	runID := run["id"].(string)
	itemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	if status, _ := qual58Run(t, f, f.agent, y, vid, "qual58-run-for-uncovered-y"); status != 403 {
		t.Fatalf("uncovered task Run accepted %d", status)
	}
	if status, _ := qual58Run(t, f, tb, x, vid, "qual58-b-run-x"); status != 403 {
		t.Fatalf("unassigned B created X Run %d", status)
	}
	if status, _ := qual58Run(t, f, f.agent, "", vid, "qual58-a-unbound-run"); status != 403 {
		t.Fatalf("broad test:write minted unbound run %d", status)
	}
	if status, _ := qual58Run(t, f, f.admin, "", vid, "qual58-manager-unbound-run"); status != 201 {
		t.Fatalf("explicit admin cannot create unbound Run %d", status)
	}
	if status, _ := qual58Run(t, f, f.reporter, "", vid, "qual58-integration-unbound-run"); status != 201 {
		t.Fatalf("integration reporter regressed unbound Run %d", status)
	}
	if status, _ := qual58Run(t, f, f.reporter, x, vid, "qual58-integration-bound-run"); status != 201 {
		t.Fatalf("integration reporter cannot report its authorized bound run %d", status)
	}
	// Different project-wide credential permissions are NOT task relationships.
	if status, _, _ := request(t, "GET", testsURL(f)+"/test-runs/"+runID, f.agent2, "", "", nil); status != 403 {
		t.Fatalf("unassigned B read X private run %d", status)
	}
	if status, _, _ := request(t, "GET", testsURL(f)+"/test-runs/"+runID, f.viewer, "", "", nil); status != 403 {
		t.Fatalf("viewer read X private run %d", status)
	}
	if status, read, _ := request(t, "GET", testsURL(f)+"/test-runs/"+runID, ta, "", "", nil); status != 200 || read["work_item_id"] != x {
		t.Fatalf("assignee cannot read own bound Run %d %v", status, read)
	}
	if status, _ := testRequest(t, "POST", attemptURL(f, itemID), tb, "qual58-b-illegal-attempt", "",
		map[string]any{"status": "passed", "source_kind": "external"}); status != 403 {
		t.Fatalf("B wrote unrelated Attempt %d", status)
	}
	status, attempt := testRequest(t, "POST", attemptURL(f, itemID), ta, "qual58-a-real-attempt", "",
		map[string]any{"status": "failed", "note": "Synthetic isolated observed failure", "source_kind": "external"})
	if status != 201 || attempt["reported_by"] != principalFor(t, f, f.agent).String() {
		t.Fatalf("scoped Attempt failed %d %v", status, attempt)
	}
	status, replay := testRequest(t, "POST", attemptURL(f, itemID), ta, "qual58-a-real-attempt", "",
		map[string]any{"status": "failed", "note": "Synthetic isolated observed failure", "source_kind": "external"})
	if status != 201 || replay["id"] != attempt["id"] {
		t.Fatalf("Attempt replay mismatch %d %v", status, replay)
	}
	if status, _ := testRequest(t, "POST", attemptURL(f, itemID), ta, "qual58-a-real-attempt", "",
		map[string]any{"status": "passed", "source_kind": "external"}); status != 409 {
		t.Fatalf("different same-key Attempt accepted %d", status)
	}
	if status, items := testRequest(t, "GET", attemptURL(f, itemID), ta, "", "", nil); status != 200 || len(items["items"].([]any)) != 1 {
		t.Fatalf("own attempt unreadable %d %v", status, items)
	}
	if status, _ := testRequest(t, "GET", attemptURL(f, itemID), tb, "", "", nil); status != 403 {
		t.Fatalf("cross-task Attempt read permitted %d", status)
	}
	// A second case may be linked only to A's claimed in_progress WorkItem,
	// never a forged unrelated Y. Publishing CaseVersions remains project-scope.
	case2, _ := mustCase(t, f, "qual58-case-extra-"+uuid.NewString()[:8], "Extra published case")
	if status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+case2+"/coverage",
		f.agent, "qual58-forged-other-coverage", "", map[string]any{"work_item_id": y}); status != 403 {
		t.Fatalf("forged coverage toward unclaimed Y succeeded %d", status)
	}
	if status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+case2+"/coverage",
		ta, "qual58-authentic-own-coverage", "", map[string]any{"work_item_id": x}); status != 201 {
		t.Fatalf("minimal Executor cannot bind own WorkItem coverage %d", status)
	}
	if status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases", ta, "qual58-minimal-must-not-create-case", "",
		map[string]any{"specification": caseSpec("Fake project-level TestCase")}); status != 403 {
		t.Fatalf("minimal Executor could publish shared spec %d", status)
	}
	if status, _ := testRequest(t, "GET", testsURL(f)+"/test-runs", ta, "", "", nil); status != 403 {
		t.Fatalf("minimal task actor accessed global Run list %d", status)
	}
	// A former Executor retains exactly the same token; after release it
	// immediately loses the ability to report or view this WorkItem's results.
	if status, _ := qual56Write(t, f, ta, x, "release", "qual58-x-released", 4, uuid.Nil); status != 200 {
		t.Fatalf("Release failed %d", status)
	}
	if status, _ := testRequest(t, "POST", attemptURL(f, itemID), ta, "qual58-released-attempt", "",
		map[string]any{"status": "passed", "source_kind": "external"}); status != 403 {
		t.Fatalf("released Executor still reported Attempt %d", status)
	}
	if status, _ := testRequest(t, "GET", attemptURL(f, itemID), ta, "", "", nil); status != 403 {
		t.Fatalf("released Executor still read Attempt %d", status)
	}
	if status, _ := qual58Run(t, f, ta, x, vid, "qual58-released-run"); status != 403 {
		t.Fatalf("released Executor could create bound Run %d", status)
	}
	if status, _ := qual58Run(t, f, f.agent2, x, vid, "qual58-b-unclaimed-run"); status != 403 {
		t.Fatalf("B without claim could write X Run %d", status)
	}
	qual56Claim(t, f, tb, x, "qual58-b-resumes-x", 5)
	if status, _ := testRequest(t, "POST", attemptURL(f, itemID), tb, "qual58-b-new-attempt", "",
		map[string]any{"status": "passed", "source_kind": "external"}); status != 201 {
		t.Fatalf("new Executor cannot record bound Attempt %d", status)
	}
	_ = caseID
}
func TestQual58ScopedArtifactUploadAndRevocation(t *testing.T) {
	f, x, y, _, _, ta, tb := qual58Fixture(t)
	if f.objects == nil {
		t.Skip("real isolated local private S3 not configured")
	}
	data := []byte("QUAL58: observed attached bytes; Qual never executes tests or Agents\n")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	begin := func(token, work, key string) (int, map[string]any) {
		return testRequest(t, "POST", evidenceURL(f)+"/artifacts", token, key, "", map[string]any{
			"target_kind": "work_item", "target_id": work, "filename": "qual58-evidence.txt", "media_type": "text/plain",
			"size_bytes": len(data), "sha256": hash,
		})
	}
	if status, _ := begin(tb, x, "qual58-b-begin-x-deny"); status != 403 {
		t.Fatalf("B began X artifact %d", status)
	}
	if status, _ := begin(f.agent, y, "qual58-a-begin-unclaimed-y"); status != 403 {
		t.Fatalf("A began Y artifact %d", status)
	}
	if status, _ := begin(f.reporter, x, "qual58-reporter-begin-work-deny"); status != 403 {
		t.Fatalf("integration was promoted to executor %d", status)
	}
	status, art := begin(ta, x, "qual58-a-begin-x")
	if status != 201 {
		t.Fatalf("scoped begin %d %v", status, art)
	}
	aid := art["id"].(string)
	artifactURL := evidenceURL(f) + "/artifacts/" + aid
	if status, _ := rawHTTP(t, "PUT", artifactURL+"/content", ta, "qual58-a-upload", data, "text/plain"); status != 200 {
		t.Fatalf("scoped upload %d", status)
	}
	if status, ready := testRequest(t, "POST", artifactURL+"/complete", ta, "qual58-a-complete", "", nil); status != 200 || ready["state"] != "ready" {
		t.Fatalf("scoped Ready %d %v", status, ready)
	}
	if status, got := rawHTTP(t, "GET", artifactURL+"/content", ta, "", nil, ""); status != 200 || !bytes.Equal(got, data) {
		t.Fatalf("scoped SHA verified download %d", status)
	}
	if status, _ := rawHTTP(t, "GET", artifactURL+"/content", tb, "", nil, ""); status != 403 {
		t.Fatalf("unassigned B downloaded X bytes %d", status)
	}
	if status, _ := testRequest(t, "GET", artifactURL, f.viewer, "", "", nil); status != 403 {
		t.Fatalf("viewer read X private artifact %d", status)
	}
	if status, _ := testRequest(t, "GET", evidenceURL(f)+"/artifacts?target_kind=work_item&target_id="+x, tb, "", "", nil); status != 403 {
		t.Fatalf("B enumerated X artifacts %d", status)
	}
	status, pending := begin(ta, x, "qual58-a-begin-pending")
	if status != 201 {
		t.Fatalf("pending Begin %d", status)
	}
	pendingURL := evidenceURL(f) + "/artifacts/" + pending["id"].(string)
	if status, _ := qual56Write(t, f, ta, x, "release", "qual58-artifact-executor-release", 4, uuid.Nil); status != 200 {
		t.Fatal(status)
	}
	if status, _ := rawHTTP(t, "PUT", pendingURL+"/content", ta, "qual58-released-upload-denied", data, "text/plain"); status != 403 {
		t.Fatalf("released A uploaded pending bytes %d", status)
	}
	if status, _ := testRequest(t, "POST", pendingURL+"/complete", ta, "qual58-released-ready-denied", "", nil); status != 403 {
		t.Fatalf("released A marked ready %d", status)
	}
	if status, _ := rawHTTP(t, "GET", artifactURL+"/content", ta, "", nil, ""); status != 403 {
		t.Fatalf("released A read old private bytes %d", status)
	}
	qual56Claim(t, f, tb, x, "qual58-artifact-b-resumes", 5)
	if status, got := rawHTTP(t, "GET", artifactURL+"/content", tb, "", nil, ""); status != 200 || !bytes.Equal(got, data) {
		t.Fatalf("B successor cannot read old evidence %d", status)
	}
	if status, _ := rawHTTP(t, "PUT", pendingURL+"/content", tb, "qual58-b-cannot-take-A-pending", data, "text/plain"); status != 403 {
		t.Fatalf("B hijacked A's pending artifact %d", status)
	}
}

func TestQual58RoleEntryReferencesMustBelongToCurrentWorkItem(t *testing.T) {
	f, x, y, caseID, vid, ta, tb := qual58Fixture(t)
	status, rx := qual58Run(t, f, ta, x, vid, "qual58-role-bound-run-x")
	if status != 201 {
		t.Fatalf("Run X setup failed %d", status)
	}
	xrun := rx["id"].(string)
	status, unbound := qual58Run(t, f, f.admin, "", vid, "qual58-role-admin-global-run")
	if status != 201 {
		t.Fatalf("unbound manager Run setup %d", status)
	}
	status, entry, _ := request(t, "POST", roleEndpoint(f, x), ta, "qual58-valid-tester-x", "",
		roleInput("tester", "passed", "test_run", xrun, "Tester recorded a test for the current WorkItem X", 4))
	if status != 201 || entry["actor_id"] != principalFor(t, f, f.agent).String() {
		t.Fatalf("own bound tester fact failed %d %v", status, entry)
	}
	status, _, _ = request(t, "POST", roleEndpoint(f, x), ta, "qual58-unbound-tester-denied", "",
		roleInput("tester", "passed", "test_run", unbound["id"].(string), "Attempt to cite unbound TestRun for scoped Task X", 5))
	if status != 403 {
		t.Fatalf("scoped tester cited unbound Run %d", status)
	}
	// B claims Y and records its own bound real Run; A cannot cite B's Run
	// despite the TestCase being reusable and both belonging to this Project.
	qual56Claim(t, f, tb, y, "qual58-role-b-claim-y", 1)
	qual56Status(t, f, tb, y, "todo", "qual58-role-b-todo", 2)
	qual56Status(t, f, tb, y, "in_progress", "qual58-role-b-progress", 3)
	status, _ = testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		tb, "qual58-role-b-y-coverage", "", map[string]any{"work_item_id": y})
	if status != 201 {
		t.Fatalf("B coverage Y failed %d", status)
	}
	status, ry := qual58Run(t, f, tb, y, vid, "qual58-role-bound-run-y")
	if status != 201 {
		t.Fatalf("Run Y setup failed %d", status)
	}
	status, _, _ = request(t, "POST", roleEndpoint(f, x), ta, "qual58-b-run-as-a-tester-denied", "",
		roleInput("tester", "passed", "test_run", ry["id"].(string), "Attempt to cite another WorkItem Y TestRun for X", 5))
	if status != 403 {
		t.Fatalf("tester used cross-task Run %d", status)
	}
	status, _, _ = request(t, "POST", roleEndpoint(f, x), tb, "qual58-b-auditor-fake-denied", "",
		roleInput("auditor", "observed", "none", "", "B cannot create X auditor fact through task claim", 5))
	if status != 403 {
		t.Fatalf("B created unrelated auditor fact %d", status)
	}
	// Explicit project TestWrite Deny wins over an actual A executor capacity.
	a := principalFor(t, f, f.agent)
	if code, _ := setPermission(t, f, f.admin, a, "test:write", "deny", 0, "qual58-test-write-deny"); code != 200 {
		t.Fatalf("test write Deny %d", code)
	}
	status, _, _ = request(t, "POST", roleEndpoint(f, x), ta, "qual58-tester-denied-after-policy", "",
		roleInput("tester", "passed", "test_run", xrun, "Policy explicit Deny must block current executor", 5))
	if status != 403 {
		t.Fatalf("explicit TestWrite Deny bypassed for Tester RoleEntry %d", status)
	}
	var version int
	if err := f.pool.QueryRow(context.Background(), "SELECT version FROM qual.work_items WHERE project_id=$1 AND id=$2", f.project, x).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("denied role events touched WorkItem v%d", version)
	}
}
func TestQual58ScopedDoneGateRejectsUnboundTestRun(t *testing.T) {
	f, x, _, _, vid, ta, _ := qual58Fixture(t)
	status, run := qual58Run(t, f, ta, x, vid, "qual58-done-bound-run-x")
	if status != 201 {
		t.Fatalf("bound Run setup %d", status)
	}
	runID := run["id"].(string)
	runItem := run["items"].([]any)[0].(map[string]any)["id"].(string)
	if status, _ := testRequest(t, "POST", attemptURL(f, runItem), ta, "qual58-done-real-attempt", "",
		map[string]any{"status": "passed", "note": "Actually observed synthetic fixture Attempt", "source_kind": "external"}); status != 201 {
		t.Fatalf("bound Attempt reported %d", status)
	}
	status, unbound := qual58Run(t, f, f.admin, "", vid, "qual58-done-unbound-manager-run")
	if status != 201 {
		t.Fatalf("unbound fixture Run %d", status)
	}
	status, _, _ = request(t, "PUT", f.server.URL+"/api/v1/projects/"+f.project.String()+"/review-policy",
		f.admin, "qual58-owner-policy", "", map[string]any{
			"default_mode": "owner_acceptance", "owner_acceptance_allowed": true,
			"force_independent_for_high_risk": true, "expected_version": 0,
			"reason": "Isolated Qual58 Owner signoff allowed for ordinary tasks with genuine bound Test evidence",
		})
	if status != 200 {
		t.Fatalf("Owner policy setup %d", status)
	}
	qual56Status(t, f, ta, x, "in_review", "qual58-done-submit-review", 4)
	path := f.url(f.project) + "/" + x + "/complete"
	status, _, _ = request(t, "POST", path, ta, "qual58-unbound-run-cannot-complete", "",
		reviewCompletion(unbound["id"].(string), 5, "owner_acceptance", true))
	if status != 422 {
		t.Fatalf("unbound project Run incorrectly completed Task X %d", status)
	}
	status, done, _ := request(t, "POST", path, ta, "qual58-bound-run-valid-owner-complete", "",
		reviewCompletion(runID, 5, "owner_acceptance", true))
	if status != 200 || done["status"] != "done" {
		t.Fatalf("valid bound Run Owner Done failed %d %v", status, done)
	}
	status, receipt, _ := request(t, "GET", f.url(f.project)+"/"+x+"/acceptances", ta, "", "", nil)
	if status != 200 || receipt["decision"] == nil {
		t.Fatalf("Done receipt missing %d", status)
	}
	decision := receipt["decision"].(map[string]any)
	if decision["independent"] != false || decision["explicit_self_review"] != true {
		t.Fatalf("Owner did not preserve self_review provenance %v", decision)
	}
	if status, _ := testRequest(t, "POST", attemptURL(f, runItem), ta, "qual58-done-further-attempt", "",
		map[string]any{"status": "passed", "source_kind": "external"}); status != 403 {
		t.Fatalf("completed WorkItem allowed further task-bound Attempt %d", status)
	}
}

// A task-bound TestRun and its immutable Attempt are legitimate Artifact
// targets. A shared Case alone isn't an authority for the bytes.
func TestQual58ArtifactsAttachedToBoundRunAndIntegrationReporter(t *testing.T) {
	f, x, _, _, v, ta, tb := qual58Fixture(t)
	if f.objects == nil {
		t.Skip("real isolated local S3 not configured")
	}
	status, run := qual58Run(t, f, ta, x, v, "qual58-artifact-run-target")
	if status != 201 {
		t.Fatalf("Run setup %d", status)
	}
	runID := run["id"].(string)
	runItem := run["items"].([]any)[0].(map[string]any)["id"].(string)
	status, attempt := testRequest(t, "POST", attemptURL(f, runItem), ta,
		"qual58-artifact-attempt-for-bytes", "", map[string]any{
			"status": "passed", "source_kind": "external", "note": "Actual isolated external report",
		})
	if status != 201 {
		t.Fatalf("Attempt setup %d", status)
	}
	payload := []byte("Bound TestRun/Attempt object bytes retained with real SHA256\n")
	digest := sha256.Sum256(payload)
	sha := hex.EncodeToString(digest[:])
	begin := func(token, kind, target, key string) (int, map[string]any) {
		return testRequest(t, "POST", evidenceURL(f)+"/artifacts", token, key, "", map[string]any{
			"target_kind": kind, "target_id": target, "filename": "qual58-run-test.txt",
			"media_type": "text/plain", "sha256": sha, "size_bytes": len(payload),
		})
	}
	if status, _ := begin(tb, "test_run", runID, "qual58-b-unrelated-run-artifact-denied"); status != 403 {
		t.Fatalf("B attached foreign Run artifact %d", status)
	}
	if status, _ := begin(f.reporter, "work_item", x, "qual58-reporter-no-work-artifact"); status != 403 {
		t.Fatalf("Integration reporter acquired WorkItem write %d", status)
	}
	status, art := begin(ta, "test_run", runID, "qual58-run-artifact-begin")
	if status != 201 {
		t.Fatalf("Own bound Run artifact begin %d %v", status, art)
	}
	id := art["id"].(string)
	url := evidenceURL(f) + "/artifacts/" + id
	if code, _ := rawHTTP(t, "PUT", url+"/content", ta, "qual58-run-artifact-upload", payload, "text/plain"); code != 200 {
		t.Fatalf("Own bound Run artifact upload %d", code)
	}
	if status, ready := testRequest(t, "POST", url+"/complete", ta, "qual58-run-artifact-ready", "", nil); status != 200 || ready["state"] != "ready" {
		t.Fatalf("Own bound Run artifact ready %d %v", status, ready)
	}
	if code, data := rawHTTP(t, "GET", url+"/content", ta, "", nil, ""); code != 200 || !bytes.Equal(data, payload) {
		t.Fatalf("Own bound Run bytes unreadable %d", code)
	}
	if code, _ := rawHTTP(t, "GET", url+"/content", tb, "", nil, ""); code != 403 {
		t.Fatalf("B read foreign Run bytes %d", code)
	}
	status, fromAttempt := begin(ta, "test_attempt", attempt["id"].(string), "qual58-attempt-artifact-begin")
	if status != 201 {
		t.Fatalf("Own bound Attempt target rejected %d %v", status, fromAttempt)
	}
	if status, _ := begin(f.reporter, "test_run", runID, "qual58-reporter-artifact-run-allowed"); status != 201 {
		t.Fatalf("Independently authorized Integration Reporter regressed scoped Run artifact %d", status)
	}
	// Legacy project-level ArtifactWrite must not bypass an explicit deny
	// after the initial Ready, even if the actor is still the executor.
	a := principalFor(t, f, f.agent)
	if status, _ := setPermission(t, f, f.admin, a, "artifact:read", "deny", 0, "qual58-artifact-read-deny"); status != 200 {
		t.Fatalf("ArtifactRead project Deny setup %d", status)
	}
	if code, _ := rawHTTP(t, "GET", url+"/content", ta, "", nil, ""); code != 403 {
		t.Fatalf("Project ArtifactRead deny bypassed %d", code)
	}
	if status, _ := setPermission(t, f, f.admin, a, "artifact:write", "deny", 0, "qual58-artifact-write-deny"); status != 200 {
		t.Fatalf("ArtifactWrite project Deny setup %d", status)
	}
	if status, _ := begin(ta, "test_run", runID, "qual58-artifact-write-denied-after-policy"); status != 403 {
		t.Fatalf("Project ArtifactWrite deny bypassed %d", status)
	}
}

func TestQual58BoundRunAutoLinksCaseCoverageOnlyForCurrentExecutor(t *testing.T) {
	f, x, y, _, _, ta, tb := qual58Fixture(t)
	caseID, _ := mustCase(t, f, "qual58-autocoverage-"+uuid.NewString()[:8],
		"Fresh published CaseVersion without any WorkItem coverage")
	vid, _ := publishCase(t, f, caseID, "qual58-autocoverage-publish", 1)
	if status, _ := qual58Run(t, f, f.reporter, x, vid, "qual58-reporter-must-not-create-coverage"); status != 403 {
		t.Fatalf("integration invented task coverage via bound Run: %d", status)
	}
	if status, _ := qual58Run(t, f, tb, x, vid, "qual58-unassigned-must-not-create-coverage"); status != 403 {
		t.Fatalf("non-assignee created task coverage via Run: %d", status)
	}
	if status, _ := qual58Run(t, f, f.agent, y, vid, "qual58-other-unclaimed-must-not-create-coverage"); status != 403 {
		t.Fatalf("executor A invented unrelated Y coverage: %d", status)
	}
	// One REST operation now records both its legitimate WorkItem coverage
	// and a frozen Run. The CLI "test report -work-item" remains usable
	// without a preliminary separate coverage command.
	status, created := qual58Run(t, f, ta, x, vid, "qual58-executor-atomic-run-and-coverage")
	if status != 201 || created["work_item_id"] != x {
		t.Fatalf("scoped Run/coverage atomic create failed: %d %v", status, created)
	}
	var links, audit, outbox int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.test_case_coverage WHERE project_id=$1 AND work_item_id=$2 AND test_case_id=$3",
		f.project, x, caseID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND operation='test_case.coverage_linked' AND entity_id=$2",
		f.project, caseID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND event_type='test_case.coverage_linked' AND aggregate_id=$2",
		f.project, caseID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if links != 1 || audit != 1 || outbox != 1 {
		t.Fatalf("bound Run failed atomic coverage/Audit/Outbox: %d/%d/%d", links, audit, outbox)
	}
	// Even an authenticated local database connection used by the isolated
	// fixture cannot retarget or rewrite historical TestRun source identity.
	if _, err := f.pool.Exec(context.Background(),
		"UPDATE qual.test_runs SET work_item_id=$1 WHERE project_id=$2 AND id=$3",
		y, f.project, created["id"]); err == nil {
		t.Fatal("Goose20 unexpectedly permitted rewriting a frozen TestRun binding")
	}
	if status, replay := qual58Run(t, f, ta, x, vid, "qual58-executor-atomic-run-and-coverage"); status != 201 || replay["id"] != created["id"] {
		t.Fatalf("identical Run retry not idempotent: %d %v", status, replay)
	}
	var postReplayLinks, postReplayAudit int
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.test_case_coverage WHERE project_id=$1 AND work_item_id=$2 AND test_case_id=$3",
		f.project, x, caseID).Scan(&postReplayLinks); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2 AND operation='test_case.coverage_linked'",
		f.project, caseID).Scan(&postReplayAudit); err != nil {
		t.Fatal(err)
	}
	if postReplayLinks != 1 || postReplayAudit != 1 {
		t.Fatalf("Run replay duplicated coverage: %d/%d", postReplayLinks, postReplayAudit)
	}
	if status, _ := qual58Run(t, f, ta, x, vid, "qual58-executor-atomic-run-and-coverage-different-key"); status != 201 {
		t.Fatalf("fresh independent Run referencing existing coverage failed: %d", status)
	}
}

func TestQual58BoundTesterOnAnotherTaskCanIndependentlyReviewThisTask(t *testing.T) {
	f, x, y, caseID, vid, ta, tb := qual58Fixture(t)
	// The same published Case legitimately covers two different tasks.
	qual56Claim(t, f, tb, y, "qual58-review-tester-y-claim", 1)
	qual56Status(t, f, tb, y, "todo", "qual58-review-tester-y-todo", 2)
	qual56Status(t, f, tb, y, "in_progress", "qual58-review-tester-y-progress", 3)
	status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		tb, "qual58-review-shared-case-y", "", map[string]any{"work_item_id": y})
	if status != 201 {
		t.Fatalf("shared Case Y coverage setup denied %d", status)
	}
	status, runY := qual58Run(t, f, tb, y, vid, "qual58-review-b-testing-y")
	if status != 201 {
		t.Fatalf("B TestRun Y setup failed %d", status)
	}
	itemY := runY["items"].([]any)[0].(map[string]any)["id"].(string)
	if status, _ := testRequest(t, "POST", attemptURL(f, itemY), tb, "qual58-review-b-attempt-y", "",
		map[string]any{"status": "passed", "source_kind": "external", "note": "B actually tested Y in an isolated fixture"}); status != 201 {
		t.Fatalf("B TestAttempt Y setup failed %d", status)
	}
	status, runX := qual58Run(t, f, ta, x, vid, "qual58-review-a-testing-x")
	if status != 201 {
		t.Fatalf("A TestRun X setup failed %d", status)
	}
	itemX := runX["items"].([]any)[0].(map[string]any)["id"].(string)
	if status, _ := testRequest(t, "POST", attemptURL(f, itemX), ta, "qual58-review-a-attempt-x", "",
		map[string]any{"status": "passed", "source_kind": "external", "note": "A actually tested X in an isolated fixture"}); status != 201 {
		t.Fatalf("A TestAttempt X setup failed %d", status)
	}
	qual56Status(t, f, ta, x, "in_review", "qual58-review-x-submit", 4)
	if status, claimed := qual57ReviewAction(t, f, tb, x, "review-claim", "qual58-review-b-independent-x", 5); status != 200 || claimed["version"] != float64(6) {
		t.Fatalf("B tested unrelated Y but cannot claim independent X review: %d %v", status, claimed)
	}
	status, review := qual57Review(t, f, tb, x, runX["id"].(string), "qual58-review-b-passed-x", 6, "passed")
	if status != 201 || review["self_review"] != false {
		t.Fatalf("B reviewer on X incorrectly marked self-review due to shared Case Y: %d %v", status, review)
	}
	status, done, _ := request(t, "POST", f.url(f.project)+"/"+x+"/complete", tb,
		"qual58-review-b-independent-complete-x", "",
		reviewCompletion(runX["id"].(string), 7, "independent_review", false))
	if status != 200 || done["status"] != "done" {
		t.Fatalf("Legitimate independent Reviewer B unable to finish X: %d %v", status, done)
	}
}
