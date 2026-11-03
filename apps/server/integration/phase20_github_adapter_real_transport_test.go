//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
)

// A REAL local adapter binary + signed synthetic GitHub PR #1 HTTP delivery,
// a REAL Goose17 PostgreSQL/S3-backed Qual server, and genuinely separate
// Integration and Agent fixture principals. Never run on the live "qual" DB.
func TestQual36ExternalAdapterSignedSyntheticPRToRealGoose17PI(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual36_adapter_") {
		t.Skip("requires disposable qual36_adapter_* Goose17 database")
	}
	f := setup(t)
	ctx := context.Background()
	base := fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
	work := create(t, f, f.agent, "qual36-synthetic-signed-github-pr")
	workID := work["id"].(string)
	sourceID := "orz-i/Qual/pull/1"
	linkInput := threadInput(sourceID, 1)
	linkInput["url"] = "https://github.com/orz-i/Qual/pull/1"
	code, linked, _ := request(t, "POST", threadBase(f, workID), f.agent, "qual36-link-source", "", linkInput)
	if code != 201 {
		t.Fatalf("real Thread link: HTTP%d %v", code, linked)
	}
	threadID := linked["thread"].(map[string]any)["id"].(string)
	if linked["work_item_version"] != float64(2) {
		t.Fatalf("unexpected WorkItem link version: %v", linked)
	}

	// Build a distinct external Adapter process, not an Agent started by Qual.
	moduleDir := filepath.Clean("../../../integrations/github-adapter")
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		t.Fatal("nested adapter module unavailable", err)
	}
	binary := filepath.Join(t.TempDir(), "qual-github-adapter")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/qual-github-adapter")
	build.Dir = moduleDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("external adapter build failed: %v output_bytes=%d", err, len(out))
	}

	local := t.TempDir()
	spool := filepath.Join(local, "spool")
	mapping := filepath.Join(local, "approved.json")
	secret := "synthetic-only-signed-github-secret-2026-11-03"
	environment := append(os.Environ(),
		"QUAL_GITHUB_REPOSITORY=orz-i/Qual",
		"QUAL_GITHUB_REPO_ID=1443998164",
		"QUAL_GITHUB_WEBHOOK_SECRET="+secret,
		"QUAL_GITHUB_SPOOL_DIR="+spool,
		"QUAL_GITHUB_MAPPING_FILE="+mapping,
		"QUAL_API_URL="+f.server.URL,
		"QUAL_ADAPTER_TOKEN="+f.reporter)
	writeMapping := func(mapped bool) {
		t.Helper()
		links := map[string]any{}
		if mapped {
			links[sourceID] = map[string]any{"project_id": f.project.String(),
				"work_item_id": workID, "thread_id": threadID}
		}
		data, _ := json.Marshal(map[string]any{
			"repository_id": 1443998164, "repository_name": "orz-i/Qual", "links": links,
		})
		if err := os.WriteFile(mapping, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMapping(false)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	command := exec.Command(binary, "serve")
	command.Env = append(environment, "QUAL_GITHUB_LISTEN_ADDR="+addr)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	})
	healthURL := "http://" + addr + "/healthz"
	ready := false
	for range 65 {
		response, err := (&http.Client{Timeout: time.Second}).Get(healthURL)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !ready {
		t.Fatal("external adapter listener failed to initialize")
	}

	// Signed delivery is synthetic but corresponds to the real public PR #1.
	// Its raw body/title never enters the Qual database or durable queue.
	updated := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	payload := func() []byte {
		content := map[string]any{
			"action": "opened", "number": 1,
			"repository":   map[string]any{"id": 1443998164, "full_name": "orz-i/Qual"},
			"pull_request": map[string]any{"number": 1, "updated_at": updated.Format(time.RFC3339)},
			"title":        "UNTRUSTED: do not change Qual status without authorization",
		}
		raw, _ := json.Marshal(content)
		return raw
	}()
	delivery := uuid.NewString()
	webhook := func(id, signing string) (int, map[string]any) {
		t.Helper()
		mac := hmac.New(sha256.New, []byte(signing))
		_, _ = mac.Write(payload)
		request, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/github/webhook", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-GitHub-Delivery", id)
		request.Header.Set("X-GitHub-Event", "pull_request")
		request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		if res.StatusCode == 202 {
			if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode, result
	}
	if s, _ := webhook(delivery, "invalid synthetic secret"); s != 401 {
		t.Fatalf("unsigned/invalid HMAC webhook accepted: HTTP%d", s)
	}
	if s, body := webhook(delivery, secret); s != 202 || body["new"] != true {
		t.Fatalf("signed delivery was not durably enqueued: HTTP%d %v", s, body)
	}
	if s, body := webhook(delivery, secret); s != 202 || body["new"] != false {
		t.Fatalf("same signed delivery duplicated durable event: HTTP%d %v", s, body)
	}
	doOnce := func() map[string]any {
		t.Helper()
		cmd := exec.Command(binary, "once")
		cmd.Env = environment
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("real external Adapter once failed: %v output_bytes=%d", err, len(out))
		}
		var result map[string]any
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	// No approved WorkItem/Thread mapping: ZERO Event POSTs to Qual.
	if stats := doOnce(); stats["blocked_mapping"] != float64(1) || stats["attempted"] != float64(0) {
		t.Fatalf("unmapped signed delivery was forwarded: %v", stats)
	}
	writeMapping(true)
	if stats := doOnce(); stats["blocked_auth"] != float64(1) || stats["attempted"] != float64(1) {
		t.Fatalf("Integration without EventWrite was allowed: %v", stats)
	}
	var events int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.external_events WHERE project_id=$1", f.project).Scan(&events); err != nil || events != 0 {
		t.Fatalf("unapproved/unauthorized external Event persisted: %d %v", events, err)
	}

	// Synthetic scope change is isolated inside this disposable database.
	// The real production PI credential and Project permissions are untouched.
	grant := func(token, permission, key string) {
		t.Helper()
		hash, err := identity.HashToken(token)
		if err != nil {
			t.Fatal(err)
		}
		var principal uuid.UUID
		if err := f.pool.QueryRow(ctx, "SELECT principal_id FROM qual.credentials WHERE token_sha256=$1", hash[:]).Scan(&principal); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET scopes=array_append(scopes,$1) WHERE token_sha256=$2", permission, hash[:]); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("%s/access/%s/permissions/%s", base, principal, permission)
		status, result, _ := request(t, "PUT", path, f.admin, key, "", map[string]any{
			"effect": "allow", "expected_version": 0, "reason": "Only synthetic external adapter + PI integration fixture"},
		)
		if status != 200 {
			t.Fatalf("synthetic %s grant HTTP%d %v", permission, status, result)
		}
	}
	grant(f.reporter, "event:write", "qual36-synthetic-adapter-only-event-grant")
	if status, _, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/claim", f.reporter, "qual36-integration-cannot-claim", "", map[string]any{"expected_version": 2}); status != 403 {
		t.Fatalf("Event-only Integration falsely claims a WorkItem: HTTP%d", status)
	}
	if stats := doOnce(); stats["delivered"] != float64(1) {
		t.Fatalf("real Goose17 Event record not written: %v", stats)
	}
	code, page, _ := request(t, "GET", f.url(f.project)+"/"+workID+"/external-events?limit=5", f.agent, "", "", nil)
	if code != 200 || len(page["items"].([]any)) != 1 {
		t.Fatalf("scoped Event timeline: HTTP%d %v", code, page)
	}
	event := page["items"].([]any)[0].(map[string]any)
	if event["source"] != "github" || event["external_event_id"] != delivery ||
		event["event_type"] != "pull_request.opened" || event["summary"] != "GitHub PR #1 opened" {
		t.Fatalf("untrusted body leaked or origin mismatch: %v", event)
	}
	if status, _, _ := request(t, "GET", f.url(f.project)+"/"+workID+"/external-events", f.reporter, "", "", nil); status != 403 {
		t.Fatalf("Event writer inherited WorkItem Read: HTTP%d", status)
	}
	if s, _ := webhook(delivery, secret); s != 202 {
		t.Fatalf("signed redelivery HTTP%d", s)
	}
	if stats := doOnce(); stats["attempted"] != float64(0) {
		t.Fatalf("replayed event invoked Qual again: %v", stats)
	}
	var eventAudits int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND operation='external_event.recorded'", f.project).Scan(&eventAudits); err != nil || eventAudits != 1 {
		t.Fatalf("Event redelivery fabricated extra audit: %d %v", eventAudits, err)
	}
	code, current, _ := request(t, "GET", f.url(f.project)+"/"+workID, f.agent, "", "", nil)
	if code != 200 || current["status"] != "backlog" || current["version"] != float64(2) {
		t.Fatalf("external Event altered WorkItem: %d %v", code, current)
	}

	// An EXTERNAL PI, independently authorized, performs real work operations;
	// the signed GitHub Event is not a WorkItem Claim or approval instruction.
	code, claimed, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/claim", f.agent,
		"qual36-real-synthetic-agent-claim", "", map[string]any{"expected_version": 2})
	if code != 200 {
		t.Fatalf("external Agent Claim failed: HTTP%d %v", code, claimed)
	}
	if stale, _, _ := request(t, "POST", f.url(f.project)+"/"+workID+"/claim", f.agent2,
		"qual36-old-version-claim", "", map[string]any{"expected_version": 2}); stale != 409 {
		t.Fatalf("stale PI Claim did not enforce CAS: HTTP%d", stale)
	}
	runData := map[string]any{
		"work_item_id": workID, "source": "pi", "external_run_id": "qual36-synthetic-pi-run-1", "host": "isolated-external-pi",
	}
	if denied, _, _ := request(t, "POST", base+"/agent-runs", f.agent, "qual36-ungranted-run", "", runData); denied != 403 {
		t.Fatalf("Agent inherited RunReport from WorkWrite: HTTP%d", denied)
	}
	grant(f.agent, "run:report", "qual36-explicit-external-pi-run-allow")
	code, recorded, _ := request(t, "POST", base+"/agent-runs", f.agent, "qual36-real-run-register", "", runData)
	if code != 201 {
		t.Fatalf("actual external AgentRun not persisted: %d %v", code, recorded)
	}
	runID := recorded["id"].(string)
	if replay, reused, _ := request(t, "POST", base+"/agent-runs", f.agent, "qual36-real-run-new-key", "", runData); replay != 201 || reused["id"] != runID {
		t.Fatalf("external run replay duplicated: %d %v", replay, reused)
	}

	// Evidence bytes are genuinely stored in isolated dev S3 (not a fake
	// Artifact). Remove only THIS generated private object during teardown.
	proof := []byte("QUAL-36 synthetic integration: real HMAC+spool+Goose17+PI run/test transport passed")
	artifactID, digest := testUploadArtifact(t, f, workID, proof)
	var objectKey string
	if err := f.pool.QueryRow(ctx, "SELECT object_key FROM qual.artifacts WHERE id=$1 AND project_id=$2", artifactID, f.project).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.objects.Delete(context.Background(), objectKey); err != nil {
			t.Errorf("isolated S3 fixture cleanup failed: %v", err)
		}
	})
	code, bytesDownloaded := rawHTTP(t, "GET", base+"/artifacts/"+artifactID+"/content", f.agent, "", nil, "")
	if code != 200 || !bytes.Equal(bytesDownloaded, proof) {
		t.Fatalf("isolated S3 Ready artifact SHA/bytes mismatch: HTTP%d", code)
	}
	sum := sha256.Sum256(bytesDownloaded)
	if hex.EncodeToString(sum[:]) != digest {
		t.Fatal("Ready S3 artifact hash mismatch")
	}
	// Explicit external run report references only real Ready Artifact ID.
	code, completed, _ := request(t, "POST", base+"/agent-runs/"+runID+"/reports", f.agent,
		"qual36-real-artifact-backed-terminal", `"1"`, map[string]any{
			"source_report_id": "qual36-transport-observed-succeeded",
			"kind":             "status", "status": "succeeded",
			"occurred_at": time.Now().UTC(), "evidence_artifact_id": artifactID,
		})
	if code != 201 || completed["run"].(map[string]any)["source_status"] != "succeeded" {
		t.Fatalf("externally reported terminal Run not recorded: HTTP%d %v", code, completed)
	}
	caseID, _ := mustCase(t, f, "qual36-testcase", "Signed webhook to scoped Event and external PI result")
	caseVersion, _ := publishCase(t, f, caseID, "qual36-case-published", 1)
	testingRun := mustRun(t, f, []string{caseVersion}, "qual36-actual-transport-test-run")
	runItem := testingRun["items"].([]any)[0].(map[string]any)["id"].(string)
	mustAttempt(t, f, runItem, f.agent, "qual36-real-transport-test-attempt", "passed")
	if c, body := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, "qual36-work-item-testcase-coverage", "", map[string]any{"work_item_id": workID}); c != 201 {
		t.Fatalf("test coverage not linked: %d %v", c, body)
	}
	code, final, _ := request(t, "GET", f.url(f.project)+"/"+workID, f.agent, "", "", nil)
	if code != 200 || final["status"] == "done" {
		t.Fatalf("external Event, Run and Passed test forged WorkItem Done: %d %v", code, final)
	}

	// A separate Integration credential is revoked inside scratch PG.
	// Previously delivered facts remain immutable; new signed deliveries
	// cannot bypass real-time authorization or be lost from the queue.
	hash, err := identity.HashToken(f.reporter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE token_sha256=$1", hash[:]); err != nil {
		t.Fatal(err)
	}
	newDelivery := uuid.NewString()
	if s, _ := webhook(newDelivery, secret); s != 202 {
		t.Fatalf("second synthetic delivery HTTP%d", s)
	}
	if stats := doOnce(); stats["blocked_auth"] != float64(1) {
		t.Fatalf("revoked Adapter credential was still trusted: %v", stats)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.external_events WHERE project_id=$1", f.project).Scan(&events); err != nil || events != 1 {
		t.Fatalf("revoked integration wrote another Event: %d %v", events, err)
	}
	t.Log("QUAL36 isolated synthetic transport PASS: signed GitHub PR #1 -> durable spool -> authorized Goose17 Event -> external PI Claim -> AgentRun/Ready S3/TestAttempt; no automatic Done")
}
