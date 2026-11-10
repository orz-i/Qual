//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI aggregates existing authenticated TestRun and Attempt APIs;
// it never invokes or fabricates an external test process. Only run against
// disposable PostgreSQL, never the live/default Qual project.
func TestPhase12ExternalReportCLIRecordsOnceAcrossRetries(t *testing.T) {
	dbURL := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(dbURL, "/qual12_report_") {
		t.Skip("requires disposable qual12_report_* PostgreSQL database")
	}
	f := setup(t)
	caseID, _ := mustCase(t, f, "phase12-cli-report-case", "External report ingestion observed")
	vid, version := publishCase(t, f, caseID, "phase12-cli-report-publish", 1)
	work := create(t, f, f.agent, "phase12-cli-report-work-item")
	workID := work["id"].(string)
	if version != 1 {
		t.Fatal("immutable case version changed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "qual")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual")
	if _, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building external Qual CLI failed (redacted): %v", err)
	}
	env := make([]string, 0)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "QUAL_TOKEN=") && !strings.HasPrefix(e, "QUAL_API_URL=") {
			env = append(env, e)
		}
	}
	env = append(env, "QUAL_TOKEN="+f.agent, "QUAL_API_URL="+f.server.URL)
	root := []string{"test", "report", "-project", f.project.String(),
		"-case-version", vid, "-work-item", workID, "-title", "externally executed Go validation",
		"-status", "passed", "-note", "Go tests observed passing in trusted external test host",
		"-report-source", "pi-integration-host", "-build", "0b15cafe",
		"-request-key", "phase12-external-cli-report-stable-event-1"}
	invoke := func(args []string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		// If this fails do not print response bodies; protect any credential.
		return cmd.CombinedOutput()
	}
	out, err := invoke(root)
	if err != nil {
		t.Fatalf("external CLI report failed (output redacted): %v", err)
	}
	var receipt struct {
		TestRunID      string `json:"test_run_id"`
		RunItemID      string `json:"run_item_id"`
		AttemptID      string `json:"attempt_id"`
		AttemptNo      int    `json:"attempt_no"`
		Status         string `json:"status"`
		Source         string `json:"source_kind"`
		WorkItemID     string `json:"work_item_id"`
		CoverageLinked bool   `json:"coverage_linked"`
		RunBinding     string `json:"run_binding"`
	}
	if err := json.Unmarshal(out, &receipt); err != nil {
		t.Fatalf("external CLI receipt not JSON, output omitted: %v", err)
	}
	if receipt.TestRunID == "" || receipt.RunItemID == "" || receipt.AttemptID == "" ||
		receipt.AttemptNo != 1 || receipt.Status != "passed" || receipt.Source != "external" ||
		receipt.WorkItemID != workID || !receipt.CoverageLinked ||
		receipt.RunBinding != "legacy_coverage_only" {
		t.Fatal("actual CLI receipt omitted durable identities or changed reported status")
	}
	if strings.Contains(string(out), f.agent) {
		t.Fatal("test reporter leaked its bearer token")
	}
	out2, err := invoke(root)
	if err != nil {
		t.Fatalf("idempotent CLI rerun failed (output redacted): %v", err)
	}
	if string(out) != string(out2) {
		t.Fatal("retry did not return the same run and attempt identities")
	}
	// Same source + same request key but different status/note MUST NOT
	// silently update or append a second Attempt.
	modified := append([]string{}, root...)
	for i := range modified {
		if modified[i] == "Go tests observed passing in trusted external test host" {
			modified[i] = "changed notes should be rejected on replay"
		}
	}
	changed, err := invoke(modified)
	if err == nil || !strings.Contains(string(changed), "IDEMPOTENCY_CONFLICT") ||
		strings.Contains(string(changed), f.agent) {
		t.Fatal("changing a reported result with the same idempotency key was not rejected")
	}
	code, run := testRequest(t, "GET", testsURL(f)+"/test-runs/"+receipt.TestRunID, f.agent, "", "", nil)
	if code != 200 || len(run["items"].([]any)) != 1 || run["work_item_id"] != nil {
		t.Fatal("Goose17 original TestRun must remain unbound; only separate Coverage links the Task")
	}
	code, attempts := testRequest(t, "GET", attemptURL(f, receipt.RunItemID), f.agent, "", "", nil)
	if code != 200 || len(attempts["items"].([]any)) != 1 ||
		attempts["items"].([]any)[0].(map[string]any)["id"] != receipt.AttemptID {
		t.Fatal("retry/change produced duplicate Attempt or erased history")
	}
	coverageURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/work-items/" + workID + "/test-coverage"
	if code, coverage := testRequest(t, "GET", coverageURL, f.agent, "", "", nil); code != 200 {
		t.Fatal("WorkItem coverage is not readable after report")
	} else {
		found := false
		for _, raw := range coverage["items"].([]any) {
			if raw.(map[string]any)["id"] == caseID {
				found = true
			}
		}
		if !found {
			t.Fatal("TestCase was not linked as WorkItem coverage")
		}
	}
	if status, _, _ := request(t, "GET", testsURL(f)+"/test-runs/"+receipt.TestRunID, f.agent2, "", "", nil); status != 200 {
		t.Fatal("authorized same-project reader cannot see reported history")
	}
	if status, _, _ := request(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/test-runs/"+receipt.TestRunID, f.agent, "", "", nil); status != 404 {
		t.Fatal("cross-project read must remain isolated")
	}
}
