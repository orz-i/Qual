//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPhase9OfflineIssueAdditionalCredentialRetainsSamePrincipal(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	principalID := principalFor(t, f, f.agent)
	var workspace string
	if err := f.pool.QueryRow(ctx, `SELECT w.slug FROM qual.workspaces w
 JOIN qual.projects p ON p.workspace_id=w.id WHERE p.id=$1`, f.project).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.principals`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "qual-admin")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual-admin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("admin build failed: %v %s", err, output)
	}
	newTokenCmd := func(project string, principal uuid.UUID, scopes string) (string, error) {
		command := exec.CommandContext(ctx, bin,
			"issue-existing", "-workspace", workspace, "-project", project,
			"-principal", principal.String(), "-scopes", scopes, "-days", "7")
		command.Env = append(os.Environ(), "DATABASE_URL="+os.Getenv("QUAL_TEST_DATABASE_URL"))
		raw, err := command.CombinedOutput()
		if err != nil {
			return "", err
		}
		match := regexp.MustCompile(`qual_pat_[a-f0-9]{64}`).FindString(string(raw))
		if match == "" {
			t.Fatal("admin token output missing; contents intentionally not logged")
		}
		return match, nil
	}
	if _, err := newTokenCmd("OTHER", principalID, "work:read"); err == nil {
		t.Fatal("offline issuer created a credential for Principal in foreign Project")
	}
	if _, err := newTokenCmd("ENG", uuid.New(), "work:read"); err == nil {
		t.Fatal("offline issuer created token for nonmember Principal")
	}
	if _, err := newTokenCmd("ENG", principalID, "work:read,work:read"); err == nil {
		t.Fatal("duplicate Scope accepted")
	}
	if _, err := newTokenCmd("ENG", principalID, "unrecognized:scope"); err == nil {
		t.Fatal("unknown Scope accepted")
	}
	token, err := newTokenCmd("ENG", principalID, "work:read,work:write,work:review,test:write,audit:read")
	if err != nil {
		t.Fatalf("issue existing Principal failed (token redacted): %v", err)
	}
	if principalFor(t, f, token) != principalID {
		t.Fatal("new credential changed real Principal")
	}
	var after int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.principals`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("credential issuance created fake additional Agent identity")
	}
	accessURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/access/me"
	status, scopes, _ := request(t, "GET", accessURL, token, "", "", nil)
	if status != 200 {
		t.Fatalf("issued credential self access %d", status)
	}
	effective, err := json.Marshal(scopes["effective_permissions"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(effective), "work:review") {
		t.Fatal("project grant missing; role Scope auto-escalated")
	}
	if status, _ := setPermission(t, f, f.admin, principalID, "work:review", "allow", 0, "phase9-real-principal-review-grant"); status != 200 {
		t.Fatalf("review allow %d", status)
	}
	if status, _ := setPermission(t, f, f.admin, principalID, "audit:read", "allow", 0, "phase9-real-principal-audit-grant"); status != 200 {
		t.Fatalf("audit allow %d", status)
	}
	status, scopes, _ = request(t, "GET", accessURL, token, "", "", nil)
	if status != 200 {
		t.Fatal("self access after explicit grant failed")
	}
	effective, _ = json.Marshal(scopes["effective_permissions"])
	if !strings.Contains(string(effective), "work:review") || !strings.Contains(string(effective), "audit:read") {
		t.Fatalf("explicit allowed scopes absent: %s", effective)
	}
	work := create(t, f, f.agent, "same Agent additional scoped credential")
	path := roleEndpoint(f, work["id"].(string))
	if status, _, _ := request(t, "POST", path, f.agent, "old-token-cannot-review", "",
		roleInput("reviewer", "passed", "git_commit", "fbd95a6", "should be rejected before new token", 1)); status != 403 {
		t.Fatalf("old Agent credential automatically promoted %d", status)
	}
	status, executor, _ := request(t, "POST", path, f.agent, "existing-agent-executor", "",
		roleInput("executor", "observed", "git_commit", "fbd95a6", "same Principal first executor", 1))
	if status != 201 {
		t.Fatalf("executor role failed %d", status)
	}
	status, tester, _ := request(t, "POST", path, token, "same-agent-tester", "",
		roleInput("tester", "passed", "git_commit", "fbd95a6", "same Principal test observation", 2))
	if status != 201 {
		t.Fatalf("tester role failed %d", status)
	}
	status, reviewer, _ := request(t, "POST", path, token, "same-agent-authorized-reviewer", "",
		roleInput("reviewer", "passed", "git_commit", "fbd95a6", "explicitly scoped same Principal self review", 3))
	if status != 201 || reviewer["self_review"] != true {
		t.Fatalf("self review not marked %d %v", status, reviewer)
	}
	status, auditor, _ := request(t, "POST", path, token, "same-agent-authorized-auditor", "",
		roleInput("auditor", "observed", "none", "", "same Principal audit role permitted", 4))
	if status != 201 {
		t.Fatalf("auditor role failed %d", status)
	}
	if executor["actor_id"] != principalID.String() || tester["actor_id"] != principalID.String() ||
		reviewer["actor_id"] != principalID.String() || auditor["actor_id"] != principalID.String() {
		t.Fatal("capacity switches changed actual Principal")
	}
	// Credential IDs differ while the actual Principal remains identical, so
	// each event also preserves the authentication credential provenance.
	var uniquePrincipal, uniqueCredentials int
	if err := f.pool.QueryRow(ctx, `SELECT count(DISTINCT actor_id),count(DISTINCT credential_id)
  FROM qual.work_role_entries WHERE work_item_id=$1`, work["id"]).
		Scan(&uniquePrincipal, &uniqueCredentials); err != nil {
		t.Fatal(err)
	}
	if uniquePrincipal != 1 || uniqueCredentials != 2 {
		t.Fatalf("traceability principal=%d credential=%d", uniquePrincipal, uniqueCredentials)
	}
	// Revocation takes effect immediately without rewriting historical events.
	if status, _ := setPermission(t, f, f.admin, principalID, "work:review", "deny", 1, "phase9-revoke-scoped-review"); status != 200 {
		t.Fatal("revoke review failed")
	}
	if status, _, _ := request(t, "POST", path, token, "same-agent-review-after-deny", "",
		roleInput("reviewer", "passed", "git_commit", "fbd95a6", "should be rejected", 5)); status != 403 {
		t.Fatalf("scope not revoked immediately %d", status)
	}
}
