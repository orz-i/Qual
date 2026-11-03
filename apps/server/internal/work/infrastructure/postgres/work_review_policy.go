package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

func (r Repository) GetWorkReviewPolicy(ctx context.Context, projectID, itemID uuid.UUID) (application.WorkReviewPolicyView, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return application.WorkReviewPolicyView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	enabled, err := acceptanceSchemaEnabled(ctx, tx)
	if err != nil {
		return application.WorkReviewPolicyView{}, err
	}
	if !enabled {
		return application.WorkReviewPolicyView{}, domain.ErrDoneGate
	}
	item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2`, projectID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return application.WorkReviewPolicyView{}, application.ErrNotFound
	}
	if err != nil {
		return application.WorkReviewPolicyView{}, err
	}
	_, view, err := resolveWorkReviewPolicy(ctx, tx, item)
	if err != nil {
		return application.WorkReviewPolicyView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return application.WorkReviewPolicyView{}, err
	}
	return view, nil
}

// ChangeWorkReviewPolicy is an idempotent, CAS and audit-bound business
// mutation. It never grants review permission or creates a Reviewer entry.
// Project locking follows the same order as Complete() and project policy
// updates. Any actual policy edit invalidates older Reviewer approval.
func (r Repository) ChangeWorkReviewPolicy(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID uuid.UUID, in application.WorkReviewPolicyChange, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	body, err := r.idempotent(ctx, actor, project, "work.review_policy.change", meta, hash,
		func(tx pgx.Tx) (domain.WorkItem, error) {
			enabled, err := acceptanceSchemaEnabled(ctx, tx)
			if err != nil {
				return domain.WorkItem{}, err
			}
			if !enabled {
				return domain.WorkItem{}, domain.ErrDoneGate
			}
			var locked uuid.UUID
			err = tx.QueryRow(ctx, `SELECT id FROM qual.projects WHERE id=$1 AND workspace_id=$2 FOR UPDATE`,
				project.ID, project.WorkspaceID).Scan(&locked)
			if err != nil {
				return domain.WorkItem{}, err
			}
			item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.WorkItem{}, application.ErrNotFound
			}
			if err != nil {
				return domain.WorkItem{}, err
			}
			if item.Version != in.ExpectedVersion {
				return domain.WorkItem{}, domain.ErrConflict
			}
			if item.Status == domain.Done || item.Status == domain.Cancelled {
				return domain.WorkItem{}, domain.ErrTransition
			}
			_, previous, err := resolveWorkReviewPolicy(ctx, tx, item)
			if err != nil {
				return domain.WorkItem{}, err
			}
			if previous.OverrideVersion != in.ExpectedPolicyVersion {
				return domain.WorkItem{}, domain.ErrConflict
			}
			if previous.Mode == in.Mode {
				return domain.WorkItem{}, domain.ErrValidation
			}
			if in.Mode == domain.OwnerAcceptance {
				if !previous.OwnerAcceptanceAllowed || (previous.HighRisk && previous.ForceIndependentForHighRisk) {
					return domain.WorkItem{}, domain.ErrDoneGate
				}
				// Even a delegated access:manage Agent cannot expand Owner
				// self-acceptance. Requires a real Human Workspace Admin.
				if actor.Type != idomain.Human {
					return domain.WorkItem{}, identity.ErrForbidden
				}
				var admin bool
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.memberships
             WHERE workspace_id=$1 AND principal_id=$2 AND project_id IS NULL AND role='workspace_admin')`,
					project.WorkspaceID, actor.PrincipalID).Scan(&admin)
				if err != nil {
					return domain.WorkItem{}, err
				}
				if !admin {
					return domain.WorkItem{}, identity.ErrForbidden
				}
			}
			newVersion := previous.OverrideVersion + 1
			if previous.OverrideVersion == 0 {
				_, err = tx.Exec(ctx, `INSERT INTO qual.work_review_policy_overrides
              (workspace_id,project_id,work_item_id,mode,version,changed_by)
              VALUES($1,$2,$3,$4,$5,$6)`,
					project.WorkspaceID, project.ID, item.ID, in.Mode, newVersion, actor.PrincipalID)
			} else {
				tag, e := tx.Exec(ctx, `UPDATE qual.work_review_policy_overrides
              SET mode=$1,version=$2,changed_by=$3,updated_at=$4
              WHERE project_id=$5 AND work_item_id=$6 AND version=$7`,
					in.Mode, newVersion, actor.PrincipalID, time.Now().UTC(), project.ID, item.ID, previous.OverrideVersion)
				err = e
				if err == nil && tag.RowsAffected() != 1 {
					return domain.WorkItem{}, domain.ErrConflict
				}
			}
			if err != nil {
				return domain.WorkItem{}, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO qual.review_policy_changes
         (id,workspace_id,project_id,work_item_id,actor_id,credential_id,request_id,
          old_mode,new_mode,old_owner_acceptance_allowed,new_owner_acceptance_allowed,
          old_force_independent_for_high_risk,new_force_independent_for_high_risk,
          old_version,new_version,reason)
         VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
				uuid.New(), project.WorkspaceID, project.ID, item.ID, actor.PrincipalID, actor.CredentialID, meta.RequestID,
				previous.Mode, in.Mode, previous.OwnerAcceptanceAllowed, previous.OwnerAcceptanceAllowed,
				previous.ForceIndependentForHighRisk, previous.ForceIndependentForHighRisk,
				previous.OverrideVersion, newVersion, in.Reason)
			if err != nil {
				return domain.WorkItem{}, err
			}
			before := item.Version
			if err := item.Touch(before, time.Now()); err != nil {
				return domain.WorkItem{}, err
			}
			if err := updateTouched(ctx, tx, item, before); err != nil {
				return domain.WorkItem{}, err
			}
			err = r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.review_policy_changed",
				item.ID, &before, item.Version, map[string]any{"from": previous.Mode, "to": in.Mode,
					"source": "work_item", "policy_version": newVersion, "reason": in.Reason})
			return item, err
		})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var item domain.WorkItem
	err = json.Unmarshal(body, &item)
	return item, err
}
