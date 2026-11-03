// qual-admin is an offline provisioning tool for controlled installations.
// It is NOT the end-user/Agent CLI and never offers an unauthenticated HTTP route.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "qual/internal/identity/application"
	"qual/internal/identity/domain"
)

var slugFormat = regexp.MustCompile(`^[a-z][a-z0-9-]{1,47}$`)
var projectFormat = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,15}$`)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		// Errors must never contain the cleartext issued credential.
		fmt.Fprintln(os.Stderr, "qual-admin:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual-admin init|project|issue|issue-existing|retire-superseded|grant|revoke|disable|browser-code [flags]")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required (no default admin database)")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "workspace slug")
		project := fs.String("project", "", "project key")
		admin := fs.String("admin", "", "human administrator display name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if !slugFormat.MatchString(*workspace) || !projectFormat.MatchString(*project) || strings.TrimSpace(*admin) == "" {
			return errors.New("required: -workspace lowercase-slug -project UPPERKEY -admin NAME")
		}
		return initialize(ctx, pool, *workspace, *project, *admin)
	case "issue":
		fs := flag.NewFlagSet("issue", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "workspace slug")
		project := fs.String("project", "", "project key")
		name := fs.String("name", "", "principal display name")
		kind := fs.String("kind", "agent", "human|agent|integration")
		role := fs.String("role", "project_member", "project role")
		scopes := fs.String("scopes", "work:read,work:write,work:claim", "comma-separated credential scopes")
		days := fs.Int("days", 7, "credential lifetime in days")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *days < 1 || *days > 90 || strings.TrimSpace(*name) == "" ||
			!domain.ValidKind(domain.PrincipalType(*kind)) ||
			!domain.ValidRole(domain.Role(*role)) ||
			*role == string(domain.WorkspaceAdmin) {
			return errors.New("invalid name, kind, project role or token lifetime")
		}
		allowed := map[string]bool{
			"work:read": true, "work:write": true, "work:claim": true,
			"work:review": true, "audit:read": true,
			"test:read": true, "test:write": true,
			"artifact:read": true, "artifact:write": true,
			"archive:export": true, "webhook:manage": true, "access:manage": true,
		}
		scopeList := strings.Split(*scopes, ",")
		if len(scopeList) < 1 || len(scopeList) > 16 {
			return errors.New("invalid scopes")
		}
		for _, scope := range scopeList {
			if !allowed[scope] || !domain.Grantable(domain.PrincipalType(*kind), domain.Permission(scope)) {
				return fmt.Errorf("scope %q is unknown or restricted for principal type", scope)
			}
		}
		return issue(ctx, pool, *workspace, *project, *name, domain.PrincipalType(*kind), domain.Role(*role), scopeList, *days)
	case "issue-existing":
		fs := flag.NewFlagSet("issue-existing", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "existing Workspace slug")
		project := fs.String("project", "", "existing Project key")
		principal := fs.String("principal", "", "existing Principal UUID (must already belong to Project)")
		scopes := fs.String("scopes", "work:read", "comma-separated NEW credential scopes; no Project rights are granted")
		days := fs.Int("days", 7, "credential lifetime days (1..90)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		principalID, err := uuid.Parse(*principal)
		if err != nil || principalID == uuid.Nil {
			return errors.New("valid existing Principal UUID required")
		}
		return issueExistingCredential(ctx, pool, *workspace, *project, principalID, strings.Split(*scopes, ","), *days)
	case "retire-superseded":
		// Trusted Human Operator only. Never add this to the Agent REST/CLI/MCP.
		home, err := os.UserHomeDir()
		if err != nil {
			return errors.New("cannot resolve local private credential directory")
		}
		fs := flag.NewFlagSet("retire-superseded", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "explicit existing Workspace slug")
		project := fs.String("project", "", "existing Project key")
		file := fs.String("token-file", filepath.Join(home, ".config", "qual", "qual-pi-agent.token"), "private active Agent PAT file (0600 inside owner-only 0700 directory)")
		apply := fs.Bool("apply", false, "explicitly retire all safe superseded credentials after preview")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return retireSuperseded(ctx, pool, *workspace, *project, *file, *apply)
	case "project":
		fs := flag.NewFlagSet("project", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "existing workspace slug")
		key := fs.String("project", "", "new project key")
		name := fs.String("name", "", "project name (defaults to key)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if !slugFormat.MatchString(*workspace) || !projectFormat.MatchString(*key) {
			return errors.New("valid -workspace and -project required")
		}
		return createProject(ctx, pool, *workspace, *key, *name)
	case "grant":
		fs := flag.NewFlagSet("grant", flag.ContinueOnError)
		workspace := fs.String("workspace", "", "workspace slug")
		project := fs.String("project", "", "project key")
		principal := fs.String("principal", "", "existing Principal UUID")
		role := fs.String("role", "project_member", "project role")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		id, err := uuid.Parse(*principal)
		if err != nil || id == uuid.Nil || !domain.ValidRole(domain.Role(*role)) || *role == string(domain.WorkspaceAdmin) {
			return errors.New("valid -principal and non-admin -role required")
		}
		return grantMembership(ctx, pool, *workspace, *project, id, domain.Role(*role))
	case "disable":
		fs := flag.NewFlagSet("disable", flag.ContinueOnError)
		principal := fs.String("principal", "", "Principal UUID to disable and revoke")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		id, err := uuid.Parse(*principal)
		if err != nil || id == uuid.Nil {
			return errors.New("valid -principal UUID required")
		}
		return disablePrincipal(ctx, pool, id)
	case "browser-code":
		fs := flag.NewFlagSet("browser-code", flag.ContinueOnError)
		principal := fs.String("principal", "", "Human Principal UUID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		id, err := uuid.Parse(*principal)
		if err != nil || id == uuid.Nil {
			return errors.New("valid Human -principal UUID required")
		}
		return issueBrowserCode(ctx, pool, id)
	case "revoke":
		fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
		id := fs.String("credential", "", "credential UUID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		parsed, err := uuid.Parse(*id)
		if err != nil {
			return errors.New("valid -credential UUID required")
		}
		tag, err := pool.Exec(ctx, `UPDATE qual.credentials SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, parsed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("credential not found or already revoked")
		}
		fmt.Println("Credential revoked:", parsed)
		return nil
	default:
		return errors.New("unknown command; use init|project|issue|issue-existing|retire-superseded|grant|revoke|disable|browser-code")
	}
}

