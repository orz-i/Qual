//go:build integration

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// QUAL-56 tests use only isolated migrated PostgreSQL and synthetic identities.
// No default-project policy, credential or WorkItem is changed.
func qual56SeedScoped(t *testing.T, f *fixture) {
	t.Helper()
	var installed bool
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Skip("Goose18 task policy required")
	}
	var ws uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	_, err := f.pool.Exec(ctx, `INSERT INTO qual.project_task_authorization_policies
		(workspace_id,project_id,mode,version,changed_by)
		VALUES($1,$2,'task_scoped',1,$3)`, ws, f.project, principalFor(t, f, f.admin))
	if err != nil {
		t.Fatal(err)
	}
}
func qual56Write(t *testing.T, f *fixture, token, itemID, operation, key string, version int, newPrincipal uuid.UUID) (int, map[string]any) {
	t.Helper()
	body := map[string]any{"expected_version": version}
	if operation == "reassign" {
		body["new_assignee_id"] = newPrincipal.String()
	}
	code, result, _ := request(t, "POST", f.url(f.project)+"/"+itemID+"/"+operation, token, key, "", body)
	return code, result
}
func qual56Claim(t *testing.T, f *fixture, token, itemID, key string, version int) map[string]any {
	t.Helper()
	code, data := qual56Write(t, f, token, itemID, "claim", key, version, uuid.Nil)
	if code != 200 {
		t.Fatalf("claim status %d data %v", code, data)
	}
	return data
}
func qual56Status(t *testing.T, f *fixture, token, itemID, status, key string, version int) map[string]any {
	t.Helper()
	code, data, _ := request(t, "POST", f.url(f.project)+"/"+itemID+"/transitions", token, key, "", map[string]any{
		"status": status, "expected_version": version,
	})
	if code != 200 {
		t.Fatalf("transition to %s status %d data %v", status, code, data)
	}
	return data
}
func qual56CheckVersion(t *testing.T, data map[string]any, version int, assignee *uuid.UUID) {
	t.Helper()
	if data["version"] != float64(version) {
		t.Fatalf("work version %v expected %d", data["version"], version)
	}
	if assignee == nil {
		if data["assignee_id"] != nil {
			t.Fatalf("expected unassigned task; got %v", data["assignee_id"])
		}
	} else if data["assignee_id"] != assignee.String() {
		t.Fatalf("assignee %v want %s", data["assignee_id"], assignee)
	}
}

