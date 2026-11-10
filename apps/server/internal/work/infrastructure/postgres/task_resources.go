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

// AuthorizeTaskResource is the single WorkItem relation authority used by
// Testing and Evidence adapters. Call it in the same mutation transaction
// before creating an immutable Test/Artifact/Reference fact.
// Lock order: Project -> WorkItem -> Credential/Membership/Override.
func (r Repository) AuthorizeTaskResource(ctx context.Context, tx pgx.Tx,
	actor identity.Actor, project identity.Project, workID uuid.UUID,
	action domain.TaskAction, legacyPermission idomain.Permission) error {
	mode, err := r.lockTaskMode(ctx, tx, project)
	if err != nil {
		return err
	}
	w, err := scanItem(tx.QueryRow(ctx, itemSelect+
		` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, workID))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	if err != nil {
		return err
	}
	if mode == domain.TaskScopedAuthorization && actor.Type == idomain.Integration {
		// Existing external reporter is explicitly limited to legacy Test/Artifact
		// project scopes and cannot acquire task role from the relation.
		return r.authorizeTaskWrite(ctx, tx, actor, project, domain.LegacyAuthorization, w, "", legacyPermission, "", false)
	}
	return r.authorizeTaskWrite(ctx, tx, actor, project, mode, w, action, legacyPermission, "", false)
}

// AuthorizeUnboundResource gates project-level Run/Artifact mutations.
// In scoped mode the legacy broad task scope alone cannot create shared facts.
// Integrations retain their independently granted reporter permissions.
func (r Repository) AuthorizeUnboundResource(ctx context.Context, tx pgx.Tx,
	actor identity.Actor, project identity.Project, permission idomain.Permission) error {
	mode, err := r.lockTaskMode(ctx, tx, project)
	if err != nil {
		return err
	}
	s, err := r.credentialAndRole(ctx, tx, actor, project)
	if err != nil {
		return err
	}
	effect, err := overrideEffect(ctx, tx, actor, project, permission)
	if err != nil {
		return err
	}
	if !idomain.CanWithEffect(s.kind, s.role, s.scopes, permission, effect) {
		return identity.ErrForbidden
	}
	if mode == domain.TaskScopedAuthorization && s.kind != idomain.Integration {
		manage, err := overrideEffect(ctx, tx, actor, project, idomain.AccessManage)
		if err != nil {
			return err
		}
		if !idomain.CanWithEffect(s.kind, s.role, s.scopes, idomain.AccessManage, manage) {
			return identity.ErrForbidden
		}
	}
	return nil
}

// AuthorizeTaskResourceRead ensures a TestRun or private Artifact belongs to
// the caller's current task. Scope grants are evaluated on each request.
func (r Repository) AuthorizeTaskResourceRead(ctx context.Context,
	actor identity.Actor, project identity.Project, workID uuid.UUID,
	action domain.TaskAction, legacyPermission idomain.Permission) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	mode, err := r.lockTaskMode(ctx, tx, project)
	if err != nil {
		return err
	}
	w, err := scanItem(tx.QueryRow(ctx, itemSelect+
		` WHERE w.project_id=$1 AND w.id=$2 FOR SHARE OF w`, project.ID, workID))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ErrNotFound
	}
	if err != nil {
		return err
	}
	if mode != domain.TaskScopedAuthorization || actor.Type == idomain.Integration {
		err = r.authorizeTaskWrite(ctx, tx, actor, project, domain.LegacyAuthorization, w, "", legacyPermission, "", false)
	} else {
		err = r.authorizeTaskWrite(ctx, tx, actor, project, mode, w, action, legacyPermission, "", false)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r Repository) AuthorizeUnboundResourceRead(ctx context.Context, actor identity.Actor,
	project identity.Project, permission idomain.Permission) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = r.AuthorizeUnboundResource(ctx, tx, actor, project, permission); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
