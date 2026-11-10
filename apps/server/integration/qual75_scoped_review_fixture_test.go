//go:build integration

package integration

import (
	"context"
	"testing"
)

// Synthetic test-only helper for the published scoped WorkItem contract.
// It never reaches the official Qual DB; the test runner uses a new unique
// PostgreSQL database and a new disposable S3 bucket.
func qual75ReviewRunForWork(t *testing.T, f *fixture, prefix, itemID string) string {
	t.Helper()
	var schema bool
	if err := f.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
         WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	caseID, _ := mustCase(t, f, prefix+"-case", "Bound scoped review evidence fixture")
	ver, _ := publishCase(t, f, caseID, prefix+"-publish", 1)
	code, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		prefix+"-coverage", "", map[string]any{"work_item_id": itemID})
	if code != 201 {
		t.Fatalf("WorkItem test coverage failed: %d", code)
	}
	body := map[string]any{"name": "Synthetic external review Run", "source_system": "external-ci",
		"external_run_key": prefix + "-run", "case_version_ids": []string{ver},
		"environment": "disposable Postgres/S3", "build_ref": "qual75-scoped"}
	if schema {
		body["work_item_id"] = itemID
	}
	code, run := testRequest(t, "POST", testsURL(f)+"/test-runs", f.agent, prefix+"-run", "", body)
	if code != 201 {
		t.Fatalf("Synthetic review Run failed: HTTP%d %#v", code, run)
	}
	item := run["items"].([]any)[0].(map[string]any)
	mustAttempt(t, f, item["id"].(string), f.agent, prefix+"-attempt", "passed")
	return run["id"].(string)
}
