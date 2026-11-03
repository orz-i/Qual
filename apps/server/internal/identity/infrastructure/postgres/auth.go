package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "qual/internal/identity/application"
	"qual/internal/identity/domain"
)

// Repository handles credential lookup and server-side project authorization.
type Repository struct {
	Pool *pgxpool.Pool
}

func (r Repository) Authenticate(ctx context.Context, bearer string) (identity.Actor, error) {
	digest, err := identity.HashToken(bearer)
	if err != nil {
		return identity.Actor{}, identity.ErrUnauthenticated
	}
	var actor identity.Actor
	var kind string
	err = r.Pool.QueryRow(ctx, `
		SELECT p.id, c.id, p.workspace_id, p.kind, c.scopes
		  FROM qual.credentials c
		  JOIN qual.principals p ON (c.principal_id = p.id AND c.workspace_id = p.workspace_id)
		 WHERE c.token_sha256 = $1
		   AND c.revoked_at IS NULL AND c.expires_at > now()
		   AND p.disabled_at IS NULL
	`, digest[:]).Scan(&actor.PrincipalID, &actor.CredentialID, &actor.WorkspaceID, &kind, &actor.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Actor{}, identity.ErrUnauthenticated
	}
	if err != nil {
		return identity.Actor{}, err
	}
	actor.Type = domain.PrincipalType(kind)
	return actor, nil
}

func (r Repository) AuthorizeProject(ctx context.Context, actor identity.Actor, projectID uuid.UUID, permission domain.Permission) (identity.Project, error) {
	if actor.PrincipalID == uuid.Nil || actor.WorkspaceID == uuid.Nil || projectID == uuid.Nil {
		return identity.Project{}, identity.ErrNotFound
	}
	var project identity.Project
	var role, effect string
	err := r.Pool.QueryRow(ctx, `
		SELECT p.id, p.workspace_id, p.key, m.role, COALESCE(o.effect,'inherit')
		  FROM qual.projects p
		  JOIN qual.memberships m ON (
		       m.workspace_id = p.workspace_id
		   AND m.principal_id = $1
		   AND (m.project_id = p.id OR (m.project_id IS NULL AND m.role='workspace_admin')))
		 LEFT JOIN qual.project_permission_overrides o ON o.workspace_id=p.workspace_id
              AND o.project_id=p.id AND o.principal_id=$1 AND o.permission=$4
         WHERE p.id = $2 AND p.workspace_id = $3
		 ORDER BY (m.project_id IS NULL) ASC
		 LIMIT 1
	`, actor.PrincipalID, projectID, actor.WorkspaceID, string(permission)).Scan(&project.ID, &project.WorkspaceID, &project.Key, &role, &effect)
	if errors.Is(err, pgx.ErrNoRows) {
		// Do not reveal whether a project belonging to another tenant exists.
		return identity.Project{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.Project{}, err
	}
	if !domain.CanWithEffect(actor.Type, domain.Role(role), actor.Scopes, permission, domain.Effect(effect)) {
		return identity.Project{}, identity.ErrForbidden
	}
	return project, nil
}