// An Agent with only base work:read/work:claim can explicitly transfer a task
// to an eligible Agent B; the original token cannot edit the task afterwards.
// Release -> unassigned in_progress -> resumed claim preserves the task's
// durable context without granting any new Principal or project-level Scope.
func TestQual56ScopedReleaseReassignAndResumption(t *testing.T) {
	f := setup(t)
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	ta := createAccessToken(t, f, a, []string{"work:read", "work:claim"})
	tb := createAccessToken(t, f, b, []string{"work:read", "work:claim"})
	item := create(t, f, f.agent, "qual56-scoped-item")
	other := create(t, f, f.agent, "qual56-scoped-unclaimed-other")
	id := item["id"].(string)
	path := f.url(f.project) + "/" + id
	qual56SeedScoped(t, f)
	qual56CheckVersion(t, qual56Claim(t, f, ta, id, "qual56-scoped-claim-a", 1), 2, &a)
	code, moved := qual56Write(t, f, ta, id, "reassign", "qual56-scoped-a-to-b", 2, b)
	if code != 200 {
		t.Fatalf("transfer A->B failed: %d %v", code, moved)
	}
	qual56CheckVersion(t, moved, 3, &b)
	// Same authenticated request/key replays original version without writing.
	code, replay := qual56Write(t, f, ta, id, "reassign", "qual56-scoped-a-to-b", 2, b)
	if code != 200 || replay["version"] != float64(3) {
		t.Fatalf("idempotent reassign failed: %d %v", code, replay)
	}
	code, _ = qual56Write(t, f, ta, id, "reassign", "qual56-scoped-a-to-b", 2, a)
	if code != 409 {
		t.Fatalf("same key different target was not rejected %d", code)
	}
	code, _ = qual56Write(t, f, ta, id, "reassign", "qual56-stale-a-transfer", 2, b)
	if code != 409 {
		t.Fatalf("stale assignee version was accepted %d", code)
	}
	code, _, _ = request(t, "PATCH", path, ta, "qual56-old-a-denied-edit", "\"3\"", map[string]any{"title": "Cannot edit after transfer"})
	if code != 403 {
		t.Fatalf("old agent still edits task %d", code)
	}
	code, _, _ = request(t, "POST", path+"/transitions", ta, "qual56-old-a-denied-transition", "", map[string]any{"status": "todo", "expected_version": 3})
	if code != 403 {
		t.Fatalf("old agent still moves task %d", code)
	}
	code, _, _ = request(t, "PATCH", f.url(f.project)+"/"+other["id"].(string), tb, "qual56-b-other-denied", "\"1\"", map[string]any{"title": "Other unassigned work"})
	if code != 403 {
		t.Fatalf("new agent modified unrelated task: %d", code)
	}
	code, patched, _ := request(t, "PATCH", path, tb, "qual56-b-edit", "\"3\"", map[string]any{"title": "Agent B picked up work"})
	if code != 200 {
		t.Fatalf("new agent cannot edit after transfer %d %v", code, patched)
	}
	qual56CheckVersion(t, patched, 4, &b)
	code, released := qual56Write(t, f, tb, id, "release", "qual56-b-releases", 4, uuid.Nil)
	if code != 200 {
		t.Fatalf("release backlog failed %d %v", code, released)
	}
	qual56CheckVersion(t, released, 5, nil)
	code, _, _ = request(t, "PATCH", path, tb, "qual56-b-no-write-after-release", "\"5\"", map[string]any{"title": "After release"})
	if code != 403 {
		t.Fatalf("released agent can write task %d", code)
	}
	qual56CheckVersion(t, qual56Claim(t, f, ta, id, "qual56-a-reclaims", 5), 6, &a)
	qual56Status(t, f, ta, id, "todo", "qual56-to-todo", 6)
	qual56Status(t, f, ta, id, "in_progress", "qual56-to-progress", 7)
	code, released = qual56Write(t, f, ta, id, "release", "qual56-a-release-progress", 8, uuid.Nil)
	if code != 200 {
		t.Fatalf("release in progress failed %d %v", code, released)
	}
	qual56CheckVersion(t, released, 9, nil)
	if released["status"] != "in_progress" {
		t.Fatal("release rewrote status")
	}
	qual56CheckVersion(t, qual56Claim(t, f, tb, id, "qual56-b-resume-progress", 9), 10, &b)
	qual56Status(t, f, tb, id, "in_review", "qual56-b-submit-review", 10)
	code, _ = qual56Write(t, f, tb, id, "release", "qual56-review-release-denied", 11, uuid.Nil)
	if code != 403 && code != 422 {
		t.Fatalf("release during review accepted %d", code)
	}
	code, _ = qual56Write(t, f, tb, id, "reassign", "qual56-review-reassign-denied", 11, a)
	if code != 403 && code != 422 {
		t.Fatalf("reassign during review accepted %d", code)
	}
	code, _, _ = request(t, "PATCH", path, tb, "qual56-review-edit-denied", "\"11\"", map[string]any{"title": "Mutate during review"})
	if code != 403 {
		t.Fatalf("review freeze breached %d", code)
	}
	// Two transfer events carry real before/after assignee IDs; failed events
	// never appear in either Audit or Outbox.
	ctx := context.Background()
	var beforeAfter []byte
	if err := f.pool.QueryRow(ctx, `SELECT diff FROM qual.audit_records
		WHERE project_id=$1 AND entity_id=$2 AND operation='work_item.reassign'`, f.project, id).Scan(&beforeAfter); err != nil {
		t.Fatal(err)
	}
	if !containsTransferIDs(beforeAfter, a, b) {
		t.Fatalf("missing assignee audit provenance: %s", beforeAfter)
	}
	var auditCount, outboxCount int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2
		AND operation IN('work_item.release','work_item.reassign')`, f.project, id).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2
		AND event_type IN('work_item.release','work_item.reassign')`, f.project, id).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 || outboxCount != auditCount {
		t.Fatalf("transfer release audit/outbox %d/%d", auditCount, outboxCount)
	}
	var activityCount int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.activity_records
		WHERE project_id=$1 AND entity_id=$2 AND operation IN('work_item.release','work_item.reassign')`,
		f.project, id).Scan(&activityCount); err != nil {
		t.Fatal(err)
	}
	if activityCount != auditCount {
		t.Fatalf("transfer Activity/Audit mismatch %d/%d", activityCount, auditCount)
	}
	t.Log("task-scoped A->B, B release, A reclaim/release, B resume/in_review; old principal immediately denied")
}

func containsTransferIDs(diff []byte, a, b uuid.UUID) bool {
	return strings.Contains(string(diff), a.String()) && strings.Contains(string(diff), b.String())
}

// The manager exemption is not a second self-service permission. It always
// requires active access:manage, and can only appoint an eligible real
// participant, never an integration reporter, viewer or foreign Principal.
func TestQual56ManagerRecoveryAndRecipientEligibility(t *testing.T) {
	f := setup(t)
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-manager-scope")
	id := item["id"].(string)
	path := f.url(f.project) + "/" + id
	qual56SeedScoped(t, f)
	qual56Claim(t, f, f.agent, id, "qual56-manager-claim-a", 1)
	code, _ := qual56Write(t, f, f.agent2, id, "reassign", "qual56-b-takeover-denied", 2, b)
	if code != 403 {
		t.Fatalf("unclaimed agent took task %d", code)
	}
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-foreign-denied", 2, uuid.New())
	if code != 404 {
		t.Fatalf("foreign identity accepted %d", code)
	}
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-viewer-denied", 2, principalFor(t, f, f.viewer))
	if code != 404 {
		t.Fatalf("viewer lacking claim credential accepted %d", code)
	}
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-integration-denied", 2, principalFor(t, f, f.reporter))
	if code != 404 {
		t.Fatalf("reporting integration accepted %d", code)
	}
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-same-denied", 2, a)
	if code != 422 {
		t.Fatalf("reassign to current owner accepted %d", code)
	}
	// Project explicit Deny on receiver is respected by manager as well.
	if c, _ := setPermission(t, f, f.admin, b, "work:claim", "deny", 0, "qual56-target-deny"); c != 200 {
		t.Fatal(c)
	}
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-denied-target", 2, b)
	if code != 403 {
		t.Fatalf("manager bypassed target Deny %d", code)
	}
	if c, _ := setPermission(t, f, f.admin, b, "work:claim", "inherit", 1, "qual56-target-inherit"); c != 200 {
		t.Fatal(c)
	}
	code, moved := qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-rescues", 2, b)
	if code != 200 {
		t.Fatalf("manager recovery failed %d %v", code, moved)
	}
	qual56CheckVersion(t, moved, 3, &b)
	code, _ = qual56Write(t, f, f.agent, id, "release", "qual56-former-a-release-denied", 3, uuid.Nil)
	if code != 403 {
		t.Fatalf("old Agent A released transferred task %d", code)
	}
	// Current owner resumes work; lifecycle remains governed by existing
	// state machine. Manager may not bypass the review freeze.
	qual56Status(t, f, f.agent2, id, "todo", "qual56-b-to-todo", 3)
	qual56Status(t, f, f.agent2, id, "in_progress", "qual56-b-to-progress", 4)
	qual56Status(t, f, f.agent2, id, "in_review", "qual56-b-to-review", 5)
	code, _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-manager-review-denied", 6, a)
	if code != 422 {
		t.Fatalf("manager bypassed review state freeze %d", code)
	}
	// Explicit transfer manager can hold access:manage alone; it need not
	// impersonate executor or receive work:claim from a task relationship.
	var auditActor string
	if err := f.pool.QueryRow(context.Background(), `SELECT actor_id::text FROM qual.audit_records
		WHERE project_id=$1 AND entity_id=$2 AND operation='work_item.reassign'`, f.project, id).Scan(&auditActor); err != nil {
		t.Fatal(err)
	}
	if auditActor != principalFor(t, f, f.admin).String() {
		t.Fatalf("audit did not retain real manager: %s", auditActor)
	}
	_ = path
}

// Two competing requests at exactly the released in_progress version must
// serialize through the project/WorkItem transaction; precisely one wins.
func TestQual56ClaimAndManagerReassignRace(t *testing.T) {
	f := setup(t)
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-claim-reassign-race")
	id := item["id"].(string)
	qual56SeedScoped(t, f)
	qual56Claim(t, f, f.agent, id, "qual56-race-start-claim", 1)
	qual56Status(t, f, f.agent, id, "todo", "qual56-race-to-todo", 2)
	qual56Status(t, f, f.agent, id, "in_progress", "qual56-race-to-progress", 3)
	if code, _ := qual56Write(t, f, f.agent, id, "release", "qual56-race-release", 4, uuid.Nil); code != 200 {
		t.Fatalf("release %d", code)
	}
	results := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], _ = qual56Write(t, f, f.agent2, id, "claim", "qual56-race-claim-b", 5, uuid.Nil)
	}()
	go func() {
		defer wg.Done()
		results[1], _ = qual56Write(t, f, f.admin, id, "reassign", "qual56-race-manager-a", 5, a)
	}()
	wg.Wait()
	if !((results[0] == 200 && results[1] == 409) || (results[1] == 200 && results[0] == 409)) {
		t.Fatalf("expected one 200 one 409, got %v", results)
	}
	var version int
	var assigned uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT version,assignee_id FROM qual.work_items
		WHERE id=$1 AND project_id=$2`, id, f.project).Scan(&version, &assigned); err != nil {
		t.Fatal(err)
	}
	if version != 6 || (assigned != a && assigned != b) {
		t.Fatalf("race inconsistent v%d assignee=%s", version, assigned)
	}
	var audits, outbox int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records
		WHERE project_id=$1 AND entity_id=$2 AND operation IN('work_item.claim','work_item.reassign')`,
		f.project, id).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.outbox_events
		WHERE project_id=$1 AND aggregate_id=$2 AND event_type IN('work_item.claim','work_item.reassign')`,
		f.project, id).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if audits != 2 || outbox != 2 {
		t.Fatalf("claim/reassign race Audit/Outbox=%d/%d", audits, outbox)
	}
	t.Logf("race 200/409, final v%d, real assignee %s, 2/2 Audit/Outbox", version, assigned)
}

