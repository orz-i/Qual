//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPhase7PATAuthoritativeRotationWithIsolatedDatabase exercises the actual
// offline administrator issue-existing/revoke commands against ONLY a throwaway
// PostgreSQL clone. The real QUAL PI PAT and its backing file are never read,
// changed, or passed into the test. The operator role is simulated by a test
// database admin connection, not an authenticated Human acceptance.
func TestPhase7PATAuthoritativeRotationWithIsolatedDatabase(t *testing.T) {
	dbURL := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(dbURL, "/qual5_rotate_") {
		t.Skip("rotation exercise requires a disposable qual5_rotate_* PostgreSQL database")
	}
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	oldToken := f.agent
	agent := principalFor(t, f, oldToken)
	var workspace, project string
	if err := f.pool.QueryRow(ctx, `SELECT w.slug, p.key FROM qual.projects p
        JOIN qual.workspaces w ON w.id=p.workspace_id WHERE p.id=$1`, f.project).
		Scan(&workspace, &project); err != nil {
		t.Fatal(err)
	}
	var beforePrincipals int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.principals`).Scan(&beforePrincipals); err != nil {
		t.Fatal(err)
	}
	var originalScopes []string
	if err := f.pool.QueryRow(ctx, `SELECT scopes FROM qual.credentials WHERE id=$1`, f.agentCredential).
		Scan(&originalScopes); err != nil {
		t.Fatal(err)
	}
	expectedScopes := []string{
		"work:read", "work:write", "work:claim",
		"test:read", "test:write", "artifact:read", "artifact:write",
	}
	if !reflect.DeepEqual(originalScopes, expectedScopes) {
		t.Fatalf("fixture scopes changed; expected seven least-privilege scopes, received %d", len(originalScopes))
	}

	work := create(t, f, oldToken, "isolated agent PAT rotation continuity")
	workURL := f.url(f.project) + "/" + work["id"].(string)
	accessURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/access/me"
	if status, _, _ := request(t, "GET", workURL, oldToken, "", "", nil); status != 200 {
		t.Fatalf("pre-rotation Agent read=%d", status)
	}

	bin := filepath.Join(t.TempDir(), "qual-admin")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual-admin")
	if _, err := build.CombinedOutput(); err != nil {
		t.Fatalf("offline admin build failed (output redacted): %v", err)
	}
	operator := func(args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, bin, args...)
		command.Env = append(os.Environ(), "DATABASE_URL="+dbURL)
		// This output can contain a one-time PAT: never log it, even on failure.
		return command.CombinedOutput()
	}

	output, err := operator("issue-existing",
		"-workspace", workspace, "-project", project,
		"-principal", agent.String(), "-scopes", strings.Join(expectedScopes, ","),
		"-days", "7")
	if err != nil {
		t.Fatalf("test-only issue-existing failed; output redacted: %v", err)
	}
	patRE := regexp.MustCompile(`qual_pat_[a-f0-9]{64}`)
	matches := patRE.FindAllString(string(output), -1)
	if len(matches) != 1 || matches[0] == oldToken {
		t.Fatal("new credential missing, duplicated, or same as old credential (value redacted)")
	}
	newToken := matches[0]
	idRE := regexp.MustCompile(`Credential ID: ([0-9a-f-]{36})`)
	rawID := idRE.FindStringSubmatch(string(output))
	if len(rawID) != 2 {
		t.Fatal("offline issuance did not report a credential UUID")
	}
	newCredential, err := uuid.Parse(rawID[1])
	if err != nil || newCredential == uuid.Nil || newCredential == f.agentCredential {
		t.Fatal("new credential ID invalid or reuses old ID")
	}
	// Forget the full one-time CLI response as soon as it has been parsed.
	output = nil
	matches = nil

	if principalFor(t, f, newToken) != agent {
		t.Fatal("rotation created a different Agent Principal")
	}
	var afterPrincipals int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.principals`).Scan(&afterPrincipals); err != nil {
		t.Fatal(err)
	}
	if afterPrincipals != beforePrincipals {
		t.Fatal("token rotation created a fake additional Agent")
	}
	var newScopes []string
	var validOld, validNew bool
	if err := f.pool.QueryRow(ctx, `SELECT scopes, revoked_at IS NULL FROM qual.credentials WHERE id=$1`, newCredential).
		Scan(&newScopes, &validNew); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM qual.credentials WHERE id=$1`, f.agentCredential).
		Scan(&validOld); err != nil {
		t.Fatal(err)
	}
	if !validOld || !validNew || !reflect.DeepEqual(newScopes, expectedScopes) {
		t.Fatal("overlap phase lacks two active, identically scoped credentials")
	}
	// Overlap phase: no downtime; both credentials can read identical history.
	for _, token := range []string{oldToken, newToken} {
		if status, item, _ := request(t, "GET", workURL, token, "", "", nil); status != 200 ||
			item["id"] != work["id"] || item["version"] != work["version"] {
			t.Fatalf("overlap phase identity/version mismatch %d", status)
		}
		if status, access, _ := request(t, "GET", accessURL, token, "", "", nil); status != 200 ||
			!hasExactlyScopedPermissions(access, expectedScopes) {
			t.Fatalf("overlap phase enlarged Agent permissions %d", status)
		}
	}
	for name, token := range map[string]string{"old": oldToken, "new": newToken} {
		if status, _, _ := request(t, "GET", f.url(f.otherProject), token, "", "", nil); status != 404 {
			t.Fatalf("%s PAT cross-project access=%d", name, status)
		}
		if status, _, _ := request(t, "GET",
			f.server.URL+"/api/v1/projects/"+f.project.String()+"/audit", token, "", "", nil); status != 403 {
			t.Fatalf("%s PAT gained forbidden audit permission=%d", name, status)
		}
	}

	revokeOutput, err := operator("revoke", "-credential", f.agentCredential.String())
	if err != nil {
		t.Fatalf("offline test-only revoke failed; output redacted: %v", err)
	}
	if strings.Contains(string(revokeOutput), oldToken) || strings.Contains(string(revokeOutput), newToken) {
		t.Fatal("offline revoke printed a PAT")
	}
	for _, target := range []string{f.server.URL + "/api/v1/projects", workURL, accessURL} {
		if status, _, _ := request(t, "GET", target, oldToken, "", "", nil); status != 401 {
			t.Fatalf("revoked PAT could still read %d", status)
		}
	}
	if status, _, _ := request(t, "POST", f.url(f.project), oldToken,
		"qual5-revoked-agent-write-denied", "", map[string]any{
			"kind": "task", "title": "must never exist",
		}); status != 401 {
		t.Fatalf("revoked PAT could still create WorkItem: %d", status)
	}
	if status, _, _ := request(t, "GET", workURL, newToken, "", "", nil); status != 200 {
		t.Fatalf("new PAT stopped reading after old revoked: %d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject), newToken, "", "", nil); status != 404 {
		t.Fatalf("new PAT could access foreign Project: %d", status)
	}
	after := create(t, f, newToken, "new credential still creates scoped work after old revoke")
	if after["created_by"] != agent.String() {
		t.Fatal("new credential changed the actual Agent actor")
	}
	var authenticatedCredential uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT credential_id FROM qual.audit_records
 WHERE entity_id=$1 AND operation='work_item.created' ORDER BY occurred_at DESC LIMIT 1`,
		after["id"]).Scan(&authenticatedCredential); err != nil {
		t.Fatal(err)
	}
	if authenticatedCredential != newCredential {
		t.Fatal("business audit did not capture the newly authenticated credential")
	}
	var oldRevoked, newActive bool
	if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM qual.credentials WHERE id=$1`,
		f.agentCredential).Scan(&oldRevoked); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NULL FROM qual.credentials WHERE id=$1`,
		newCredential).Scan(&newActive); err != nil {
		t.Fatal(err)
	}
	if !oldRevoked || !newActive {
		t.Fatal("credential revocation state lost during handoff")
	}
	// Idempotency is handled by the actual API; revoke is an offline one-shot
	// command and correctly rejects attempting the same revoke twice.
	if again, err := operator("revoke", "-credential", f.agentCredential.String()); err == nil ||
		strings.Contains(string(again), oldToken) || strings.Contains(string(again), newToken) {
		t.Fatal("repeated revoke succeeded or disclosed a token")
	}
}

func hasExactlyScopedPermissions(access map[string]any, expected []string) bool {
	raw, ok := access["effective_permissions"].([]any)
	if !ok || len(raw) != len(expected) {
		return false
	}
	found := make(map[string]bool, len(raw))
	for _, value := range raw {
		s, ok := value.(string)
		if !ok || found[s] {
			return false
		}
		found[s] = true
	}
	for _, scope := range expected {
		if !found[scope] {
			return false
		}
	}
	return true
}
