package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "qual/internal/identity/application"
	"qual/internal/identity/domain"
)

// issueExistingCredential is an OFFLINE administrator-only operation. It
// creates a new credential for one existing real Principal; it never creates
// an alternate Agent identity or grants Project Permission Overrides.
//
// Administrators must run it outside PI and retain the new one-time token in
// their secret manager, then explicitly rotate/revoke the old credential.
func issueExistingCredential(ctx context.Context, pool *pgxpool.Pool, workspace, project string,
	principalID uuid.UUID, scopes []string, days int) error {
	if !slugFormat.MatchString(workspace) || !projectFormat.MatchString(project) || principalID == uuid.Nil ||
		days < 1 || days > 90 {
		return errors.New("invalid existing-credential target or lifetime")
	}
	if len(scopes) < 1 || len(scopes) > 12 {
		return errors.New("at least one and at most 12 scopes required")
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if scope != strings.TrimSpace(scope) || seen[scope] || !domain.ValidPermission(domain.Permission(scope)) {
			return errors.New("invalid, duplicate or unknown credential scopes")
		}
		seen[scope] = true
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var workspaceID, projectID uuid.UUID
	var kind domain.PrincipalType
	var role domain.Role
	err = tx.QueryRow(ctx, `
 SELECT p.workspace_id,pr.id,p.kind,m.role
 FROM qual.principals p
 JOIN qual.workspaces w ON w.id=p.workspace_id
 JOIN qual.projects pr ON pr.workspace_id=p.workspace_id
 JOIN qual.memberships m ON m.principal_id=p.id AND m.workspace_id=p.workspace_id
  AND (m.project_id=pr.id OR (m.project_id IS NULL AND m.role='workspace_admin'))
 WHERE w.slug=$1 AND pr.key=$2 AND p.id=$3 AND p.disabled_at IS NULL
 ORDER BY (m.project_id IS NULL)
 LIMIT 1 FOR SHARE OF p`, workspace, project, principalID).
		Scan(&workspaceID, &projectID, &kind, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("active Principal is not a member of this project")
	}
	if err != nil {
		return err
	}
	if !domain.ValidKind(kind) || !domain.ValidRole(role) {
		return errors.New("invalid Principal kind or Project role")
	}
	for _, scope := range scopes {
		if !domain.Grantable(kind, domain.Permission(scope)) {
			return fmt.Errorf("scope %q prohibited for this Principal type", scope)
		}
	}

	token, hash, err := identity.NewToken()
	if err != nil {
		return err
	}
	credentialID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
 VALUES($1,$2,$3,$4,$5,$6)`, credentialID, workspaceID, principalID, hash[:], scopes, time.Now().Add(time.Duration(days)*24*time.Hour))
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("Project ID:", projectID)
	fmt.Println("Existing Principal ID:", principalID)
	fmt.Println("Credential ID:", credentialID)
	fmt.Println("Token (displayed once; store securely):", token)
	return nil
}