// Legacy project keeps existing Claim logic; the new transfer endpoints are
// independently scoped and auditable without requiring Goose18 at all.
func TestQual56LegacyReleaseAndReassignCompatibility(t *testing.T) {
	f := setup(t)
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-legacy-compat")
	id := item["id"].(string)
	qual56Claim(t, f, f.agent, id, "qual56-legacy-claim", 1)
	if code, _ := qual56Write(t, f, f.agent2, id, "release", "qual56-legacy-b-denied", 2, uuid.Nil); code != 403 {
		t.Fatalf("non-owner legacy release %d", code)
	}
	code, released := qual56Write(t, f, f.agent, id, "release", "qual56-legacy-a-release", 2, uuid.Nil)
	if code != 200 {
		t.Fatalf("legacy release failed %d %v", code, released)
	}
	qual56CheckVersion(t, released, 3, nil)
	qual56Claim(t, f, f.agent2, id, "qual56-legacy-claim-b", 3)
	if code, _ := qual56Write(t, f, f.agent, id, "reassign", "qual56-legacy-old-a-denied", 4, a); code != 403 {
		t.Fatalf("legacy former assignee transfer %d", code)
	}
	if code, data := qual56Write(t, f, f.agent2, id, "reassign", "qual56-legacy-b-to-a", 4, a); code != 200 || data["assignee_id"] != a.String() {
		t.Fatalf("legacy transfer failed %d %v", code, data)
	}
	// Legacy project wide work:write remains intentionally unchanged.
	status, _, _ := request(t, "PATCH", f.url(f.project)+"/"+id, f.agent2, "qual56-legacy-b-workwrite", "\"5\"", map[string]any{"title": "Legacy project-wide write remains"})
	if status != 200 {
		t.Fatalf("legacy broad work:write changed %d", status)
	}
	// When a legacy in-progress assignee explicitly releases, Claim still
	// uses its original backlog/todo-only gate; authorized manager can rescue.
	next := create(t, f, f.agent, "qual56-legacy-progress-rescue")
	nextID := next["id"].(string)
	qual56Claim(t, f, f.agent, nextID, "qual56-legacy-progress-claim", 1)
	qual56Status(t, f, f.agent, nextID, "todo", "qual56-legacy-to-todo", 2)
	qual56Status(t, f, f.agent, nextID, "in_progress", "qual56-legacy-to-progress", 3)
	if code, _ := qual56Write(t, f, f.agent, nextID, "release", "qual56-legacy-progress-release", 4, uuid.Nil); code != 200 {
		t.Fatal(code)
	}
	if code, _ := qual56Write(t, f, f.agent2, nextID, "claim", "qual56-legacy-progress-claim-not-allowed", 5, uuid.Nil); code != 422 {
		t.Fatalf("legacy claim behavior changed %d", code)
	}
	if code, data := qual56Write(t, f, f.admin, nextID, "reassign", "qual56-legacy-manager-recovery", 5, b); code != 200 || data["version"] != float64(6) {
		t.Fatalf("legacy manager recovery failed %d %v", code, data)
	}
}

