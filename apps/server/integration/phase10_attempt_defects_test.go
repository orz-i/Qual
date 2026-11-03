//go:build integration

package integration

import (
	"testing"

	"github.com/google/uuid"
)

func TestPhase10AttemptDefectReadUsesProjectTestReadAndPreservesHistory(t *testing.T) {
	f := setup(t)
	base := testsURL(f)
	cid, _ := mustCase(t, f, "phase10-qa-case", "External quality finding")
	version, _ := publishCase(t, f, cid, "phase10-qa-publish", 1)
	run := mustRun(t, f, []string{version}, "phase10-qa-run")
	rid := run["id"].(string)
	item := run["items"].([]any)[0].(map[string]any)
	failed := mustAttempt(t, f, item["id"].(string), f.agent, "phase10-qa-failed", "failed")
	attemptID := failed["id"].(string)
	endpoint := base + "/test-attempts/" + attemptID + "/defects"

	for _, scenario := range []struct {
		name, url, token string
		code             int
	}{
		{"anonymous", endpoint, "", 401},
		{"foreign project", f.server.URL + "/api/v1/projects/" + f.otherProject.String() + "/test-attempts/" + attemptID + "/defects", f.agent, 404},
		{"unknown attempt", base + "/test-attempts/" + uuid.NewString() + "/defects", f.agent, 404},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			status, _ := testRequest(t, "GET", scenario.url, scenario.token, "", "", nil)
			if status != scenario.code {
				t.Fatalf("expected %d, got %d", scenario.code, status)
			}
		})
	}
	if status, page := testRequest(t, "GET", endpoint, f.viewer, "", "", nil); status != 200 || len(page["items"].([]any)) != 0 {
		t.Fatalf("viewer must be able to inspect empty attempt links: %d %v", status, page)
	}
	status, link := testRequest(t, "POST", endpoint, f.agent, "phase10-qa-create-bug", "", map[string]any{
		"title": "Genuine failed Attempt test fixture", "severity": "major",
	})
	if status != 201 {
		t.Fatalf("cannot link failed attempt %d %v", status, link)
	}
	bug := link["bug_id"].(string)
	if status, _ := testRequest(t, "POST", endpoint, f.reporter, "phase10-integration-cannot-create-bug", "", map[string]any{"title": "Unauthorized bug"}); status != 403 {
		t.Fatalf("integration wrote a bug from a result %d", status)
	}
	status, read := testRequest(t, "GET", endpoint, f.viewer, "", "", nil)
	if status != 200 {
		t.Fatalf("read only viewer cannot read existing failure links: %d", status)
	}
	links := read["items"].([]any)
	if len(links) != 1 {
		t.Fatalf("linked bug absent %v", read)
	}
	d := links[0].(map[string]any)
	if d["attempt_id"] != attemptID || d["bug_id"] != bug || d["bug_key"] != link["bug_key"] ||
		d["bug_status"] != "backlog" || d["bug_title"] != "Genuine failed Attempt test fixture" {
		t.Fatalf("wrong linked bug facts: %v", d)
	}
	if d["linked_at"] == "" || d["linked_by"] != principalFor(t, f, f.agent).String() {
		t.Fatalf("link provenance missing: %v", d)
	}
	if len(d) != 7 {
		t.Fatalf("GET leaked unexpected data: %v", d)
	}
	passed := mustAttempt(t, f, item["id"].(string), f.agent, "phase10-qa-later-passed", "passed")
	if status, page := testRequest(t, "GET", base+"/test-attempts/"+passed["id"].(string)+"/defects", f.viewer, "", "", nil); status != 200 || len(page["items"].([]any)) != 0 {
		t.Fatalf("new passed attempt should not inherit prior Bug: %d %v", status, page)
	}
	if status, page := testRequest(t, "GET", endpoint, f.viewer, "", "", nil); status != 200 || len(page["items"].([]any)) != 1 {
		t.Fatalf("past Failed Attempt relation lost after Passed: %d %v", status, page)
	}
	if status, current := testRequest(t, "GET", base+"/test-runs/"+rid, f.viewer, "", "", nil); status != 200 || current["items"].([]any)[0].(map[string]any)["latest_status"] != "passed" {
		t.Fatalf("latest result failed to advance while preserving history: %d %v", status, current)
	}
}
