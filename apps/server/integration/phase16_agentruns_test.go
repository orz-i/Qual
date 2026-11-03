//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
)

// This test intentionally refuses to touch the live Qual database. The
// caller must create an isolated migrated qual32_run_* database first.
func TestAgentRunAuthorizedExternalFacts(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_") {
		t.Skip("requires disposable qual32_run_* PostgreSQL database")
	}
	f := setup(t)
	ctx := context.Background()
	work := create(t, f, f.agent, "qual32-work-create")
	workID := work["id"].(string)
	base := fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
	registerURL := base + "/agent-runs"
	registration := map[string]any{"work_item_id": workID, "source": "pi", "external_run_id": "run-qual32-a", "host": "isolated-pi"}

	// An old broad work:write PAT is NOT automatically a run reporter.
	if s, _, _ := request(t, "POST", registerURL, f.agent, "qual32-deny-old-scope", "", registration); s != 403 {
		t.Fatalf("legacy Agent run:report must be denied: %d", s)
	}
	grant := func(token, key string) uuid.UUID {
		t.Helper()
		digest, err := identity.HashToken(token)
		if err != nil {
			t.Fatal(err)
		}
		var principal uuid.UUID
		if err := f.pool.QueryRow(ctx, `SELECT principal_id FROM qual.credentials WHERE token_sha256=$1`, digest[:]).Scan(&principal); err != nil {
			t.Fatal(err)
		}
		// Disposable synthetic Credential only. The grant itself uses the
		// real authorized REST access:manage path and its audit/CAS.
		if _, err := f.pool.Exec(ctx, `UPDATE qual.credentials SET scopes=array_append(scopes,'run:report')
            WHERE token_sha256=$1`, digest[:]); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("%s/access/%s/permissions/run:report", base, principal)
		s, data, _ := request(t, "PUT", path, f.admin, key, "", map[string]any{
			"effect": "allow", "expected_version": 0, "reason": "Authorize isolated synthetic AgentRun integration reporter",
		})
		if s != 200 {
			t.Fatalf("explicit grant status=%d body=%v", s, data)
		}
		return principal
	}
	grant(f.agent, "qual32-grant-agentA")
	if s, _, _ := request(t, "POST", registerURL, f.viewer, "qual32-deny-viewer", "", registration); s != 403 {
		t.Fatalf("viewer unexpectedly registered run: %d", s)
	}
	if s, _, _ := request(t, "POST", registerURL, f.agent, "qual32-no-item", "",
		map[string]any{"work_item_id": uuid.NewString(), "source": "pi", "external_run_id": "unbound", "host": "pi"}); s != 404 {
		t.Fatalf("foreign work item must be hidden: %d", s)
	}
	s, run, _ := request(t, "POST", registerURL, f.agent, "qual32-run-create", "", registration)
	if s != 201 || run["source_status"] != "queued" || run["version"] != float64(1) {
		t.Fatalf("run registered=%d %v", s, run)
	}
	runID := run["id"].(string)
	// QUAL-45: PostgreSQL TEXT rejects NUL; public API must respond 422,
	// not leak a persistence error as an unexpected HTTP 500.
	for i, bad := range []string{"mac\x00mini", "mac\nmini", "mac\tmini", "mac\x7fmini"} {
		badInput := map[string]any{"work_item_id": workID, "source": "pi", "external_run_id": fmt.Sprintf("bad-host-%d", i), "host": bad}
		if status, _, _ := request(t, "POST", registerURL, f.agent, fmt.Sprintf("qual45-invalid-host-%d", i), "", badInput); status != 422 {
			t.Fatalf("invalid Host %q must be 422, got %d", bad, status)
		}
	}
	s, replay, _ := request(t, "POST", registerURL, f.agent, "qual32-run-create", "", registration)
	if s != 201 || replay["id"] != runID {
		t.Fatalf("same key replay %d %v", s, replay)
	}
	s, replay, _ = request(t, "POST", registerURL, f.agent, "qual32-run-create-second-key", "", registration)
	if s != 201 || replay["id"] != runID {
		t.Fatalf("same external ID replay %d %v", s, replay)
	}
	if s, _, _ := request(t, "POST", registerURL, f.agent, "qual32-run-collision", "",
		map[string]any{"work_item_id": uuid.NewString(), "source": "pi", "external_run_id": "run-qual32-a", "host": "isolated-pi"}); s != 404 {
		// No existing WorkItem may be forged; hides it before collision.
		t.Fatalf("forged WorkItem must be 404: %d", s)
	}
	otherWorkStatus, otherWork, _ := request(t, "POST", f.url(f.project), f.agent, "qual32-other-work", "",
		map[string]any{"kind": "task", "title": "Other real synthetic WorkItem"})
	if otherWorkStatus != 201 {
		t.Fatal(otherWork)
	}
	if s, _, _ := request(t, "POST", registerURL, f.agent, "qual32-rebind-work", "",
		map[string]any{"work_item_id": otherWork["id"], "source": "pi", "external_run_id": "run-qual32-a", "host": "isolated-pi"}); s != 409 {
		t.Fatalf("source run ID may not be rebound: %d", s)
	}
	if s, _, _ := request(t, "POST", registerURL, f.agent, "qual32-forged-principal", "",
		map[string]any{"work_item_id": workID, "source": "pi", "external_run_id": "forged", "host": "pi",
			"reporter_principal_id": uuid.NewString()}); s != 400 {
		t.Fatalf("client-supplied authentication metadata must be rejected: %d", s)
	}
	detailURL := base + "/agent-runs/" + runID
	reportsURL := detailURL + "/reports"
	stamp := time.Now().UTC().Add(-time.Minute)
	report := func(key, sourceID, kind, status, etag string, when time.Time) (int, map[string]any) {
		t.Helper()
		data := map[string]any{"source_report_id": sourceID, "kind": kind, "occurred_at": when}
		if status != "" {
			data["status"] = status
		}
		code, body, _ := request(t, "POST", reportsURL, f.agent, key, etag, data)
		return code, body
	}
	s, receipt := report("qual32-running", "status-running", "status", "running", `"1"`, stamp)
	if s != 201 || receipt["run"].(map[string]any)["version"] != float64(2) {
		t.Fatalf("running status=%d %v", s, receipt)
	}
	s, duplicate := report("qual32-running", "status-running", "status", "running", `"1"`, stamp)
	if s != 201 || duplicate["report"].(map[string]any)["id"] != receipt["report"].(map[string]any)["id"] {
		t.Fatalf("source report must be replayable with stale If-Match: %d", s)
	}
	s, duplicate = report("qual32-running-new-key", "status-running", "status", "running", `"1"`, stamp)
	if s != 201 || duplicate["report"].(map[string]any)["id"] != receipt["report"].(map[string]any)["id"] {
		t.Fatalf("same report ID must dedup across keys: %d %v", s, duplicate)
	}
	if s, _, _ := request(t, "POST", reportsURL, f.agent, "qual32-report-tamper", `"2"`, map[string]any{
		"source_report_id": "status-running", "kind": "status", "status": "failed", "occurred_at": stamp,
	}); s != 409 {
		t.Fatalf("source report digest must conflict: %d", s)
	}
	if s, _ := report("qual32-stale-cas", "new-running", "status", "blocked", `"1"`, stamp.Add(time.Second)); s != 409 {
		t.Fatalf("stale status CAS must be 409: %d", s)
	}
	if s, observed := report("qual32-out-of-order", "status-old", "status", "queued", `"2"`, stamp.Add(-time.Second)); s != 201 ||
		observed["report"].(map[string]any)["applied"] != false ||
		observed["run"].(map[string]any)["source_status"] != "running" {
		t.Fatalf("late older status must be archived but not projected: %d %v", s, observed)
	}
	// Heartbeat is a source fact but not an acceptance, review or test. A
	// second distinct heartbeat within the same minute is rejected.
	s, _ = report("qual32-heartbeat-1", "hb-1", "heartbeat", "", `"2"`, stamp.Add(3*time.Second))
	if s != 201 {
		t.Fatalf("first heartbeat=%d", s)
	}
	var auditBefore, outboxBefore int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2`, f.project, runID).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2`, f.project, runID).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	if s, _ := report("qual32-heartbeat-2", "hb-2", "heartbeat", "", `"2"`, stamp.Add(4*time.Second)); s != 429 {
		t.Fatalf("heartbeat throttle=%d", s)
	}
	if s, _ := report("qual32-heartbeat-replay", "hb-1", "heartbeat", "", `"2"`, stamp.Add(3*time.Second)); s != 201 {
		t.Fatalf("accepted heartbeat must be deduplicated despite rate limit: %d", s)
	}
	var auditAfter, outboxAfter int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2`, f.project, runID).Scan(&auditAfter)
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2`, f.project, runID).Scan(&outboxAfter)
	if auditBefore != auditAfter || outboxBefore != outboxAfter {
		t.Fatalf("heartbeat fabricated audit/outbox %d/%d -> %d/%d", auditBefore, outboxBefore, auditAfter, outboxAfter)
	}

	// Evidence must be a REAL Ready artifact or existing same-project HTTPS
	// reference; missing or guessed IDs are hidden as 404.
	if s, _, _ := request(t, "POST", reportsURL, f.agent, "qual32-bad-evidence", `"2"`, map[string]any{
		"source_report_id": "bad-evidence", "kind": "status", "status": "blocked",
		"occurred_at": stamp.Add(5 * time.Second), "evidence_artifact_id": uuid.NewString(),
	}); s != 404 {
		t.Fatalf("forged evidence=%d", s)
	}

	// Reference a real same-project HTTPS provenance record. The run reports
	// its source evidence but does not turn it into a TestAttempt pass.
	refStatus, reference, _ := request(t, "POST", base+"/external-references", f.agent,
		"qual32-provenance-reference", "", map[string]any{
			"target_kind": "work_item", "target_id": workID, "provider": "git",
			"kind": "commit", "external_id": "qual32-provenance-1",
			"url": "https://example.org/qual/commit/qual32-provenance-1",
		})
	if refStatus != 201 {
		t.Fatalf("real reference creation=%d %v", refStatus, reference)
	}
	s, receipt, _ = request(t, "POST", reportsURL, f.agent, "qual32-blocked",
		strconv.Quote("2"), map[string]any{
			"source_report_id": "status-blocked", "kind": "status", "status": "blocked",
			"occurred_at": stamp.Add(5 * time.Second), "error_reason": "waiting for external input",
			"external_reference_id": reference["id"],
		})
	if s != 201 || receipt["run"].(map[string]any)["version"] != float64(3) ||
		receipt["report"].(map[string]any)["external_reference_id"] != reference["id"] {
		t.Fatalf("blocked with source evidence=%d %v", s, receipt)
	}
	s, receipt = report("qual32-succeeded", "status-succeeded", "status", "succeeded", `"3"`, stamp.Add(10*time.Second))
	if s != 201 || receipt["run"].(map[string]any)["source_status"] != "succeeded" {
		t.Fatalf("external terminal=%d %v", s, receipt)
	}
	if s, _ := report("qual32-regression", "status-regression", "status", "running", `"4"`, stamp.Add(11*time.Second)); s != 409 {
		t.Fatalf("terminal regression must be blocked: %d", s)
	}
	if s, receipt = report("qual32-older-after-done", "status-older", "status", "running", `"4"`, stamp.Add(2*time.Second)); s != 201 ||
		receipt["run"].(map[string]any)["source_status"] != "succeeded" {
		t.Fatalf("late report changed terminal status: %d %v", s, receipt)
	}
	if s, item, _ := request(t, "GET", f.url(f.project)+"/"+workID, f.admin, "", "", nil); s != 200 || item["status"] != "backlog" {
		t.Fatalf("external succeeded illegally changed WorkItem: %d %v", s, item)
	}
	if s, runs, _ := request(t, "GET", f.url(f.project)+"/"+workID+"/agent-runs", f.viewer, "", "", nil); s != 200 ||
		len(runs["items"].([]any)) != 1 {
		t.Fatalf("scoped WorkItem run list %d %v", s, runs)
	}
	if s, history, _ := request(t, "GET", reportsURL+"?limit=2", f.viewer, "", "", nil); s != 200 ||
		len(history["items"].([]any)) != 2 || history["next_before"] == nil {
		t.Fatalf("bounded run history=%d %v", s, history)
	}
	// A WorkItem reader without artifact:read may list Run reports, but not
	// infer private evidence identity from the report projection.
	viewerHash, err := identity.HashToken(f.viewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET scopes=array_remove(scopes,'artifact:read') WHERE token_sha256=$1", viewerHash[:]); err != nil {
		t.Fatal(err)
	}
	s, redacted, _ := request(t, "GET", reportsURL+"?limit=30", f.viewer, "", "", nil)
	if s != 200 {
		t.Fatalf("read without artifact scope=%d %v", s, redacted)
	}
	sawRedactedEvidence := false
	for _, raw := range redacted["items"].([]any) {
		entry := raw.(map[string]any)
		if entry["source_report_id"] == "status-blocked" {
			sawRedactedEvidence = true
			if _, leaked := entry["external_reference_id"]; leaked {
				t.Fatalf("private reference ID leaked without artifact:read: %v", entry)
			}
		}
	}
	if !sawRedactedEvidence {
		t.Fatal("source evidence report missing from restricted history")
	}
	// Another explicitly authorized real Principal cannot impersonate the
	// run's creator, even by guessing the source run UUID.
	grant(f.agent2, "qual32-grant-agentB")
	if s, _, _ := request(t, "POST", reportsURL, f.agent2, "qual32-impostor", `"4"`, map[string]any{
		"source_report_id": "impostor", "kind": "status", "status": "failed",
		"occurred_at": stamp.Add(12 * time.Second),
	}); s != 404 {
		t.Fatalf("other actor could report run: %d", s)
	}
	if s, _, _ := request(t, "GET", fmt.Sprintf("%s/api/v1/projects/%s/agent-runs/%s", f.server.URL, f.otherProject, runID),
		f.agent, "", "", nil); s != 404 {
		t.Fatalf("cross-project Run access=%d", s)
	}
	// The reporting permission cannot be used as an evidence existence oracle.
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET scopes=array_remove(scopes,'artifact:read') WHERE id=$1", f.agentCredential); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request(t, "POST", reportsURL, f.agent, "qual32-private-evidence-scope", strconv.Quote("4"),
		map[string]any{
			"source_report_id": "private-evidence", "kind": "status", "status": "succeeded",
			"occurred_at": stamp.Add(14 * time.Second), "evidence_artifact_id": uuid.NewString(),
		}); status != 403 {
		t.Fatalf("private evidence metadata disclosed without artifact:read: %d", status)
	}
	var attempts, persistedReports int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.test_attempts WHERE project_id=$1", f.project).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("external succeeded forged TestAttempt: count=%d error=%v", attempts, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.agent_run_reports WHERE project_id=$1 AND agent_run_id=$2", f.project, runID).Scan(&persistedReports); err != nil || persistedReports < 6 {
		t.Fatalf("append-only report history missing: count=%d error=%v", persistedReports, err)
	}
	// Real-time credential revocation must precede any idempotent replay.
	if _, err := f.pool.Exec(ctx, `UPDATE qual.credentials SET revoked_at=now() WHERE id=$1`, f.agentCredential); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := request(t, "POST", reportsURL, f.agent, "qual32-running", `"1"`, map[string]any{
		"source_report_id": "status-running", "kind": "status", "status": "running", "occurred_at": stamp,
	}); s != 401 {
		t.Fatalf("revoked token replay succeeded: %d", s)
	}
}

