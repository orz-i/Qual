//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func roleEndpoint(f *fixture, workID string) string {
	return f.url(f.project) + "/" + workID + "/role-entries"
}
func roleInput(capacity, outcome, kind, ref, summary string, version int) map[string]any {
	return map[string]any{"capacity": capacity, "outcome": outcome, "summary": summary,
		"reference_kind": kind, "reference_value": ref, "expected_version": version}
}
func TestPhase9SinglePrincipalMultiRoleAuditAndArchive(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "phase9-real-principal-multi-role")
	workID := work["id"].(string)
	path := roleEndpoint(f, workID)
	actorID := principalFor(t, f, f.agent)
	if status, _, _ := request(t, "GET", path, "", "", "", nil); status != 401 {
		t.Fatalf("anonymous role history status %d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject)+"/"+workID+"/role-entries", f.agent, "", "", nil); status != 404 {
		t.Fatalf("cross-project status %d", status)
	}
	if status, _, _ := request(t, "GET", path, f.viewer, "", "", nil); status != 200 {
		t.Fatalf("viewer role history %d", status)
	}

	executor := roleInput("executor", "observed", "git_commit", "4d6b9ee", "Actual implementation commit referenced; not executed by Qual", 1)
	status, execEntry, _ := request(t, "POST", path, f.agent, "phase9-executor-first", "", executor)
	if status != 201 || execEntry["actor_id"] != actorID.String() || execEntry["work_version"] != float64(2) {
		t.Fatalf("executor event %d %v", status, execEntry)
	}
	if execEntry["self_review"] != false {
		t.Fatal("executor incorrectly marked self-review")
	}
	status, replay, _ := request(t, "POST", path, f.agent, "phase9-executor-first", "", executor)
	if status != 201 || replay["id"] != execEntry["id"] {
		t.Fatalf("idempotent replay mutated entry: %d %v", status, replay)
	}
	badKey := roleInput("executor", "observed", "none", "", "different input with reused key", 1)
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-executor-first", "", badKey); status != 409 {
		t.Fatalf("same key conflicting payload accepted %d", status)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-no-real-test-ref", "",
		roleInput("tester", "passed", "none", "", "unverifiable result", 2)); status != 422 {
		t.Fatalf("tester passed without evidence accepted %d", status)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-foreign-run-ref", "",
		roleInput("tester", "passed", "test_run", uuid.NewString(), "foreign source should not pass", 2)); status != 404 {
		t.Fatalf("missing or cross-project TestRun accepted %d", status)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-stale-version", "",
		roleInput("executor", "observed", "none", "", "stale role should not persist", 1)); status != 409 {
		t.Fatalf("stale version accepted %d", status)
	}
	status, tester, _ := request(t, "POST", path, f.agent, "phase9-tester-second", "",
		roleInput("tester", "passed", "git_commit", "4d6b9ee", "Actual external check; source is a Git reference", 2))
	if status != 201 || tester["actor_id"] != actorID.String() {
		t.Fatalf("tester role %d %v", status, tester)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-no-review-scope", "",
		roleInput("reviewer", "passed", "git_commit", "4d6b9ee", "Cannot impersonate reviewer", 3)); status != 403 {
		t.Fatalf("ordinary PI Agent self-review unexpectedly allowed %d", status)
	}
	privileged := createAccessToken(t, f, actorID, []string{"work:read", "work:write", "work:review", "test:write", "audit:read"})
	status, _ = setPermission(t, f, f.admin, actorID, "work:review", "allow", 0, "phase9-reviewer-grant")
	if status != 200 {
		t.Fatalf("grant agent review %d", status)
	}
	status, _ = setPermission(t, f, f.admin, actorID, "audit:read", "allow", 0, "phase9-auditor-grant")
	if status != 200 {
		t.Fatalf("grant agent audit %d", status)
	}
	if status, _, _ := request(t, "POST", path, f.agent, "phase9-old-token-review-still-denied", "",
		roleInput("reviewer", "passed", "git_commit", "4d6b9ee", "Old token must never auto-elevate", 3)); status != 403 {
		t.Fatalf("old Agent token got review role %d", status)
	}
	status, review, _ := request(t, "POST", path, privileged, "phase9-actual-self-review", "",
		roleInput("reviewer", "passed", "git_commit", "4d6b9ee", "Explicit self-review: not independent approval", 3))
	if status != 201 || review["actor_id"] != actorID.String() || review["self_review"] != true {
		t.Fatalf("self-review marker missing: %d %v", status, review)
	}
	if review["work_status"] != "backlog" {
		t.Fatal("review record unexpectedly transitioned WorkItem")
	}
	status, audit, _ := request(t, "POST", path, privileged, "phase9-actual-audit", "",
		roleInput("auditor", "observed", "none", "", "Observed immutable event and role history under scoped audit read", 4))
	if status != 201 || audit["actor_id"] != actorID.String() {
		t.Fatalf("auditor role %d %v", status, audit)
	}

	// Auditor-only PAT is genuinely read-only; it cannot write audit conclusions.
	auditorID, auditorToken := createAuditor(t, f)
	if status, _ := setPermission(t, f, f.admin, auditorID, "audit:read", "allow", 0, "phase9-audit-only-grant"); status != 200 {
		t.Fatal("grant audit-only")
	}
	if status, _, _ := request(t, "POST", path, auditorToken, "phase9-audit-only-cannot-write", "",
		roleInput("auditor", "observed", "none", "", "No write scope", 5)); status != 403 {
		t.Fatalf("audit read-only principal wrote role event %d", status)
	}
	status, independent, _ := request(t, "POST", path, f.admin, "phase9-human-independent-review", "",
		roleInput("reviewer", "passed", "git_commit", "4d6b9ee", "Independent Human reviewed the same facts", 5))
	if status != 201 || independent["self_review"] != false {
		t.Fatalf("independent reviewer flagged self %d %v", status, independent)
	}
	if status, current, _ := request(t, "GET", f.url(f.project)+"/"+workID, f.agent, "", "", nil); status != 200 ||
		current["version"] != float64(6) || current["status"] != "backlog" {
		t.Fatalf("Role record altered status or wrong version %d %v", status, current)
	}

	status, page, _ := request(t, "GET", path, f.agent, "", "", nil)
	if status != 200 || len(page["items"].([]any)) != 5 {
		t.Fatalf("role list %d %v", status, page)
	}
	for _, v := range page["items"].([]any) {
		entry := v.(map[string]any)
		if entry["actor_id"] != actorID.String() && entry["actor_id"] != principalFor(t, f, f.admin).String() {
			t.Fatal("role entry invented a Principal")
		}
	}

	ctx := context.Background()
	var records, changes, activity, outbox int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.work_role_entries WHERE work_item_id=$1`, workID).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE entity_id=$1 AND operation='work_item.role_recorded'`, workID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.activity_records WHERE entity_id=$1 AND operation='work_item.role_recorded'`, workID).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE aggregate_id=$1 AND event_type='work_item.role_recorded'`, workID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if records != 5 || changes != 5 || activity != 5 || outbox != 5 {
		t.Fatalf("role/audit/activity/outbox=%d/%d/%d/%d", records, changes, activity, outbox)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE qual.work_role_entries SET summary='tampered' WHERE work_item_id=$1`, workID); err == nil {
		t.Fatal("role history UPDATE accepted")
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM qual.work_role_entries WHERE work_item_id=$1`, workID); err == nil {
		t.Fatal("role history DELETE accepted")
	}
	if f.objects != nil {
		base := evidenceURL(f)
		status, export := testRequest(t, "POST", base+"/exports", f.admin, "phase9-role-archive-export", "", nil)
		if status != 201 {
			t.Fatalf("export status %d %v", status, export)
		}
		status, body := rawHTTP(t, "GET", base+"/exports/"+export["id"].(string)+"/content", f.admin, "", nil, "")
		if status != 200 {
			t.Fatalf("export read HTTP %d", status)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, file := range zr.File {
			if file.Name == "work_role_entries.jsonl" {
				found = true
				rc, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(data, []byte(execEntry["id"].(string))) || bytes.Count(data, []byte("\n")) != 5 {
					t.Fatalf("project ZIP missed immutable role records: %s", data)
				}
			}
		}
		if !found {
			t.Fatal("export missing work_role_entries.jsonl")
		}
	}
}

