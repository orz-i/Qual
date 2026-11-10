package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// GetTaskMode returns the effective project mode. Goose17 and projects with
// no explicit policy remain legacy. A query failure is never treated as legacy.
func (r Repository) GetTaskMode(ctx context.Context, projectID uuid.UUID) (domain.TaskAuthorizationMode, error) {
	var installed bool
	if err := r.Pool.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		return "", err
	}
	if !installed {
		return domain.LegacyAuthorization, nil
	}
	var mode domain.TaskAuthorizationMode
	err := r.Pool.QueryRow(ctx, `SELECT mode FROM qual.project_task_authorization_policies WHERE project_id=$1`, projectID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LegacyAuthorization, nil
	}
	if err != nil {
		return "", err
	}
	if mode != domain.LegacyAuthorization && mode != domain.TaskScopedAuthorization {
		return "", domain.ErrValidation
	}
	return mode, nil
}

// lockTaskMode is called BEFORE locking WorkItem, synchronizing task decisions
// with project policy updates. Never turn an unavailable policy into legacy.
func (r Repository) lockTaskMode(ctx context.Context, tx pgx.Tx, project identity.Project) (domain.TaskAuthorizationMode, error) {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM qual.projects WHERE id=$1 AND workspace_id=$2 FOR SHARE`, project.ID, project.WorkspaceID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", identity.ErrNotFound
		}
		return "", err
	}
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('qual.project_task_authorization_policies') IS NOT NULL").Scan(&installed); err != nil {
		return "", err
	}
	if !installed {
		return domain.LegacyAuthorization, nil
	}
	var mode domain.TaskAuthorizationMode
	err := tx.QueryRow(ctx, `SELECT mode FROM qual.project_task_authorization_policies WHERE project_id=$1`, project.ID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LegacyAuthorization, nil
	}
	if err != nil {
		return "", err
	}
	if mode != domain.LegacyAuthorization && mode != domain.TaskScopedAuthorization {
		return "", domain.ErrValidation
	}
	return mode, nil
}

type taskActorSnapshot struct {
	kind   idomain.PrincipalType
	role   idomain.Role
	scopes []string
}

// credentialAndRole verifies the current principal/credential/membership in
// the same transaction as the WorkItem write, never trusting stale actor
// scopes or a role supplied by the client.
func (r Repository) credentialAndRole(ctx context.Context, tx pgx.Tx, actor identity.Actor, project identity.Project) (taskActorSnapshot, error) {
	var s taskActorSnapshot
	err := tx.QueryRow(ctx, `
 SELECT p.kind,m.role,c.scopes
 FROM qual.credentials c
 JOIN qual.principals p ON p.id=c.principal_id AND p.workspace_id=c.workspace_id
 JOIN qual.memberships m ON m.principal_id=p.id AND m.workspace_id=p.workspace_id
   AND (m.project_id=$3 OR (m.project_id IS NULL AND m.role='workspace_admin'))
 WHERE c.id=$1 AND p.id=$2 AND p.workspace_id=$4
   AND c.revoked_at IS NULL AND c.expires_at>now()
   AND p.disabled_at IS NULL
 ORDER BY (m.project_id IS NULL) ASC LIMIT 1
 FOR SHARE OF c,p,m`, actor.CredentialID, actor.PrincipalID, project.ID, project.WorkspaceID).Scan(&s.kind, &s.role, &s.scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, identity.ErrUnauthenticated
	}
	if err != nil {
		return s, err
	}
	if actor.Type != s.kind || actor.WorkspaceID != project.WorkspaceID {
		return s, identity.ErrForbidden
	}
	return s, nil
}

func overrideEffect(ctx context.Context, tx pgx.Tx, actor identity.Actor, project identity.Project, permission idomain.Permission) (idomain.Effect, error) {
	var effect idomain.Effect
	err := tx.QueryRow(ctx, `SELECT effect FROM qual.project_permission_overrides
 WHERE workspace_id=$1 AND project_id=$2 AND principal_id=$3 AND permission=$4
 FOR SHARE`, project.WorkspaceID, project.ID, actor.PrincipalID, permission).Scan(&effect)
	if errors.Is(err, pgx.ErrNoRows) {
		return idomain.Inherit, nil
	}
	return effect, err
}

// authorizeTaskWrite runs inside the SAME transaction as the affected locked
// WorkItem. Its project lock must already have been obtained by lockTaskMode.
// Legacy continues to require the canonical Scope + project policy; scoped
// mode derives only the named WorkItem action from persisted facts.
func (r Repository) authorizeTaskWrite(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	project identity.Project, mode domain.TaskAuthorizationMode, item domain.WorkItem,
	action domain.TaskAction, legacyPermission idomain.Permission, reviewMode domain.ReviewPolicyMode,
	forcedIndependent bool) error {
	s, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return err
	}
	if mode == domain.LegacyAuthorization {
		effect, err := overrideEffect(ctx, tx, actor, project, legacyPermission)
		if err != nil {
			return err
		}
		if !idomain.CanWithEffect(s.kind, s.role, s.scopes, legacyPermission, effect) {
			return identity.ErrForbidden
		}
		return nil
	}
	if mode != domain.TaskScopedAuthorization || (s.kind != idomain.Human && s.kind != idomain.Agent) {
		return identity.ErrForbidden
	}
	for _, p := range []idomain.Permission{idomain.WorkRead, idomain.WorkClaim} {
		effect, err := overrideEffect(ctx, tx, actor, project, p)
		if err != nil {
			return err
		}
		if !idomain.CanWithEffect(s.kind, s.role, s.scopes, p, effect) {
			return identity.ErrForbidden
		}
	}
	canonical, ok := application.TaskActionPermission(action)
	if !ok {
		return identity.ErrForbidden
	}
	effect, err := overrideEffect(ctx, tx, actor, project, canonical)
	if err != nil {
		return err
	}
	facts := domain.TaskAccessFacts{
		ActorID: actor.PrincipalID, WorkItem: item, Action: action,
		ProjectMode: mode, Participant: true, ExplicitDeny: effect == idomain.Deny,
		ReviewMode: reviewMode, ForcedIndependent: forcedIndependent,
	}
	// Current reviewer identity is a persisted relationship, never a RoleEntry
	// or an API-supplied reviewer label. Owner completion also needs the real
	// policy; no reviewer is required for valid owner_acceptance.
	switch action {
	case domain.TaskComment, domain.TaskClaimReview, domain.TaskRecordReview,
		domain.TaskReturnForWork, domain.TaskReleaseReview,
		domain.TaskCompleteIndependent, domain.TaskCompleteOwner,
		domain.TaskReportTest, domain.TaskAttachEvidence,
		domain.TaskReadTest, domain.TaskReadEvidence:
		reviewer, _, err := activeReviewer(ctx, tx, item)
		if err != nil {
			return err
		}
		facts.ReviewerID = reviewer
	}
	switch action {
	case domain.TaskClaimReview, domain.TaskRecordReview, domain.TaskReturnForWork,
		domain.TaskCompleteIndependent, domain.TaskCompleteOwner:
		policy, _, err := resolveWorkReviewPolicy(ctx, tx, item)
		if err != nil {
			return err
		}
		facts.ReviewMode = policy.Mode
		facts.ForcedIndependent = policy.ForceIndependent
	}
	if !facts.Allowed() {
		return identity.ErrForbidden
	}
	return nil
}

// authorizeTaskCreation is deliberately a PROJECT-level gate, never an
// inferred executor/reviewer action: a WorkItem does not exist yet. Legacy
// retains work:write; task_scoped also permits an explicitly trusted creator
// holding work:read+work:claim+work:write (all with current project grants).
// Once created, editing still requires a separate, real WorkItem Claim.
// A valid access:manage manager retains the existing governance create path.
func (r Repository) authorizeTaskCreation(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	project identity.Project, mode domain.TaskAuthorizationMode) error {
	if mode == domain.LegacyAuthorization {
		return r.authorizeTaskWrite(ctx, tx, actor, project, mode,
			domain.WorkItem{}, "", idomain.WorkWrite, "", false)
	}
	if mode != domain.TaskScopedAuthorization {
		return identity.ErrForbidden
	}
	s, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return err
	}
	managerEffect, err := overrideEffect(ctx, tx, actor, project, idomain.AccessManage)
	if err != nil {
		return err
	}
	if idomain.CanWithEffect(s.kind, s.role, s.scopes, idomain.AccessManage, managerEffect) {
		return nil
	}
	if s.kind != idomain.Human && s.kind != idomain.Agent {
		return identity.ErrForbidden
	}
	for _, permission := range []idomain.Permission{idomain.WorkRead, idomain.WorkClaim, idomain.WorkWrite} {
		effect, err := overrideEffect(ctx, tx, actor, project, permission)
		if err != nil {
			return err
		}
		if !idomain.CanWithEffect(s.kind, s.role, s.scopes, permission, effect) {
			return identity.ErrForbidden
		}
	}
	return nil
}

// Ensure this repository satisfies the optional project-mode contract.
var _ application.TaskModeRepository = Repository{}
