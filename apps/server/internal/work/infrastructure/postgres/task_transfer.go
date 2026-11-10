package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// authorizeExecutorChange allows a current assignee to release/transfer their
// task, or a separately authenticated project access manager to recover a
// blocked task. Manager authority is never derived from task ownership.
// Called under a project lock and a WorkItem FOR UPDATE lock.
func (r Repository) authorizeExecutorChange(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	project identity.Project, mode domain.TaskAuthorizationMode, item domain.WorkItem,
	action domain.TaskAction) error {
	if item.AssigneeID != nil && *item.AssigneeID == actor.PrincipalID {
		err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, item, action, idomain.WorkClaim, "", false)
		if err == nil {
			return nil
		}
		if !errors.Is(err, identity.ErrForbidden) {
			return err
		}
	}
	// Recheck the real current credential, membership, scope and project
	// explicit override in this transaction. No request boolean can grant it.
	s, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return err
	}
	effect, err := overrideEffect(ctx, tx, actor, project, idomain.AccessManage)
	if err != nil {
		return err
	}
	if !idomain.CanWithEffect(s.kind, s.role, s.scopes, idomain.AccessManage, effect) {
		return identity.ErrForbidden
	}
	return nil
}

// validateNewAssignee enforces the receiving principal's effective project
// participation ability. A known UUID is not enough: they must have active
// membership, an active credential with both base scopes, and no project deny.
// This also prevents a manager from appointing a non-member/integration bot.
func (r Repository) validateNewAssignee(ctx context.Context, tx pgx.Tx,
	project identity.Project, principalID uuid.UUID) error {
	if principalID == uuid.Nil {
		return domain.ErrValidation
	}
	var kind idomain.PrincipalType
	var role idomain.Role
	var scopes []string
	err := tx.QueryRow(ctx, `SELECT p.kind,m.role,c.scopes
 FROM qual.principals p
 JOIN qual.memberships m ON m.workspace_id=p.workspace_id AND m.principal_id=p.id
   AND (m.project_id=$2 OR (m.project_id IS NULL AND m.role='workspace_admin'))
 JOIN qual.credentials c ON c.workspace_id=p.workspace_id AND c.principal_id=p.id
 WHERE p.id=$1 AND p.workspace_id=$3 AND p.disabled_at IS NULL
   AND c.revoked_at IS NULL AND c.expires_at>now()
   AND c.scopes @> ARRAY['work:read','work:claim']::text[]
 ORDER BY (m.project_id IS NULL) ASC,c.expires_at DESC
 LIMIT 1 FOR SHARE OF p,m,c`,
		principalID, project.ID, project.WorkspaceID).Scan(&kind, &role, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != idomain.Agent && kind != idomain.Human {
		return identity.ErrNotFound
	}
	target := identity.Actor{PrincipalID: principalID, WorkspaceID: project.WorkspaceID}
	for _, permission := range []idomain.Permission{idomain.WorkRead, idomain.WorkClaim} {
		effect, err := overrideEffect(ctx, tx, target, project, permission)
		if err != nil {
			return err
		}
		if !idomain.CanWithEffect(kind, role, scopes, permission, effect) {
			return identity.ErrForbidden
		}
	}
	return nil
}