func TestPhase9RoleRecordsConcurrentVersionAndInvalidMetadata(t *testing.T) {
	f := setup(t)
	item := create(t, f, f.agent, "phase9-invalid-role")
	url := roleEndpoint(f, item["id"].(string))
	for i, req := range []map[string]any{
		roleInput("fake-person", "observed", "none", "", "cannot impersonate another Actor", 1),
		roleInput("tester", "passed", "none", "", "faked pass", 1),
		roleInput("executor", "observed", "git_commit", "not-a-git-hash", "invalid git", 1),
		roleInput("executor", "observed", "none", "something", "unexpected reference", 1),
	} {
		if status, _, _ := request(t, "POST", url, f.agent, fmt.Sprintf("phase9-invalid-%d", i), "", req); status != 422 {
			t.Fatalf("invalid role input accepted %d: %d", i, status)
		}
	}
	invalidActor := roleInput("executor", "observed", "none", "", "Injected Actor should be rejected", 1)
	invalidActor["actor_id"] = uuid.NewString()
	if status, _, _ := request(t, "POST", url, f.agent, "phase9-fake-actor", "", invalidActor); status != 400 {
		t.Fatalf("untrusted actor accepted %d", status)
	}
	// Two concurrent real writers using the same optimistic version cannot
	// both append an independently versioned role record.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			codes[index], _, _ = request(t, "POST", url, f.agent, fmt.Sprintf("phase9-role-race-%d", index), "",
				roleInput("executor", "observed", "none", "", fmt.Sprintf("Actual concurrent writer %d", index), 1))
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, c := range codes {
		if c == 201 {
			wins++
		} else if c != 409 {
			t.Fatalf("unexpected CAS status %d", c)
		}
	}
	if wins != 1 {
		t.Fatalf("CAS allowed %d role writes", wins)
	}
}
