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
	identitydomain "qual/internal/identity/domain"
)

// Offline Human Operator command. Never exposed by REST/MCP or the scoped
// Agent CLI. The current protected PAT identifies the credential to KEEP;
// no old token value or manually discovered old Credential ID is required.
func retireSuperseded(ctx context.Context, pool *pgxpool.Pool, workspace, project, tokenFile string, apply bool) error {
	if !slugFormat.MatchString(workspace) || !projectFormat.MatchString(project) {
		return errors.New("valid explicit -workspace and -project required")
	}
	digest, err := privateAgentTokenDigest(tokenFile)
	if err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var current identitydomain.CredentialMeta
	var workspaceID, principalID uuid.UUID
	err = tx.QueryRow(ctx, `
SELECT c.id,c.workspace_id,c.principal_id,c.scopes,c.created_at,c.expires_at
FROM qual.credentials c
JOIN qual.workspaces w ON w.id=c.workspace_id
JOIN qual.principals p ON p.id=c.principal_id AND p.workspace_id=c.workspace_id
JOIN qual.projects pr ON pr.workspace_id=c.workspace_id
WHERE c.token_sha256=$1 AND w.slug=$2 AND pr.key=$3
  AND p.kind='agent' AND p.disabled_at IS NULL
  AND c.revoked_at IS NULL AND c.expires_at>now()
  AND EXISTS (SELECT 1 FROM qual.memberships m
              WHERE m.workspace_id=c.workspace_id AND m.principal_id=c.principal_id
                AND m.project_id=pr.id AND m.role<>'workspace_admin')
FOR UPDATE OF c`, digest[:], workspace, project).
		Scan(&current.ID, &workspaceID, &principalID, &current.Scopes, &current.CreatedAt, &current.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("current PAT is not an active Agent credential in the selected Project")
	}
	if err != nil {
		return err
	}
	// Credential grants apply at Workspace Principal level, not at a single
	// Project. For multi-project Agents, refuse to guess which clients own
	// other active credentials: keep manual multi-project Operator handling.
	var memberships int
	if err := tx.QueryRow(ctx, `
SELECT count(*) FROM qual.memberships WHERE workspace_id=$1 AND principal_id=$2`,
		workspaceID, principalID).Scan(&memberships); err != nil {
		return err
	}
	if memberships != 1 {
		return errors.New("Agent has multiple Project/Workspace memberships; manual credential review required")
	}

	rows, err := tx.Query(ctx, `
SELECT id,scopes,created_at,expires_at
FROM qual.credentials
WHERE workspace_id=$1 AND principal_id=$2
  AND revoked_at IS NULL AND expires_at>now()
ORDER BY created_at,id FOR UPDATE`, workspaceID, principalID)
	if err != nil {
		return err
	}
	var active []identitydomain.CredentialMeta
	for rows.Next() {
		var item identitydomain.CredentialMeta
		if err := rows.Scan(&item.ID, &item.Scopes, &item.CreatedAt, &item.ExpiresAt); err != nil {
			rows.Close()
			return err
		}
		active = append(active, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	plan, err := identitydomain.PlanRetirement(current, active)
	if err != nil {
		return err
	}
	fmt.Printf("Current Agent Credential (will retain): %s\n", plan.Current.ID)
	fmt.Printf("Project: %s / %s; superseded active credentials: %d\n", workspace, project, len(plan.Old))
	for _, item := range plan.Old {
		fmt.Printf("  retire %s (issued %s; scopes %s)\n",
			item.ID, item.CreatedAt.UTC().Format(time.RFC3339), strings.Join(item.Scopes, ","))
	}
	if !apply {
		fmt.Println("PREVIEW ONLY: nothing changed. Re-run with -apply in a trusted Human Operator terminal.")
		return nil
	}
	for _, item := range plan.Old {
		tag, err := tx.Exec(ctx, `
UPDATE qual.credentials SET revoked_at=now()
WHERE id=$1 AND workspace_id=$2 AND principal_id=$3
  AND revoked_at IS NULL AND expires_at>now()`, item.ID, workspaceID, principalID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("credential changed during retirement; no partial changes committed")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("APPLIED: retired %d superseded credentials; current credential retained.\n", len(plan.Old))
	fmt.Println("Offline database maintenance has no authenticated Human actor; record Operator handoff separately.")
	return nil
}
