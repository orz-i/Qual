//go:build integration

package integration

import "testing"

func TestTestingCoverageBidirectionalAndScoped(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.admin, "covered-work-item")
	id, key := mustCase(t, f, "covered-test-case", "Coverage Case")
	url := testsURL(f) + "/test-cases/" + id + "/coverage"
	status, linked := testRequest(t, "POST", url, f.agent, "coverage-link-first", "", map[string]any{"work_item_id": work["id"]})
	if status != 201 || linked["key"] != key {
		t.Fatalf("link case coverage: %d %#v", status, linked)
	}
	if status, _ := testRequest(t, "POST", url, f.agent, "coverage-link-duplicate", "", map[string]any{"work_item_id": work["id"]}); status != 409 {
		t.Fatalf("expected duplicate coverage conflict: %d", status)
	}
	status, covered := testRequest(t, "GET", url, f.viewer, "", "", nil)
	if status != 200 || len(covered["items"].([]any)) != 1 || covered["items"].([]any)[0].(map[string]any)["key"] != work["key"] {
		t.Fatalf("case coverage GET=%d %#v", status, covered)
	}
	workURL := f.url(f.project) + "/" + work["id"].(string) + "/test-coverage"
	status, tests := testRequest(t, "GET", workURL, f.viewer, "", "", nil)
	if status != 200 || len(tests["items"].([]any)) != 1 || tests["items"].([]any)[0].(map[string]any)["key"] != key {
		t.Fatalf("reverse coverage=%d %#v", status, tests)
	}
	if status, _ := testRequest(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/test-cases/"+id+"/coverage", f.agent, "", "", nil); status != 404 {
		t.Fatalf("cross-project case=%d", status)
	}
}
