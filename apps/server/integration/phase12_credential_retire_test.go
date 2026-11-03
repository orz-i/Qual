//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This test MUST run only in a migrated disposable qual5_rotate_* database.
// Neither the default QUAL database nor a real PI/Owner credential is touched.
// The tested admin binary is spawned exclusively with a synthetic DB URL.
func TestOperatorRetireSupersededRequiresProtectedCurrentTokenAndExplicitApply(t *testing.T) {
	databaseURL := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(databaseURL, "/qual5_rotate_") {
		t.Skip("requires disposable qual5_rotate_* DB, never real QUAL")
	}
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	agent := principalFor(t, f, f.agent)
	bin := filepath.Join(t.TempDir(), "qual-admin")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual-admin")
	if _, err := build.CombinedOutput(); err != nil {
		t.Fatalf("test-only admin build failed (output redacted): %v", err)
	}
	var workspace string
	if err := f.pool.QueryRow(ctx, `
SELECT w.slug FROM qual.workspaces w JOIN qual.projects p
ON w.id=p.workspace_id WHERE p.id=$1`, f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+databaseURL)
		// CombinedOutput may contain a new one-time PAT: never log it.
		return cmd.CombinedOutput()
	}
	issue := func(scopes string) (string, uuid.UUID) {
		t.Helper()
		out, err := run("issue-existing", "-workspace", workspace, "-project", "ENG",
			"-principal", agent.String(), "-scopes", scopes, "-days", "7")
		if err != nil {
			t.Fatalf("test-only issue-existing failed (output redacted): %v", err)
		}
		pat := regexp.MustCompile(`qual_pat_[a-f0-9]{64}`).FindString(string(out))
		idMatch := regexp.MustCompile(`Credential ID: ([0-9a-f-]{36})`).FindStringSubmatch(string(out))
		if pat == "" || len(idMatch) != 2 {
			t.Fatal("synthetic credentials were not generated")
		}
		id, err := uuid.Parse(idMatch[1])
		if err != nil {
			t.Fatal(err)
		}
		return pat, id
	}

	// Human-operated rotation already installed this replacement; the new
	// command only needs its protected file, not the original old Token.
	newToken, newID := issue("work:read,work:write,work:claim,test:read,test:write,artifact:read,artifact:write,work:review")
	directory := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "active-agent.token")
	if err := os.WriteFile(path, []byte(newToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"retire-superseded", "-workspace", workspace, "-project", "ENG", "-token-file", path}
	preview, err := run(base...)
	if err != nil || !strings.Contains(string(preview), "PREVIEW ONLY") ||
		!strings.Contains(string(preview), f.agentCredential.String()) ||
		!strings.Contains(string(preview), newID.String()) ||
		strings.Contains(string(preview), newToken) || strings.Contains(string(preview), f.agent) {
		t.Fatal("safe preview failed, changed plan, or printed a cleartext PAT")
	}
	var oldRevoked bool
	if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM qual.credentials WHERE id=$1`,
		f.agentCredential).Scan(&oldRevoked); err != nil || oldRevoked {
		t.Fatal("preview unexpectedly revoked the original credential")
	}
	// Unknown project and unsafe files must fail before revocation.
	wrongProject := append([]string{}, base...)
	wrongProject[4] = "OTHER"
	if out, err := run(wrongProject...); err == nil || strings.Contains(string(out), newToken) {
		t.Fatal("foreign project unexpectedly accepted or leaked token")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(append(append([]string{}, base...), "-apply")...); err == nil ||
		strings.Contains(string(out), newToken) {
		t.Fatal("unsafe private credential accepted or disclosed a PAT")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Explicit apply retires the old token without ever needing its value.
	applied, err := run(append(append([]string{}, base...), "-apply")...)
	if err != nil || !strings.Contains(string(applied), "APPLIED: retired 1") ||
		strings.Contains(string(applied), newToken) || strings.Contains(string(applied), f.agent) {
		t.Fatalf("explicit retirement failed (output redacted): %v", err)
	}
	if status, _, _ := request(t, "GET", f.url(f.project), f.agent, "", "", nil); status != 401 {
		t.Fatalf("revoked synthetic old PAT still works: %d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.project), newToken, "", "", nil); status != 200 {
		t.Fatalf("protected new PAT lost access: %d", status)
	}
	var currentRevoked bool
	if err := f.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM qual.credentials WHERE id=$1`,
		newID).Scan(&currentRevoked); err != nil || currentRevoked {
		t.Fatal("current new credential was accidentally revoked")
	}
	again, err := run(append(append([]string{}, base...), "-apply")...)
	if err != nil || !strings.Contains(string(again), "retired 0") {
		t.Fatal("repeated retirement did not remain safely idempotent")
	}
	// A later narrower credential must NOT automatically retire a previous
	// broader one or break other still-intended clients.
	narrow, _ := issue("work:read")
	if err := os.WriteFile(path, []byte(narrow+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(append(append([]string{}, base...), "-apply")...); err == nil ||
		strings.Contains(string(out), narrow) {
		t.Fatal("broader older scope was retired automatically or token leaked")
	}
	if status, _, _ := request(t, "GET", f.url(f.project), newToken, "", "", nil); status != 200 {
		t.Fatalf("scope-mismatch guard revoked retained credential: %d", status)
	}
}
