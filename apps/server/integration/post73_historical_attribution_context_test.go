//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Only disposable Goose21 with a synthetic historic Run is allowed here.
// The official Goose21 database is never touched or re-attributed.
func TestPost73HistoricalRunSourceIndexPreservesTaskAuthorization(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/post73_hist_") {
		t.Skip("requires disposable post73_hist_* PostgreSQL and S3")
	}
	f := setup(t)
	ctx := context.Background()

	work := create(t, f, f.agent, "post73-historical-index")
	workID := work["id"].(string)
	path := f.url(f.project) + "/" + workID
	other := create(t, f, f.agent, "post73-unrelated-task")
	otherPath := f.url(f.project) + "/" + other["id"].(string)

	caseID, _ := mustCase(t, f, "post73-case", "Unique legacy Run source")
	caseVersion, _ := publishCase(t, f, caseID, "post73-publish", 1)
	runIDs := make([]string, 0, 2)
	for n, name := range []string{"old-failed", "old-passed"} {
		run := mustRun(t, f, []string{caseVersion}, "post73-"+name)
		runID := run["id"].(string)
		mustAttempt(t, f, run["items"].([]any)[0].(map[string]any)["id"].(string), f.reporter,
			"post73-original-"+name, []string{"failed", "passed"}[n])
		status, role, _ := request(t, http.MethodPost, path+"/role-entries", f.agent,
			"post73-source-"+name, "", roleInput("tester", "observed", "test_run", runID,
				"Synthetic old signed source reference, not an invented pass", n+1))
		if status != 201 || role["id"] == nil {
			t.Fatalf("role source write HTTP%d %#v", status, role)
		}
		roleID := uuid.MustParse(role["id"].(string))
		// Simulates precisely the immutable Goose21 unique attribution fact
		// added by migration, NOT a rewrite of the legacy TestRun.
		if _, err := f.pool.Exec(ctx, `INSERT INTO qual.historical_test_run_attributions
          (project_id,test_run_id,work_item_id,role_entry_ids)
          VALUES($1,$2,$3,$4)`, f.project, runID, workID, []uuid.UUID{roleID}); err != nil {
			t.Fatalf("synthetic provenance insert: %v", err)
		}
		runIDs = append(runIDs, runID)
	}
	for _, runID := range runIDs {
		var bound *uuid.UUID
		if err := f.pool.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, f.project, runID).Scan(&bound); err != nil || bound != nil {
			t.Fatalf("original legacy Run was altered %s %v %v", runID, bound, err)
		}
	}

	status, _, _ := request(t, http.MethodPut,
		f.server.URL+"/api/v1/projects/"+f.project.String()+"/task-authorization-policy", f.admin,
		"post73-isolated-policy", "", map[string]any{
			"mode": "task_scoped", "expected_version": 0,
			"reason": "Exercise scoped historical provenance with original Runs unchanged in disposable Goose21",
		})
	if status != 200 {
		t.Fatalf("could not enable scoped fixture %d", status)
	}

	for _, url := range []string{path, otherPath} {
		if code, _, _ := request(t, http.MethodGet, url+"/context?section=historical_attributions", f.agent2, "", "", nil); code != 403 {
			t.Fatalf("unclaimed Agent B saw private source (%d) %s", code, url)
		}
	}
	if code, _, _ := request(t, http.MethodGet, path+"/context?section=historical_attributions", "", "", "", nil); code != 401 {
		t.Fatalf("anonymous source read HTTP%d", code)
	}
	if code, _, _ := request(t, http.MethodGet,
		f.url(f.otherProject)+"/"+workID+"/context?section=historical_attributions", f.agent2, "", "", nil); code != 404 {
		t.Fatalf("cross-project historical source leaked HTTP%d", code)
	}

	status, claimed, _ := request(t, http.MethodPost, path+"/claim", f.agent2,
		"post73-b-claim", "", map[string]any{"expected_version": 3})
	if status != 200 && status != 201 {
		t.Fatalf("B claim HTTP%d %v", status, claimed)
	}
	first, items := sectionItems(t, path+"/context?section=historical_attributions&limit=1", f.agent2, 1)
	if first["has_more"] != true || first["next_cursor"] == "" {
		t.Fatalf("historical provenance paging missing: %v", first)
	}
	checkSource := func(row any) {
		record := row.(map[string]any)
		if record["source"] != "historical_test_run_attributions" || record["status"] != nil ||
			record["run_id"] == nil || len(record["role_entry_ids"].([]any)) != 1 {
			t.Fatalf("historical index invented Attempt or dropped provenance: %v", record)
		}
	}
	checkSource(items[0])
	next, items := sectionItems(t, path+"/context?section=historical_attributions&limit=1&cursor="+
		first["next_cursor"].(string), f.agent2, 1)
	checkSource(items[0])
	if next["has_more"] != false {
		t.Fatalf("historical page fabricated more: %v", next)
	}
	_, attempts := sectionItems(t, path+"/context?section=attempts", f.agent2, 0)
	if len(attempts) != 0 {
		t.Fatal("legacy unbound Attempts leaked into current bound Attempt section")
	}
	if code, _, _ := request(t, http.MethodGet, otherPath+"/context?section=historical_attributions", f.agent2, "", "", nil); code != 403 {
		t.Fatalf("B improperly read an unrelated Task private provenance index HTTP%d", code)
	}
	for _, runID := range runIDs {
		var bound *uuid.UUID
		if err := f.pool.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, f.project, runID).Scan(&bound); err != nil || bound != nil {
			t.Fatalf("source Run changed during context read %v %v", bound, err)
		}
	}
}