func initialize(ctx context.Context, pool *pgxpool.Pool, slug, key, admin string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	wsID, projID, adminID, memberID, credentialID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	token, hash, err := identity.NewToken()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.workspaces(id,slug,name) VALUES($1,$2,$3)`, wsID, slug, slug); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.projects(id,workspace_id,key,name) VALUES($1,$2,$3,$4)`, projID, wsID, key, key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.principals(id,workspace_id,kind,display_name) VALUES($1,$2,'human',$3)`, adminID, wsID, admin); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.memberships(id,workspace_id,principal_id,role) VALUES($1,$2,$3,'workspace_admin')`, memberID, wsID, adminID); err != nil {
		return err
	}
	// A short-lived bootstrap human token; web session and OIDC are not implemented.
	if _, err := tx.Exec(ctx, `INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, credentialID, wsID, adminID, hash[:],
		[]string{"work:read", "work:write", "work:claim", "work:review", "audit:read", "test:read", "test:write", "artifact:read", "artifact:write", "archive:export", "webhook:manage", "access:manage"}, time.Now().Add(30*24*time.Hour)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("Workspace ID:", wsID)
	fmt.Println("Project ID:", projID)
	fmt.Println("Admin Principal ID:", adminID)
	fmt.Println("Credential ID:", credentialID)
	fmt.Println("Token (displayed once; store securely):", token)
	return nil
}

func issue(ctx context.Context, pool *pgxpool.Pool, workspace, project, name string, kind domain.PrincipalType, role domain.Role, scopes []string, days int) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var wsID, projID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT w.id,p.id FROM qual.workspaces w
		JOIN qual.projects p ON p.workspace_id=w.id WHERE w.slug=$1 AND p.key=$2`, workspace, project).Scan(&wsID, &projID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("workspace/project not found")
		}
		return err
	}
	principalID, membershipID, credentialID := uuid.New(), uuid.New(), uuid.New()
	token, hash, err := identity.NewToken()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.principals(id,workspace_id,kind,display_name)
		VALUES($1,$2,$3,$4)`, principalID, wsID, kind, name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.memberships(id,workspace_id,project_id,principal_id,role)
		VALUES($1,$2,$3,$4,$5)`, membershipID, wsID, projID, principalID, role); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
		VALUES($1,$2,$3,$4,$5,$6)`, credentialID, wsID, principalID, hash[:], scopes, time.Now().Add(time.Duration(days)*24*time.Hour)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("Project ID:", projID)
	fmt.Println("Principal ID:", principalID)
	fmt.Println("Credential ID:", credentialID)
	fmt.Println("Token (displayed once; store securely):", token)
	return nil
}
