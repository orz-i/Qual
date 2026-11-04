//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This test operates exclusively in a newly migrated disposable qual32_run_*
// database. It seeds synthetic external facts, never real GitHub deliveries.
func TestQual34AttentionScopedDeduplicatedReadOnly(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_") {
		t.Skip("requires disposable qual32_run_* PostgreSQL database")
	}
	f := setup(t)
	ctx := context.Background()
	base := fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
	endpoint := base + "/attention"
	activate := func(key string, toReview bool) map[string]any {
		t.Helper()
		item := create(t, f, f.agent, "qual34-"+key)
		id := item["id"].(string)
		states := []string{"todo", "in_progress"}
		if toReview {
			states = append(states, "in_review")
		}
		for i, state := range states {
			code, body, _ := request(t, "POST", base+"/work-items/"+id+"/transitions",
				f.agent, fmt.Sprintf("qual34-%s-state-%d", key, i), "", map[string]any{
					"status": state, "expected_version": i + 1,
				})
			if code != 200 || body["status"] != state {
				t.Fatalf("activation %s/%s: %d %v", key, state, code, body)
			}
		}
		return item
	}
	needs := activate("needs", false)
	awaiting := activate("awaiting", true)
	blocked := activate("blocked", false)
	stale := activate("stale", false)
	unknown := activate("unknown", false)
	signal := activate("signal", false)
	nonSignal := activate("normal-event", false)
	oldSignal := activate("old-signal", false)
	needsID := needs["id"].(string)
	if code, body, _ := request(t, "POST", roleEndpoint(f, needsID), f.admin,
		"qual34-review-needs", "", roleInput("reviewer", "needs_work", "none", "",
			"Actual Reviewer needs changes; no acceptance", 3)); code != 201 {
		t.Fatalf("Reviewer record %d %v", code, body)
	}

	// Projection input is synthetic and inserted only into disposable PG.
	// Core AgentRun authorization and replay behavior are separately covered
	// by real HTTP tests; this test exercises ranking and scope together.
	var ws uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	actor := principalFor(t, f, f.agent)
	insertRun := func(work map[string]any, suffix, status string, lastSeen *time.Time) {
		t.Helper()
		_, err := f.pool.Exec(ctx, `INSERT INTO qual.agent_runs
		  (id,workspace_id,project_id,work_item_id,source,external_run_id,host,
		   reporter_principal_id,registration_credential_id,source_status,last_seen_at)
		  VALUES($1,$2,$3,$4,'pi',$5,'synthetic-pi',$6,$7,$8,$9)`,
			uuid.New(), ws, f.project, work["id"], suffix, actor, f.agentCredential, status, lastSeen)
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	recent := now.Add(-20 * time.Second)
	old := now.Add(-10 * time.Minute)
	insertRun(blocked, "blocked-1", "blocked", &recent)
	insertRun(awaiting, "blocked-duplicate-work", "blocked", &recent) // Reviewer wins; one attention row per WorkItem.
	insertRun(stale, "stale-1", "running", &old)
	insertRun(unknown, "unknown-1", "running", nil)
	insertRun(nonSignal, "healthy-1", "running", &recent)

	insertEvent := func(item map[string]any, key, typ string, when time.Time) {
		t.Helper()
		id := item["id"].(string)
		input := threadInput("example/project/pull/"+key, 3)
		delete(input, "url") // Optional and unrelated to attention; avoids a mismatched synthetic URL.
		code, link, _ := request(t, "POST", threadBase(f, id), f.agent,
			"qual34-link-"+key, "", input)
		// For this test the synthetic ID must be a positive GitHub pull number.
		if code != 201 {
			t.Fatalf("link %s %d %v", key, code, link)
		}
		thread := link["thread"].(map[string]any)["id"]
		// A real ingestion timestamp follows the link mutation. Historical
		// signals deliberately retain their old timestamp for expiry checks.
		if time.Since(when) < time.Minute {
			when = time.Now().UTC()
		}
		digest := sha256.Sum256([]byte(key))
		_, err := f.pool.Exec(ctx, `INSERT INTO qual.external_events
		    (id,workspace_id,project_id,work_item_id,thread_id,source,external_event_id,
		     event_type,occurred_at,received_at,payload_sha256,summary,
		     reporter_principal_id,registration_credential_id)
		    VALUES($1,$2,$3,$4,$5,'github',$6,$7,$8,$9,$10,'Synthetic hint only',$11,$12)`,
			uuid.New(), ws, f.project, id, thread, key, typ, when.Add(-time.Hour), when, digest[:], actor, f.agentCredential)
		if err != nil {
			t.Fatal(err)
		}
	}
	insertEvent(signal, "91", "needs_input", now)
	insertEvent(blocked, "94", "attention.required", now) // A second source cannot duplicate a Run alert.
	insertEvent(nonSignal, "92", "pull_request.opened", now)
	insertEvent(oldSignal, "93", "attention.required", now.Add(-8*24*time.Hour))

	check := func(path, token string, want int) map[string]any {
		t.Helper()
		status, result, headers := request(t, "GET", path, token, "", "", nil)
		if status != want {
			t.Fatalf("GET %s: HTTP%d want%d %v", path, status, want, result)
		}
		if want == 200 && headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("missing no-store")
		}
		return result
	}
	check(endpoint, "", 401)
	check(endpoint, f.reporter, 403)
	check(f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/attention", f.agent, 404)
	for _, suffix := range []string{"?limit=51", "?offset=-1", "?offset=451", "?limit=2&limit=3", "?raw_audit=1"} {
		check(endpoint+suffix, f.viewer, 400)
	}
	var before int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1`, f.project).Scan(&before); err != nil {
		t.Fatal(err)
	}
	page := check(endpoint+"?limit=50", f.viewer, 200)
	rows := page["items"].([]any)
	if len(rows) != 6 || page["has_more"] != false {
		t.Fatalf("expected 6 distinct attention WorkItems, got %v", page)
	}
	if rows[1].(map[string]any)["work_item_id"] != awaiting["id"] {
		t.Fatalf("unreviewed in_review WorkItem was not classified as awaiting")
	}
	want := []string{"review_needs_work", "review_awaiting", "run_blocked", "run_stale", "run_unknown", "event_needs_input"}
	seen := map[string]bool{}
	for i, raw := range rows {
		x := raw.(map[string]any)
		reason := x["reason"].(string)
		if reason != want[i] {
			t.Fatalf("attention order at %d: %s want %s: %v", i, reason, want[i], page)
		}
		id := x["work_item_id"].(string)
		if seen[id] {
			t.Fatalf("duplicate WorkItem %s", id)
		}
		seen[id] = true
		if x["source_id"] == "" || x["observed_at"] == nil {
			t.Fatalf("missing provenance %v", x)
		}
		if i < 2 && x["hint_only"] != false || i >= 2 && x["hint_only"] != true {
			t.Fatalf("review vs external hint authority lost %v", x)
		}
		for _, field := range []string{"audit_id", "credential_id", "payload_sha256", "reviewer_passed", "test_attempt"} {
			if _, ok := x[field]; ok {
				t.Fatalf("leaked restricted fields %s", field)
			}
		}
	}
	ids := []string{}
	for offset := 0; offset < 6; offset += 2 {
		p := check(fmt.Sprintf("%s?limit=2&offset=%d", endpoint, offset), f.viewer, 200)
		for _, v := range p["items"].([]any) {
			ids = append(ids, v.(map[string]any)["work_item_id"].(string))
		}
		if offset < 4 && (p["next_offset"] != float64(offset+2) || p["has_more"] != true) {
			t.Fatalf("page %d wrong %v", offset, p)
		}
	}
	for i, v := range rows {
		if ids[i] != v.(map[string]any)["work_item_id"] {
			t.Fatalf("paging order drift %v vs %v", ids, rows)
		}
	}
	var after int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1`, f.project).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("GET created audit records %d->%d", before, after)
	}

	// Real audited re-submission supersedes the old needs_work for QUEUE
	// presentation but does not delete its immutable Reviewer record.
	status, updated, _ := request(t, "POST", base+"/work-items/"+needsID+"/transitions",
		f.agent, "qual34-resubmission", "", map[string]any{"status": "in_review", "expected_version": 4})
	if status != 200 || updated["status"] != "in_review" {
		t.Fatalf("resubmit %d %v", status, updated)
	}
	newPage := check(endpoint+"?limit=50", f.viewer, 200)
	for _, v := range newPage["items"].([]any) {
		row := v.(map[string]any)
		if row["work_item_id"] == needsID && row["reason"] != "review_awaiting" {
			t.Fatalf("old needs_work falsely treated as current review: %v", row)
		}
	}
	// Read-only contract: no credentials or audit permissions were changed
	// for the viewer; the separate ReviewQueue remains available.
	if code, _, _ := request(t, "GET", base+"/review-queue", f.viewer, "", "", nil); code != 200 {
		t.Fatalf("original ReviewQueue failed: %d", code)
	}
}