// Negative tests for all new transport preconditions and same-project target
// eligibility. Denials must not change the WorkItem, Audit or Outbox.
func TestQual56TransferHTTPGuardsAndTargetRevocation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-negative-input")
	id := item["id"].(string)
	qual56SeedScoped(t, f)
	qual56Claim(t, f, f.agent, id, "qual56-negative-claim", 1)
	path := f.url(f.project) + "/" + id
	for _, c := range []struct {
		name, method, token, key, uri string
		body                          any
		want                          int
	}{
		{"release missing idempotency", "POST", f.agent, "", path + "/release", map[string]any{"expected_version": 2}, 428},
		{"reassign missing idempotency", "POST", f.agent, "", path + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": b.String()}, 428},
		{"release missing version", "POST", f.agent, "qual56-missing-release-v", path + "/release", map[string]any{}, 428},
		{"reassign zero version", "POST", f.agent, "qual56-zero-reassign-v", path + "/reassign", map[string]any{"expected_version": 0, "new_assignee_id": b.String()}, 428},
		{"reassign unknown field", "POST", f.agent, "qual56-extra-payload", path + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": b.String(), "actor_id": a.String()}, 400},
		{"reassign unknown UUID", "POST", f.agent, "qual56-bad-uuid", path + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": "someone"}, 400},
		{"release unauthenticated", "POST", "", "qual56-no-auth-release", path + "/release", map[string]any{"expected_version": 2}, 401},
		{"reassign unauthenticated", "POST", "", "qual56-no-auth-reassign", path + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": b.String()}, 401},
		{"release cross-project", "POST", f.agent, "qual56-foreign-release", f.url(f.otherProject) + "/" + id + "/release", map[string]any{"expected_version": 2}, 404},
		{"reassign cross-project", "POST", f.agent, "qual56-foreign-reassign", f.url(f.otherProject) + "/" + id + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": b.String()}, 404},
		{"release stale version", "POST", f.agent, "qual56-stale-release", path + "/release", map[string]any{"expected_version": 1}, 409},
		{"reassign stale version", "POST", f.agent, "qual56-stale-reassign", path + "/reassign", map[string]any{"expected_version": 1, "new_assignee_id": b.String()}, 409},
		{"reassign nil UUID", "POST", f.agent, "qual56-empty-reassign", path + "/reassign", map[string]any{"expected_version": 2, "new_assignee_id": "00000000-0000-0000-0000-000000000000"}, 422},
	} {
		status, _, _ := request(t, c.method, c.uri, c.token, c.key, "", c.body)
		if status != c.want {
			t.Errorf("%s: HTTP %d, want %d", c.name, status, c.want)
		}
	}
	// Recipient must have an active non-revoked participating credential, not
	// merely a known Principal UUID.
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE principal_id=$1", b); err != nil {
		t.Fatal(err)
	}
	if status, _ := qual56Write(t, f, f.agent, id, "reassign", "qual56-revoked-target", 2, b); status != 404 {
		t.Fatalf("revoked target accepted: %d", status)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.principals SET disabled_at=now() WHERE id=$1", b); err != nil {
		t.Fatal(err)
	}
	if status, _ := qual56Write(t, f, f.admin, id, "reassign", "qual56-disabled-target", 2, b); status != 404 {
		t.Fatalf("disabled target accepted: %d", status)
	}
	// Explicit deny on assignee's work:claim also revokes the power to
	// voluntarily transfer or release unless they separately have access:manage.
	if code, _ := setPermission(t, f, f.admin, a, "work:claim", "deny", 0, "qual56-source-claim-denied"); code != 200 {
		t.Fatal(code)
	}
	if status, _ := qual56Write(t, f, f.agent, id, "release", "qual56-denied-source-release", 2, uuid.Nil); status != 403 {
		t.Fatalf("source Deny bypassed release %d", status)
	}
	if status, _ := qual56Write(t, f, f.agent, id, "reassign", "qual56-denied-source-reassign", 2, a); status != 403 {
		t.Fatalf("source Deny bypassed reassign %d", status)
	}
	var version, count int
	if err := f.pool.QueryRow(ctx, "SELECT version FROM qual.work_items WHERE id=$1 AND project_id=$2", id, f.project).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2
		AND operation IN ('work_item.release','work_item.reassign')`, f.project, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if version != 2 || count != 0 {
		t.Fatalf("denied requests touched work version=%d audit=%d", version, count)
	}
}

// Two simultaneous requests by the actual executor to release/reassign the
// same task cannot both commit; the second receives 409 and no audit entry.
func TestQual56ReleaseVersusReassignCASRace(t *testing.T) {
	f := setup(t)
	b := principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-release-transfer-race")
	id := item["id"].(string)
	qual56SeedScoped(t, f)
	qual56Claim(t, f, f.agent, id, "qual56-release-race-claim", 1)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0], _ = qual56Write(t, f, f.agent, id, "release", "qual56-race-release", 2, uuid.Nil)
	}()
	go func() {
		defer wg.Done()
		codes[1], _ = qual56Write(t, f, f.agent, id, "reassign", "qual56-race-reassign", 2, b)
	}()
	wg.Wait()
	if !((codes[0] == 200 && codes[1] == 409) || (codes[0] == 409 && codes[1] == 200)) {
		t.Fatalf("release/reassign race %v", codes)
	}
	var version int
	if err := f.pool.QueryRow(context.Background(), "SELECT version FROM qual.work_items WHERE id=$1", id).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("both mutated race version %d", version)
	}
	var audit, outbox int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2
	AND operation IN('work_item.release','work_item.reassign')`, f.project, id).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND aggregate_id=$2
	AND event_type IN('work_item.release','work_item.reassign')`, f.project, id).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if audit != 1 || outbox != 1 {
		t.Fatalf("race has duplicate history: %d/%d", audit, outbox)
	}
}
