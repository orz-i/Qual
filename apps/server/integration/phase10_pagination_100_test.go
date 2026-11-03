//go:build integration

package integration

import (
	"fmt"
	"net/url"
	"testing"
)

// Tests a REAL Go HTTP client, scoped project auth, and a disposable
// PostgreSQL fixture >100 items. It does not put artificial work in QUAL.
func TestPhase10WorkItemPaginationBeyond100NoDupes(t *testing.T) {
	f := setup(t)
	base := f.url(f.project)
	for n := 0; n < 125; n++ {
		create(t, f, f.agent, fmt.Sprintf("qual17-pagination-real-http-%03d", n))
	}
	bstatus, bug, _ := request(t, "POST", base, f.agent, "qual17-pagination-created-bug-001", "",
		map[string]any{"kind": "bug", "title": "Actual integration fixture only", "priority": "low",
			"bug_details": map[string]any{"severity": "minor"}})
	if bstatus != 201 {
		t.Fatalf("cannot create bug test fixture: %d %v", bstatus, bug)
	}
	pstatus, page, _ := request(t, "GET", base+"?limit=100", f.viewer, "", "", nil)
	if pstatus != 200 || len(page["items"].([]any)) != 100 {
		t.Fatalf("expected actual first 100, got %d %v", pstatus, page)
	}
	cursor, ok := page["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("first page did not provide next_cursor")
	}
	// Values received on the first page are immutable. An exact cursor
	// comparison prevents overlap even when multiple items have same timestamp.
	seen := map[string]struct{}{}
	appendPage := func(values []any) {
		t.Helper()
		for _, v := range values {
			item := v.(map[string]any)
			id := item["id"].(string)
			if _, exists := seen[id]; exists {
				t.Fatalf("duplicate UUID %s after pagination", id)
			}
			seen[id] = struct{}{}
		}
	}
	appendPage(page["items"].([]any))
	status, last, _ := request(t, "GET", base+"?limit=100&cursor="+url.QueryEscape(cursor), f.viewer, "", "", nil)
	if status != 200 || len(last["items"].([]any)) != 26 {
		t.Fatalf("expected second page 26, got %d %v", status, last)
	}
	if _, more := last["next_cursor"]; more {
		t.Fatalf("unexpected third cursor for 126 fixtures: %v", last)
	}
	appendPage(last["items"].([]any))
	if len(seen) != 126 {
		t.Fatalf("dropped or repeated fixtures: %d", len(seen))
	}
	status, bugs, _ := request(t, "GET", base+"?limit=100&kind=bug", f.viewer, "", "", nil)
	if status != 200 || len(bugs["items"].([]any)) != 1 || bugs["items"].([]any)[0].(map[string]any)["id"] != bug["id"] {
		t.Fatalf("kind filtering failed: %d %v", status, bugs)
	}
	status, tasks, _ := request(t, "GET", base+"?limit=100&kind=task", f.viewer, "", "", nil)
	if status != 200 || len(tasks["items"].([]any)) != 100 || tasks["next_cursor"] == nil {
		t.Fatalf("filtered tasks should paginate 100+ with scoped cursor: %d %v", status, tasks)
	}
	taskCursor := tasks["next_cursor"].(string)
	status, lastTasks, _ := request(t, "GET", base+"?limit=100&kind=task&cursor="+url.QueryEscape(taskCursor), f.viewer, "", "", nil)
	if status != 200 || len(lastTasks["items"].([]any)) != 25 {
		t.Fatalf("second filtered page expected 25: %d %v", status, lastTasks)
	}
	if status, _, _ := request(t, "GET", base+"?limit=100&kind=bug&cursor="+url.QueryEscape(taskCursor), f.viewer, "", "", nil); status != 422 {
		t.Fatalf("cross-filter cursor reused, status=%d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject)+"?limit=10&cursor="+url.QueryEscape(cursor), f.admin, "", "", nil); status != 422 {
		t.Fatalf("foreign project reused cursor, status=%d", status)
	}
	if status, _, _ := request(t, "GET", base+"?limit=101", f.viewer, "", "", nil); status != 400 {
		t.Fatalf("backend ignored page size limit: %d", status)
	}
	if status, _, _ := request(t, "GET", base+"?limit=100", f.reporter, "", "", nil); status != 403 {
		t.Fatalf("work:read missing but cross-scope query allowed: %d", status)
	}
	// A local search of just the first page is explicitly incomplete. The
	// front-end must preserve the NextPage affordance and not call it a
	// server-side search (covered by existing TypeScript work-view test).
	t.Logf("Pagination scope verified: 126 real HTTP fixture WorkItems, 100 first page + 26 last page, task-only 100+25, no duplicate UUID")
}
