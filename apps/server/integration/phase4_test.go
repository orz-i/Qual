//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func testsURL(f *fixture) string { return f.server.URL + "/api/v1/projects/" + f.project.String() }
func testRequest(t *testing.T, method, url, token, key, version string, body any) (int, map[string]any) {
	t.Helper()
	code, obj, _ := request(t, method, url, token, key, version, body)
	return code, obj
}
func caseSpec(title string) map[string]any {
	return map[string]any{"title": title, "preconditions": "User exists", "expected_result": "Accepted",
		"steps": []map[string]string{{"action": "Sign in", "expected": "Welcome displayed"}}}
}
func mustCase(t *testing.T, f *fixture, key, title string) (string, string) {
	t.Helper()
	code, c := testRequest(t, "POST", testsURL(f)+"/test-cases", f.agent, key, "", map[string]any{"specification": caseSpec(title)})
	if code != 201 {
		t.Fatalf("case create: %d %#v", code, c)
	}
	return c["id"].(string), c["key"].(string)
}
func publishCase(t *testing.T, f *fixture, caseID, key string, revision int) (string, int) {
	t.Helper()
	code, v := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/versions", f.agent, key, "",
		map[string]any{"expected_revision": revision})
	if code != 201 {
		t.Fatalf("publish: %d %#v", code, v)
	}
	return v["id"].(string), int(v["version"].(float64))
}
func mustRun(t *testing.T, f *fixture, versionIDs []string, key string) map[string]any {
	t.Helper()
	code, run := testRequest(t, "POST", testsURL(f)+"/test-runs", f.agent, key, "", map[string]any{
		"name": "CI Results", "source_system": "external-ci", "external_run_key": key,
		"environment": "linux", "build_ref": "deadbeef", "case_version_ids": versionIDs,
	})
	if code != 201 {
		t.Fatalf("create run: %d %#v", code, run)
	}
	return run
}
func attemptURL(f *fixture, itemID string) string {
	return testsURL(f) + "/test-run-items/" + itemID + "/attempts"
}
func mustAttempt(t *testing.T, f *fixture, itemID, token, key, status string) map[string]any {
	t.Helper()
	code, a := testRequest(t, "POST", attemptURL(f, itemID), token, key, "",
		map[string]any{"status": status, "note": "Reported observation only", "source_kind": "external"})
	if code != 201 {
		t.Fatalf("attempt: %d %#v", code, a)
	}
	return a
}
func TestTestingVersionedHistoryAndBugTraceability(t *testing.T) {
	f := setup(t)
	base := testsURL(f)
	if status, _ := testRequest(t, "GET", base+"/test-cases", "", "", "", nil); status != 401 {
		t.Fatalf("anon=%d", status)
	}
	if status, _ := testRequest(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/test-cases", f.agent, "", "", nil); status != 404 {
		t.Fatalf("cross-project=%d", status)
	}
	if status, _ := testRequest(t, "POST", base+"/test-cases", f.viewer, "denied-viewer-case", "", map[string]any{"specification": caseSpec("Forbidden")}); status != 403 {
		t.Fatalf("viewer write=%d", status)
	}
	if status, _ := testRequest(t, "POST", base+"/test-cases", f.reporter, "denied-integration-spec", "", map[string]any{"specification": caseSpec("Forbidden")}); status != 403 {
		t.Fatalf("integration changed spec: %d", status)
	}
	code, suite := testRequest(t, "POST", base+"/test-suites", f.admin, "suite-authentication", "", map[string]any{"name": "Auth"})
	if code != 201 {
		t.Fatalf("suite %d %#v", code, suite)
	}
	if code, rows := testRequest(t, "GET", base+"/test-suites", f.viewer, "", "", nil); code != 200 || len(rows["items"].([]any)) < 1 {
		t.Fatalf("suite read %d %v", code, rows)
	}
	suiteID := suite["id"].(string)
	code, c := testRequest(t, "POST", base+"/test-cases", f.agent, "case-first-version", "", map[string]any{
		"suite_id": suiteID, "specification": caseSpec("Valid login v1"),
	})
	if code != 201 {
		t.Fatalf("case %d %#v", code, c)
	}
	caseID := c["id"].(string)
	if c["key"] != "ENG-TC-1" {
		t.Fatalf("unexpected key %v", c["key"])
	}
	if c["revision"] != float64(1) {
		t.Fatalf("initial revision %v", c)
	}
	vid1, vno := publishCase(t, f, caseID, "publish-first-version", 1)
	if vno != 1 {
		t.Fatalf("v1 version %d", vno)
	}
	if code, _ := testRequest(t, "POST", base+"/test-cases/"+caseID+"/versions", f.agent, "publish-unchanged-revision", "",
		map[string]any{"expected_revision": 1}); code != 409 {
		t.Fatalf("duplicate publish %d", code)
	}
	code, updated := testRequest(t, "PATCH", base+"/test-cases/"+caseID, f.agent, "edit-to-version2", `"1"`,
		map[string]any{"suite_id": suiteID, "specification": caseSpec("Valid login v2")})
	if code != 200 || updated["revision"] != float64(2) {
		t.Fatalf("update=%d %#v", code, updated)
	}
	if code, _ := testRequest(t, "PATCH", base+"/test-cases/"+caseID, f.agent, "stale-revision-update", `"1"`,
		map[string]any{"specification": caseSpec("Overwrites?")}); code != 409 {
		t.Fatalf("stale edit=%d", code)
	}
	vid2, vno := publishCase(t, f, caseID, "publish-second-version", 2)
	if vno != 2 || vid1 == vid2 {
		t.Fatalf("versions mismatch %d", vno)
	}
	code, versions := testRequest(t, "GET", base+"/test-cases/"+caseID+"/versions", f.viewer, "", "", nil)
	if code != 200 || len(versions["items"].([]any)) != 2 {
		t.Fatalf("versions %d %#v", code, versions)
	}
	listed := versions["items"].([]any)
	first := listed[1].(map[string]any)
	if first["specification"].(map[string]any)["title"] != "Valid login v1" {
		t.Fatalf("v1 mutated: %#v", first)
	}
	if len(first["sha256"].(string)) != 64 {
		t.Fatalf("missing sha256: %#v", first)
	}
	// Published versions are immutable at the database level.
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.test_case_versions SET specification='{}' WHERE id=$1`, vid1); err == nil {
		t.Fatal("version update was accepted")
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM qual.test_case_versions WHERE id=$1`, vid1); err == nil {
		t.Fatal("version deletion was accepted")
	}

	// A run created after v2 publication still binds explicitly to v1.
	run := mustRun(t, f, []string{vid1}, "run-bind-version1")
	rid := run["id"].(string)
	items := run["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("missing run items %v", run)
	}
	item := items[0].(map[string]any)
	itemID := item["id"].(string)
	if item["case_version"] != float64(1) || item["case_version_id"] != vid1 {
		t.Fatalf("run did not freeze v1: %v", item)
	}
	if code, detail := testRequest(t, "GET", base+"/test-runs/"+rid, f.viewer, "", "", nil); code != 200 || detail["items"].([]any)[0].(map[string]any)["case_version_id"] != vid1 {
		t.Fatalf("run version drift: %d %v", code, detail)
	}
	failed := mustAttempt(t, f, itemID, f.reporter, "result-first-failed", "failed")
	if failed["attempt_no"] != float64(1) {
		t.Fatalf("bad attempt index %v", failed)
	}
	if code, replay := testRequest(t, "POST", attemptURL(f, itemID), f.reporter, "result-first-failed", "",
		map[string]any{"status": "failed", "note": "Reported observation only", "source_kind": "external"}); code != 201 || replay["id"] != failed["id"] {
		t.Fatalf("idempotency replay=%d %v", code, replay)
	}
	if code, _ := testRequest(t, "POST", attemptURL(f, itemID), f.reporter, "result-first-failed", "",
		map[string]any{"status": "passed", "note": "Different payload", "source_kind": "external"}); code != 409 {
		t.Fatalf("idempotency collision=%d", code)
	}
	passed := mustAttempt(t, f, itemID, f.reporter, "result-later-passed", "passed")
	if passed["attempt_no"] != float64(2) {
		t.Fatalf("failed result overwritten: %v", passed)
	}
	code, history := testRequest(t, "GET", attemptURL(f, itemID), f.viewer, "", "", nil)
	if code != 200 || len(history["items"].([]any)) != 2 ||
		history["items"].([]any)[1].(map[string]any)["status"] != "failed" {
		t.Fatalf("history lost: %d %#v", code, history)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.test_attempts SET status='passed' WHERE id=$1`, failed["id"]); err == nil {
		t.Fatal("Attempt UPDATE succeeded")
	}
	code, link := testRequest(t, "POST", base+"/test-attempts/"+failed["id"].(string)+"/defects", f.agent, "bug-from-failed-1", "",
		map[string]any{"title": "Session rejected", "severity": "critical", "reproduction_steps": "Reuse expired token"})
	if code != 201 || link["created"] != true {
		t.Fatalf("create Bug from failed=%d %#v", code, link)
	}
	bugID := link["bug_id"].(string)
	bugKey := link["bug_key"].(string)
	if bugKey != "ENG-1" {
		t.Fatalf("Bug key %s", bugKey)
	}
	if code, bug, _ := request(t, "GET", f.url(f.project)+"/"+bugID, f.admin, "", "", nil); code != 200 || bug["kind"] != "bug" {
		t.Fatalf("Bug not created: %d %#v", code, bug)
	}
	if code, _ := testRequest(t, "POST", base+"/test-attempts/"+passed["id"].(string)+"/defects", f.agent, "invalid-bug-from-pass", "",
		map[string]any{"bug_id": bugID}); code != 422 {
		t.Fatalf("linked passed result to Bug: %d", code)
	}
	if code, related := testRequest(t, "POST", base+"/test-attempts/"+failed["id"].(string)+"/defects", f.agent, "duplicate-defect-link", "",
		map[string]any{"bug_id": bugID}); code != 409 {
		t.Fatalf("duplicate bug link=%d %v", code, related)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.attempt_defects WHERE project_id=$1`, f.project).Scan(&count); err != nil || count != 1 {
		t.Fatalf("defect link count %d %v", count, err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE entity_id=$1`, uuid.MustParse(failed["id"].(string))).Scan(&count); err != nil || count != 2 {
		t.Fatalf("attempt audit=%d err=%v", count, err)
	}
}
func xmlImport(t *testing.T, url, token, key, filename, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Report-Filename", filename)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data
}
func TestJUnitImportAtomicMappingAndAudit(t *testing.T) {
	f := setup(t)
	cid, key := mustCase(t, f, "junit-case-spec", "JUnit Login")
	version, _ := publishCase(t, f, cid, "junit-publish", 1)
	run := mustRun(t, f, []string{version}, "junit-run-created")
	itemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	importURL := testsURL(f) + "/test-runs/" + run["id"].(string) + "/junit-imports"
	xml := fmt.Sprintf(`<testsuites><testsuite name="External CI"><testcase name="dynamic-name">
  <properties><property name="qual.case_key" value="%s"/></properties>
  <failure message="Login mismatch">details from external runner</failure>
 </testcase></testsuite></testsuites>`, key)
	if code, _ := xmlImport(t, importURL, f.reporter, "xml-unknown-key", "report.xml", strings.Replace(xml, key, "ENG-TC-999", 1)); code != 404 {
		t.Fatalf("unknown mapping=%d", code)
	}
	if code, attempts := testRequest(t, "GET", attemptURL(f, itemID), f.viewer, "", "", nil); code != 200 || len(attempts["items"].([]any)) != 0 {
		t.Fatalf("unknown mapping produced attempts %d %v", code, attempts)
	}
	status, summary := xmlImport(t, importURL, f.reporter, "xml-import-first", "report.xml", xml)
	if status != 201 || summary["failed"] != float64(1) || summary["imported_count"] != float64(1) {
		t.Fatalf("import=%d %v", status, summary)
	}
	if status, again := xmlImport(t, importURL, f.reporter, "xml-import-first", "report.xml", xml); status != 201 || again["id"] != summary["id"] {
		t.Fatalf("replay import=%d %v", status, again)
	}
	if status, _ := xmlImport(t, importURL, f.reporter, "xml-import-duplicate-payload", "report.xml", xml); status != 409 {
		t.Fatalf("same report duplicate not rejected=%d", status)
	}
	malicious := `<!DOCTYPE testsuite [<!ENTITY x SYSTEM "file:///etc/passwd">]><testsuite><testcase name="` + key + `"/></testsuite>`
	if status, _ := xmlImport(t, importURL, f.reporter, "xml-malicious-dtd", "report.xml", malicious); status != 422 {
		t.Fatalf("DTD accepted=%d", status)
	}
	if status, _ := xmlImport(t, importURL, f.reporter, "xml-oversized", "report.xml", strings.Repeat("a", 1048577)); status != 413 {
		t.Fatalf("oversized report=%d", status)
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.test_attempts WHERE project_id=$1`, f.project).Scan(&count); err != nil || count != 1 {
		t.Fatalf("result count after malformed imports=%d err=%v", count, err)
	}
	var raw []byte
	if err := f.pool.QueryRow(context.Background(), `SELECT raw_xml FROM qual.test_report_imports WHERE id=$1`, summary["id"]).Scan(&raw); err != nil || !bytes.Equal(raw, []byte(xml)) {
		t.Fatalf("raw report evidence missing: %v", err)
	}
}
func TestConcurrentExternalResultsAreAppended(t *testing.T) {
	f := setup(t)
	id, _ := mustCase(t, f, "attempt-race-spec", "Concurrent result")
	vid, _ := publishCase(t, f, id, "attempt-race-publish", 1)
	run := mustRun(t, f, []string{vid}, "attempt-race-run")
	itemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	start := make(chan struct{})
	status := make([]int, 2)
	tokens := []string{f.agent, f.agent2}
	var wg sync.WaitGroup
	for i := range tokens {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			status[index], _ = testRequest(t, "POST", attemptURL(f, itemID), tokens[index], fmt.Sprintf("attempt-race-%d", index), "",
				map[string]any{"status": "failed", "source_kind": "external", "note": "Simultaneous report"})
		}(i)
	}
	close(start)
	wg.Wait()
	if status[0] != 201 || status[1] != 201 {
		t.Fatalf("concurrent attempt status=%v", status)
	}
	code, items := testRequest(t, "GET", attemptURL(f, itemID), f.admin, "", "", nil)
	if code != 200 || len(items["items"].([]any)) != 2 {
		t.Fatalf("attempt race lost results %d %v", code, items)
	}
	if items["items"].([]any)[0].(map[string]any)["attempt_no"] != float64(2) {
		t.Fatalf("attempt sequence not monotonic: %v", items)
	}
}
