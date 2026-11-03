package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
)

// RedeemCode atomically consumes a local one-time Human login code.
// No token or code plaintext is persisted, and no Agent can redeem this route.
func (r Repository) RedeemCode(ctx context.Context, code string) (identity.BrowserSession, error) {
	hash, err := identity.HashSecret(code, identity.LoginCodePrefix)
	if err != nil {
		return identity.BrowserSession{}, identity.ErrUnauthenticated
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return identity.BrowserSession{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actor identity.Actor
	var kind string
	err = tx.QueryRow(ctx, `UPDATE qual.browser_login_codes lc SET used_at=now()
        FROM qual.principals p
        WHERE lc.code_hash=$1 AND lc.used_at IS NULL AND lc.expires_at>now()
          AND p.id=lc.principal_id AND p.disabled_at IS NULL AND p.kind='human'
        RETURNING p.id,p.workspace_id,p.kind`, hash[:]).
		Scan(&actor.PrincipalID, &actor.WorkspaceID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.BrowserSession{}, identity.ErrUnauthenticated
	}
	if err != nil {
		return identity.BrowserSession{}, err
	}
	actor.Type = "human"
	actor.CredentialID = uuid.New()
	actor.Scopes = []string{"work:read", "work:write", "work:claim", "work:review", "audit:read", "test:read", "test:write", "artifact:read", "artifact:write", "archive:export", "webhook:manage", "access:manage"}
	token, sessionHash, err := identity.GenerateSecret(identity.SessionPrefix)
	if err != nil {
		return identity.BrowserSession{}, err
	}
	expires := time.Now().Add(8 * time.Hour)
	_, err = tx.Exec(ctx, `INSERT INTO qual.credentials
        (id,workspace_id,principal_id,token_sha256,scopes,expires_at)
        VALUES ($1,$2,$3,$4,$5,$6)`, actor.CredentialID, actor.WorkspaceID, actor.PrincipalID,
		sessionHash[:], actor.Scopes, expires)
	if err != nil {
		return identity.BrowserSession{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.browser_sessions (credential_id,principal_id,expires_at)
        VALUES ($1,$2,$3)`, actor.CredentialID, actor.PrincipalID, expires)
	if err != nil {
		return identity.BrowserSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.BrowserSession{}, err
	}
	return identity.BrowserSession{Token: token, Actor: actor, ExpiresAt: expires}, nil
}

func (r Repository) AuthenticateSession(ctx context.Context, token string) (identity.Actor, error) {
	hash, err := identity.HashSecret(token, identity.SessionPrefix)
	if err != nil {
		return identity.Actor{}, identity.ErrUnauthenticated
	}
	var actor identity.Actor
	var kind string
	err = r.Pool.QueryRow(ctx, `SELECT p.id,c.id,p.workspace_id,p.kind,c.scopes
        FROM qual.credentials c
        JOIN qual.browser_sessions bs ON bs.credential_id=c.id
        JOIN qual.principals p ON p.id=bs.principal_id AND p.id=c.principal_id
        WHERE c.token_sha256=$1 AND c.revoked_at IS NULL AND c.expires_at>now()
          AND bs.revoked_at IS NULL AND bs.expires_at>now()
          AND p.disabled_at IS NULL AND p.kind='human'`, hash[:]).
		Scan(&actor.PrincipalID, &actor.CredentialID, &actor.WorkspaceID, &kind, &actor.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Actor{}, identity.ErrUnauthenticated
	}
	if err != nil {
		return identity.Actor{}, err
	}
	actor.Type = "human"
	return actor, nil
}

func (r Repository) LogoutSession(ctx context.Context, token string) error {
	hash, err := identity.HashSecret(token, identity.SessionPrefix)
	if err != nil {
		return identity.ErrUnauthenticated
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE qual.browser_sessions SET revoked_at=now()
        WHERE credential_id IN (SELECT id FROM qual.credentials WHERE token_sha256=$1)
          AND revoked_at IS NULL`, hash[:])
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return identity.ErrUnauthenticated
	}
	if _, err := tx.Exec(ctx, `UPDATE qual.credentials SET revoked_at=now()
        WHERE token_sha256=$1 AND revoked_at IS NULL`, hash[:]); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r Repository) Projects(ctx context.Context, actor identity.Actor) ([]identity.Project, error) {
	rows, err := r.Pool.Query(ctx, `SELECT DISTINCT p.id,p.workspace_id,p.key
        FROM qual.projects p
        JOIN qual.memberships m ON m.workspace_id=p.workspace_id AND m.principal_id=$1
          AND (m.project_id=p.id OR (m.project_id IS NULL AND m.role='workspace_admin'))
        WHERE p.workspace_id=$2 ORDER BY p.key`, actor.PrincipalID, actor.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]identity.Project, 0)
	for rows.Next() {
		var p identity.Project
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Key); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r Repository) Principal(ctx context.Context, actor identity.Actor) (identity.PrincipalInfo, error) {
	var p identity.PrincipalInfo
	err := r.Pool.QueryRow(ctx, `SELECT id,workspace_id,display_name,kind
       FROM qual.principals WHERE id=$1 AND workspace_id=$2 AND disabled_at IS NULL`,
		actor.PrincipalID, actor.WorkspaceID).Scan(&p.ID, &p.WorkspaceID, &p.DisplayName, &p.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, identity.ErrUnauthenticated
	}
	return p, err
}
