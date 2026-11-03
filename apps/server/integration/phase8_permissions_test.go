//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
)

func createAccessToken(t *testing.T, f *fixture, principal uuid.UUID, scopes []string) string {
	t.Helper()
	token, hash, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	var workspace uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT workspace_id FROM qual.principals WHERE id=$1`, principal).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.credentials
  (id,workspace_id,principal_id,token_sha256,scopes,expires_at) VALUES($1,$2,$3,$4,$5,$6)`,
		uuid.New(), workspace, principal, hash[:], scopes, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return token
}
func principalFor(t *testing.T, f *fixture, token string) uuid.UUID {
	t.Helper()
	hash, err := identity.HashToken(token)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT principal_id FROM qual.credentials WHERE token_sha256=$1`, hash[:]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func createAuditor(t *testing.T, f *fixture) (uuid.UUID, string) {
	t.Helper()
	principal := uuid.New()
	var ws uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT workspace_id FROM qual.projects WHERE id=$1`, f.project).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.principals(id,workspace_id,kind,display_name)
  VALUES($1,$2,'agent','Audit Agent B')`, principal, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.memberships(id,workspace_id,project_id,principal_id,role)
  VALUES($1,$2,$3,$4,'project_viewer')`, uuid.New(), ws, f.project, principal); err != nil {
		t.Fatal(err)
	}
	return principal, createAccessToken(t, f, principal, []string{"audit:read"})
}
func accessURL(f *fixture) string {
	return f.server.URL + "/api/v1/projects/" + f.project.String() + "/access"
}
func setPermission(t *testing.T, f *fixture, token string, principal uuid.UUID, permission, effect string, version int64, key string) (int, map[string]any) {
	t.Helper()
	path := accessURL(f) + "/" + principal.String() + "/permissions/" + permission
	status, data, _ := request(t, "PUT", path, token, key, "", map[string]any{
		"effect": effect, "expected_version": version, "reason": "Explicit delegation for Phase 8 automated project policy test",
	})
	return status, data
}
func TestPhase8HumanAgentPermissionsAndImmediateRevocation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	idA := principalFor(t, f, f.agent)
	tokenA := createAccessToken(t, f, idA, []string{"work:read", "work:write", "work:review"})
	idB, tokenB := createAuditor(t, f)
	work := create(t, f, f.agent, "phase8-create-agent-review")
	workURL := f.url(f.project) + "/" + work["id"].(string)
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		status, item, _ := request(t, "POST", workURL+"/transitions", f.agent, fmt.Sprintf("phase8-before-%d", i), "", map[string]any{
			"status": next, "expected_version": i + 1,
		})
		if status != 200 {
			t.Fatalf("prepare work status %d %v", status, item)
		}
	}
	if status, _, _ := request(t, "POST", workURL+"/transitions", tokenA, "no-agent-review-before-grant", "",
		map[string]any{"status": "done", "expected_version": 4}); status != 403 {
		t.Fatalf("agent default review=%d", status)
	}
	if status, _, _ := request(t, "GET", f.server.URL+"/api/v1/projects/"+f.project.String()+"/audit", tokenB, "", "", nil); status != 403 {
		t.Fatalf("auditor default role incorrectly allowed: %d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f), f.agent, "", "", nil); status != 403 {
		t.Fatalf("original Agent listed access=%d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f), tokenB, "", "", nil); status != 403 {
		t.Fatalf("auditor listed users=%d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f)+"/me", tokenB, "", "", nil); status != 200 {
		t.Fatalf("audit-only self-service denied=%d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f), f.admin, "", "", nil); status != 200 {
		t.Fatalf("Human Admin cannot list permissions=%d", status)
	}

	status, permission := setPermission(t, f, f.admin, idA, "work:review", "allow", 0, "phase8-agentA-grant-review")
	if status != 200 || permission["version"] != float64(1) || permission["effect"] != "allow" {
		t.Fatalf("Human grant review %d %v", status, permission)
	}
	if status, _, _ := request(t, "POST", workURL+"/transitions", f.agent, "agent-scope-still-missing-review", "",
		map[string]any{"status": "done", "expected_version": 4}); status != 403 {
		t.Fatalf("old credential auto-escalated=%d", status)
	}
	// Scope+Project Allow gives Agent A permission to invoke the *explicit*
	// completion use case, not a bypass around Goose13 evidence/review gates.
	if status, _, _ := request(t, "POST", workURL+"/transitions", tokenA, "agent-legacy-done-denied", "",
		map[string]any{"status": "done", "expected_version": 4}); status != 422 {
		t.Fatalf("delegated Agent bypassed explicit Done Gate: %d", status)
	}
	completeWithIndependentEvidence(t, f, work["id"].(string), tokenA, "phase8-delegated-done")
	status, permission = setPermission(t, f, f.admin, idB, "audit:read", "allow", 0, "phase8-agentB-allow-audit")
	if status != 200 || permission["allowed_by_project"] != true {
		t.Fatalf("agent auditor grant=%d %v", status, permission)
	}
	auditURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/audit"
	if status, _, _ := request(t, "GET", auditURL, tokenB, "", "", nil); status != 200 {
		t.Fatalf("Agent B audit denied after grant %d", status)
	}
	if status, _, _ := request(t, "GET", workURL, tokenB, "", "", nil); status != 403 {
		t.Fatalf("audit-only B accessed WorkItem %d", status)
	}
	if status, _, _ := request(t, "POST", workURL+"/transitions", tokenB, "audit-cannot-transition-work", "",
		map[string]any{"status": "todo", "expected_version": 5}); status != 403 {
		t.Fatalf("audit-only B mutated work=%d", status)
	}
	if status, _, _ := request(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/audit", tokenB, "", "", nil); status != 404 {
		t.Fatalf("cross project audit scope leak %d", status)
	}
	// Deleting an override is not needed; explicit DENY is immediate and
	// preserved in history even while the old credential remains live.
	status, permission = setPermission(t, f, f.admin, idB, "audit:read", "deny", 1, "phase8-agentB-deny-audit")
	if status != 200 || permission["version"] != float64(2) {
		t.Fatalf("revoke auditor=%d %v", status, permission)
	}
	if status, _, _ := request(t, "GET", auditURL, tokenB, "", "", nil); status != 403 {
		t.Fatalf("revoked audit still authorized %d", status)
	}
	status, permission = setPermission(t, f, f.admin, idB, "audit:read", "deny", 1, "phase8-agentB-deny-audit")
	if status != 200 || permission["version"] != float64(2) {
		t.Fatalf("idempotent replay failed %d %v", status, permission)
	}
	if status, errBody := setPermission(t, f, f.admin, idB, "audit:read", "allow", 1, "phase8-agentB-deny-audit"); status != 409 || errBody["error"].(map[string]any)["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("same key different payload should conflict: %d %v", status, errBody)
	}
	if status, _ := setPermission(t, f, f.admin, idB, "audit:read", "allow", 1, "phase8-agentB-stale-version"); status != 409 {
		t.Fatalf("stale project grant mutated %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, idA, "work:review", "deny", 1, "phase8-agentA-revoke-review"); status != 200 {
		t.Fatalf("review deny failed %d", status)
	}
	// New WorkItem cannot be done even while tokenA still carries work:review.
	other := create(t, f, f.agent, "phase8-create-work-after-review-revoke")
	otherURL := f.url(f.project) + "/" + other["id"].(string)
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		if status, _, _ := request(t, "POST", otherURL+"/transitions", f.agent, fmt.Sprintf("phase8-after-%d", i), "",
			map[string]any{"status": next, "expected_version": i + 1}); status != 200 {
			t.Fatalf("prepare second work %d", status)
		}
	}
	if status, _, _ := request(t, "POST", otherURL+"/transitions", tokenA, "review-revoked-immediately", "",
		map[string]any{"status": "done", "expected_version": 4}); status != 403 {
		t.Fatalf("review revocation not immediate %d", status)
	}
	var history, audit, events int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.project_permission_changes
  WHERE project_id=$1`, f.project).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.audit_records
  WHERE project_id=$1 AND operation='permission.changed'`, f.project).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.outbox_events
  WHERE project_id=$1 AND event_type='permission.changed'`, f.project).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if history != 4 || audit != history || events != history {
		t.Fatalf("permission history/audit/outbox mismatch: %d/%d/%d", history, audit, events)
	}
	if _, err := f.pool.Exec(ctx, `DELETE FROM qual.project_permission_changes WHERE project_id=$1`, f.project); err == nil {
		t.Fatal("permission history is mutable")
	}
}
func TestPhase8AccessManageDelegationAndCAS(t *testing.T) {
	f := setup(t)
	agentA := principalFor(t, f, f.agent)
	agentB := principalFor(t, f, f.agent2)
	managerToken := createAccessToken(t, f, agentA, []string{"access:manage", "work:read"})
	if status, _, _ := request(t, "GET", accessURL(f), managerToken, "", "", nil); status != 403 {
		t.Fatalf("ungranted manager listed access %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, agentA, "access:manage", "allow", 0, "phase8-delegate-manager"); status != 200 {
		t.Fatalf("admin did not grant management %d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f), managerToken, "", "", nil); status != 200 {
		t.Fatalf("agent manager cannot list project permissions %d", status)
	}
	if status, _ := setPermission(t, f, managerToken, agentB, "work:review", "allow", 0, "phase8-manager-delegate-review"); status != 200 {
		t.Fatalf("delegated manager cannot change ordinary grants %d", status)
	}
	if status, _ := setPermission(t, f, managerToken, agentB, "access:manage", "allow", 0, "phase8-manager-attempt-escalation"); status != 403 {
		t.Fatalf("agent manager delegated management to another agent %d", status)
	}
	if status, _ := setPermission(t, f, managerToken, agentA, "work:write", "deny", 0, "phase8-manager-self-edit"); status != 403 {
		t.Fatalf("agent modified own project abilities %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, agentA, "access:manage", "deny", 1, "phase8-admin-revoke-manager"); status != 200 {
		t.Fatalf("admin revoke manager failed %d", status)
	}
	if status, _, _ := request(t, "GET", accessURL(f), managerToken, "", "", nil); status != 403 {
		t.Fatalf("revoked agent manager still has access %d", status)
	}
	// Different CAS writes for a single permission/version: exactly one wins.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, effect := range []string{"allow", "deny"} {
		wg.Add(1)
		go func(index int, e string) {
			defer wg.Done()
			statuses[index], _ = setPermission(t, f, f.admin, agentB, "audit:read", e, 0, fmt.Sprintf("phase8-race-%d", index))
		}(i, effect)
	}
	wg.Wait()
	winners := 0
	for _, code := range statuses {
		if code == 200 {
			winners++
		} else if code != 409 {
			t.Fatalf("unexpected race code %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("permission CAS winners=%d expected exactly one", winners)
	}
}

// Agent can also receive archive and webhook permissions when explicitly
// delegated. No event is delivered: a Worker must be separately launched.
func TestPhase8AgentDelegatedArchiveAndWebhookPermissions(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("private S3 required for delegated export")
	}
	principal := principalFor(t, f, f.agent)
	token := createAccessToken(t, f, principal, []string{"archive:export", "webhook:manage"})
	base := f.server.URL + "/api/v1/projects/" + f.project.String()
	if status, _, _ := request(t, "GET", base+"/exports", token, "", "", nil); status != 403 {
		t.Fatalf("Agent export default %d", status)
	}
	if status, _, _ := request(t, "GET", base+"/webhooks", token, "", "", nil); status != 403 {
		t.Fatalf("Agent webhook default %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, principal, "archive:export", "allow", 0, "phase8-agent-archive-allow"); status != 200 {
		t.Fatalf("archive grant %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, principal, "webhook:manage", "allow", 0, "phase8-agent-webhook-allow"); status != 200 {
		t.Fatalf("webhook grant %d", status)
	}
	if status, _, _ := request(t, "GET", base+"/exports", f.agent, "", "", nil); status != 403 {
		t.Fatalf("credential without archive scope elevated %d", status)
	}
	status, export, _ := request(t, "POST", base+"/exports", token, "phase8-privileged-agent-export", "", nil)
	if status != 201 || export["sha256"] == nil {
		t.Fatalf("agent export failed: %d %v", status, export)
	}
	status, webhook, _ := request(t, "POST", base+"/webhooks", token, "phase8-privileged-agent-webhook", "", map[string]any{
		"endpoint": "https://example.com/qual-events", "event_types": []string{"work_item.created"},
	})
	if status != 201 || webhook["id"] == nil {
		t.Fatalf("Agent webhook management failed: %d %v", status, webhook)
	}
	// The bearer must not be able to use the privileged API after live deny.
	if status, _ := setPermission(t, f, f.admin, principal, "archive:export", "deny", 1, "phase8-agent-archive-deny"); status != 200 {
		t.Fatal("archive deny failed")
	}
	if status, _ := setPermission(t, f, f.admin, principal, "webhook:manage", "deny", 1, "phase8-agent-webhook-deny"); status != 200 {
		t.Fatal("webhook deny failed")
	}
	if status, _, _ := request(t, "GET", base+"/exports", token, "", "", nil); status != 403 {
		t.Fatalf("agent export not revoked %d", status)
	}
	if status, _, _ := request(t, "GET", base+"/webhooks", token, "", "", nil); status != 403 {
		t.Fatalf("agent webhook not revoked %d", status)
	}
}
