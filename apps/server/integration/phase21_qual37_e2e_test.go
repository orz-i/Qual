//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identity "qual/internal/identity/application"
)

// An end-to-end pilot with actual disposable PostgreSQL, S3 and independent
// Qual CLI processes. The delivery and Agent host are SYNTHETIC, not GitHub.
func TestQual37CrossContextResumeAndEvidence(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_ci_") {
		t.Skip("requires a newly created, disposable qual32_run_ci_* Goose17 database")
	}
	f := setup(t)
	if f.objects == nil {
		t.Fatal("real isolated S3 is required")
	}
	ctx := context.Background()
	base := fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
	w := create(t, f, f.agent, "qual37-real-persistence-fixture")
	id := w["id"].(string)
	for i, status := range []string{"todo", "in_progress"} {
		s, x, _ := request(t, "POST", base+"/work-items/"+id+"/transitions", f.agent,
			fmt.Sprintf("qual37-status-%d", i), "",
			map[string]any{"status": status, "expected_version": i + 1})
		if s != 200 || x["status"] != status {
			t.Fatalf("WorkItem transition %d: %d %v", i, s, x)
		}
	}
	s, linked, _ := request(t, "POST", threadBase(f, id), f.agent, "qual37-thread", "",
		threadInput("example/project/pull/91", 3))
	if s != 201 || linked["work_item_version"] != float64(4) {
		t.Fatalf("Thread CAS failed %d %v", s, linked)
	}
	thread := linked["thread"].(map[string]any)["id"].(string)
	sum := sha256.Sum256([]byte("Synthetic delivery for scoped integration"))
	event := map[string]any{
		"work_item_id": id, "thread_id": thread, "source": "github",
		"external_event_id": "qual37-test-delivery", "event_type": "needs_input",
		"occurred_at":    time.Now().UTC().Add(-time.Minute),
		"payload_sha256": hex.EncodeToString(sum[:]),
		"summary":        "Synthetic input needed; not a verified external Actor",
	}
	runInput := map[string]any{
		"work_item_id": id, "source": "pi", "external_run_id": "qual37-run",
		"host": "isolated-pi",
	}
	eventURL, runURL := base+"/external-events", base+"/agent-runs"
	if s, _, _ := request(t, "POST", eventURL, f.agent, "qual37-ungranted-event", "", event); s != 403 {
		t.Fatalf("work:write implicitly permitted event registration %d", s)
	}
	if s, _, _ := request(t, "POST", runURL, f.agent, "qual37-ungranted-run", "", runInput); s != 403 {
		t.Fatalf("work:write implicitly permitted run registration %d", s)
	}
	digest, err := identity.HashToken(f.agent)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, "UPDATE qual.credentials SET scopes=array_append(array_append(scopes,'event:write'),'run:report') WHERE token_sha256=$1", digest[:])
	if err != nil {
		t.Fatal(err)
	}
	principal := principalFor(t, f, f.agent)
	for _, permission := range []string{"event:write", "run:report"} {
		s, x, _ := request(t, "PUT", base+"/access/"+principal.String()+"/permissions/"+permission, f.admin,
			"qual37-allow-"+permission, "", map[string]any{
				"effect": "allow", "expected_version": 0, "reason": "Synthetic isolated E2E grant, never a production permission",
			})
		if s != 200 {
			t.Fatalf("grant %s failed %d %v", permission, s, x)
		}
	}
	s, received, _ := request(t, "POST", eventURL, f.agent, "qual37-event-first", "", event)
	if s != 201 {
		t.Fatalf("Event record %d %v", s, received)
	}
	for _, key := range []string{"qual37-event-first", "qual37-event-other-key"} {
		code, again, _ := request(t, "POST", eventURL, f.agent, key, "", event)
		if code != 201 || again["id"] != received["id"] {
			t.Fatalf("Event replay %d %v", code, again)
		}
	}
	changed := map[string]any{}
	for k, v := range event {
		changed[k] = v
	}
	changed["payload_sha256"] = strings.Repeat("f", 64)
	if code, _, _ := request(t, "POST", eventURL, f.agent, "qual37-event-conflict", "", changed); code != 409 {
		t.Fatalf("conflicting source Event not rejected %d", code)
	}
	s, inbox, _ := request(t, "GET", base+"/attention", f.viewer, "", "", nil)
	if s != 200 || len(inbox["items"].([]any)) != 1 ||
		inbox["items"].([]any)[0].(map[string]any)["reason"] != "event_needs_input" {
		t.Fatalf("Event clue missing from read model %d %v", s, inbox)
	}
	s, run, _ := request(t, "POST", runURL, f.agent, "qual37-run-register", "", runInput)
	if s != 201 {
		t.Fatalf("AgentRun %d %v", s, run)
	}
	reports := runURL + "/" + run["id"].(string) + "/reports"
	stamp := time.Now().UTC().Add(-time.Minute)
	report := map[string]any{
		"source_report_id": "qual37-blocked-1", "kind": "status", "status": "blocked",
		"occurred_at": stamp, "error_reason": "External fixture host needs input",
	}
	s, r1, _ := request(t, "POST", reports, f.agent, "qual37-run-blocked", `"1"`, report)
	if s != 201 || r1["run"].(map[string]any)["source_status"] != "blocked" {
		t.Fatalf("Run report %d %v", s, r1)
	}
	s, r2, _ := request(t, "POST", reports, f.agent, "qual37-run-repeat", `"1"`, report)
	if s != 201 || r2["report"].(map[string]any)["id"] != r1["report"].(map[string]any)["id"] {
		t.Fatalf("Run replay created duplicate %d %v", s, r2)
	}
	s, hint, _ := request(t, "GET", base+"/attention", f.viewer, "", "", nil)
	if s != 200 || len(hint["items"].([]any)) != 1 ||
		hint["items"].([]any)[0].(map[string]any)["reason"] != "run_blocked" ||
		hint["items"].([]any)[0].(map[string]any)["hint_only"] != true {
		t.Fatalf("Run priority over Event must be a nonauthoritative hint %d %v", s, hint)
	}
	if code, _, _ := request(t, "GET", base+"/audit", f.viewer, "", "", nil); code != 403 {
		t.Fatalf("work:read leaked audit:read: %d", code)
	}
	if code, _, _ := request(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/attention", f.agent, "", "", nil); code != 404 {
		t.Fatalf("foreign project Attention leaked %d", code)
	}
	caseID, _ := mustCase(t, f, "qual37-case", "Actually reported immutable fixture attempts")
	versionID, _ := publishCase(t, f, caseID, "qual37-case-publish", 1)
	testRun := mustRun(t, f, []string{versionID}, "qual37-test-run")
	runItemID := testRun["items"].([]any)[0].(map[string]any)["id"].(string)
	failed := mustAttempt(t, f, runItemID, f.agent, "qual37-failed", "failed")
	passed := mustAttempt(t, f, runItemID, f.agent, "qual37-passed", "passed")
	if failed["id"] == passed["id"] {
		t.Fatal("Failed Attempt was overwritten")
	}
	if status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		"qual37-case-coverage", "", map[string]any{"work_item_id": id}); status != 201 {
		t.Fatalf("coverage failed %d", status)
	}
	payload := []byte("Actual isolated S3 evidence for immutable attempt history\n")
	artifactID, artifactHash := testUploadArtifact(t, f, id, payload)
	artifactURL := base + "/artifacts/" + artifactID + "/content"
	if code, bytesRead := rawHTTP(t, "GET", artifactURL, f.viewer, "", nil, ""); code != 200 ||
		!bytes.Equal(bytesRead, payload) {
		t.Fatalf("Ready Artifact content %d", code)
	}
	sha := sha256.Sum256(payload)
	if artifactHash != hex.EncodeToString(sha[:]) {
		t.Fatal("real Artifact SHA differs")
	}
	var objectKey string
	if err := f.pool.QueryRow(ctx, "SELECT object_key FROM qual.artifacts WHERE id=$1 AND project_id=$2",
		artifactID, f.project).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Put(ctx, objectKey, "text/plain", []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if code, _ := rawHTTP(t, "GET", artifactURL, f.viewer, "", nil, ""); code != 422 {
		t.Fatalf("tampered S3 accepted %d", code)
	}
	if err := f.objects.Put(ctx, objectKey, "text/plain", payload); err != nil {
		t.Fatal(err)
	}

	// Each invocation launches a new Qual CLI process; no chat context is
	// reused. Only persisted server WorkItem/Test/Thread facts are queried.
	bin := filepath.Join(t.TempDir(), "qual")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/qual").CombinedOutput(); err != nil {
		t.Fatalf("CLI build failed: %v (%d bytes redacted)", err, len(out))
	}
	resume := func(token string) (int, map[string]any) {
		t.Helper()
		cmd := exec.Command(bin, "resume", "-project", f.project.String(), "-item", id)
		env := []string{}
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "QUAL_TOKEN=") && !strings.HasPrefix(v, "QUAL_API_URL=") {
				env = append(env, v)
			}
		}
		cmd.Env = append(env, "QUAL_TOKEN="+token, "QUAL_API_URL="+f.server.URL)
		result, err := cmd.Output()
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				return e.ExitCode(), nil
			}
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(result, &data); err != nil {
			t.Fatal("invalid CLI JSON")
		}
		return 0, data
	}
	code, one := resume(f.agent)
	if code != 0 || one["schema_version"] != float64(2) || one["context_revision"] != float64(4) {
		t.Fatalf("first CLI process did not recover WorkItem v4: %d %v", code, one["context_revision"])
	}
	a := one["sections"].(map[string]any)["attempts"].(map[string]any)["items"].([]any)
	if len(a) != 2 {
		t.Fatalf("CLI did not resume both actual Attempts: %v", a)
	}
	if s, _, _ := request(t, "POST", base+"/work-items/"+id+"/comments", f.agent, "qual37-stale-cas", "",
		map[string]any{"body": "Do not commit outdated data", "expected_version": 3}); s != 409 {
		t.Fatalf("stale WorkItem CAS accepted %d", s)
	}
	s, updated, _ := request(t, "POST", base+"/work-items/"+id+"/comments", f.agent, "qual37-new-version", "",
		map[string]any{"body": "Versioned work after independent CLI recovery", "expected_version": 4})
	if s != 201 || updated["version"] != float64(5) {
		t.Fatalf("WorkItem version did not bump %d %v", s, updated)
	}
	code, two := resume(f.agent)
	if code != 0 || two["context_revision"] != float64(5) ||
		len(two["sections"].(map[string]any)["attempts"].(map[string]any)["items"].([]any)) != 2 {
		t.Fatalf("new CLI process lost version/history: exit%d, revision%v", code, two["context_revision"])
	}
	own := create(t, f, f.admin, "qual37-self-review-marker")
	s, role, _ := request(t, "POST", roleEndpoint(f, own["id"].(string)), f.admin,
		"qual37-self-review", "", roleInput("reviewer", "observed", "none", "",
			"Human observed own fixture, not an approval", 1))
	if s != 201 || role["self_review"] != true {
		t.Fatalf("real self_review marker not retained %d %v", s, role)
	}
	if s, current, _ := request(t, "GET", base+"/work-items/"+id, f.agent, "", "", nil); s != 200 || current["status"] != "in_progress" || current["version"] != float64(5) {
		t.Fatalf("external Run/Event improperly changed task %d %v", s, current)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE id=$1", f.agentCredential); err != nil {
		t.Fatal(err)
	}
	if code, _ := resume(f.agent); code != 3 {
		t.Fatalf("revoked new CLI retained Agent rights: %d", code)
	}
	if s, _, _ := request(t, "POST", runURL, f.agent, "qual37-run-after-revoke", "", runInput); s != 401 {
		t.Fatalf("revoked AgentRun reporter accepted %d", s)
	}
}