// A per-Run row lock and current-version check ensure two concurrent status
// reports cannot both claim the same transition.
func TestAgentRunConcurrentCAS(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_") {
		t.Skip("requires disposable db")
	}
	f := setup(t)
	// Admin's normal credential is deliberately not endowed with run:report;
	// use an isolated Agent fixture and explicit permission grant via SQL
	// only for this synthetic test setup, not for production principals.
	digest, _ := identity.HashToken(f.agent)
	var actor, ws uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT principal_id,workspace_id FROM qual.credentials WHERE token_sha256=$1`, digest[:]).Scan(&actor, &ws); err != nil {
		t.Fatal(err)
	}
	_, err := f.pool.Exec(context.Background(), `UPDATE qual.credentials SET scopes=array_append(scopes,'run:report') WHERE token_sha256=$1`, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(context.Background(), `INSERT INTO qual.project_permission_overrides
      (workspace_id,project_id,principal_id,permission,effect,version,changed_by)
      VALUES($1,$2,$3,'run:report','allow',1,$3)`, ws, f.project, actor)
	if err != nil {
		t.Fatal(err)
	}
	work := create(t, f, f.agent, "qual32-cas-work")
	base := fmt.Sprintf("%s/api/v1/projects/%s/agent-runs", f.server.URL, f.project)
	// Two different request keys racing for the same project/source/external
	// run identity must both resolve to one durable Run, not SQL error 500.
	type registrationResult struct {
		status int
		id     string
	}
	var registerWG sync.WaitGroup
	registrationResults := make(chan registrationResult, 2)
	for n := 0; n < 2; n++ {
		registerWG.Add(1)
		go func(n int) {
			defer registerWG.Done()
			status, body, _ := request(t, "POST", base, f.agent,
				fmt.Sprintf("qual32-concurrent-register-%d", n), "",
				map[string]any{"work_item_id": work["id"], "source": "pi",
					"external_run_id": "same-natural-key", "host": "pi"})
			id, _ := body["id"].(string)
			registrationResults <- registrationResult{status, id}
		}(n)
	}
	registerWG.Wait()
	close(registrationResults)
	originalID := ""
	for result := range registrationResults {
		if result.status != 201 || result.id == "" {
			t.Fatalf("concurrent registration: %+v", result)
		}
		if originalID != "" && result.id != originalID {
			t.Fatalf("natural key duplicated: %q vs %q", originalID, result.id)
		}
		originalID = result.id
	}
	s, run, _ := request(t, "POST", base, f.agent, "qual32-cas-register", "",
		map[string]any{"work_item_id": work["id"], "source": "pi", "external_run_id": "race", "host": "pi"})
	if s != 201 {
		t.Fatalf("%d %v", s, run)
	}
	url := base + "/" + run["id"].(string) + "/reports"
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			code, _, _ := request(t, "POST", url, f.agent, fmt.Sprintf("qual32-race-%d", n), `"1"`,
				map[string]any{"source_report_id": fmt.Sprintf("source-%d", n), "kind": "status",
					"status": "running", "occurred_at": time.Now().UTC()})
			codes <- code
		}(i)
	}
	wg.Wait()
	close(codes)
	passed, conflicted := 0, 0
	for s := range codes {
		if s == 201 {
			passed++
		} else if s == 409 {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrency status: %d", s)
		}
	}
	if passed != 1 || conflicted != 1 {
		t.Fatalf("CAS results success=%d conflict=%d", passed, conflicted)
	}
}
