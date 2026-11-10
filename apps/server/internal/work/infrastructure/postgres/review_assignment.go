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

// activeReviewer is a current task relationship, not an old RoleEntry. The
// caller holds the Project + WorkItem locks before inspecting this relation.
// Goose18 and older schemas have no reviewer assignment table.
func activeReviewer(ctx context.Context, tx pgx.Tx, item domain.WorkItem) (*uuid.UUID, int64, error) {
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('qual.work_review_assignments') IS NOT NULL").Scan(&installed); err != nil {
		return nil, 0, err
	}
	if !installed {
		return nil, 0, nil
	}
	var principal uuid.UUID
	var claimedVersion int64
	err := tx.QueryRow(ctx, `SELECT reviewer_id, claimed_work_version
		FROM qual.work_review_assignments
		WHERE project_id=$1 AND work_item_id=$2 FOR SHARE`, item.ProjectID, item.ID).Scan(&principal, &claimedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return &principal, claimedVersion, nil
}

// wasTaskExecutor denies independence for the same real WorkItem only. From
// Goose20 a TestAttempt's immutable Run work_item_id is authoritative;
// a shared published Case does NOT turn a tester on Y into a tester on X.
// Legacy unbound runs remain conservatively attributed via Case coverage.
func wasTaskExecutor(ctx context.Context, tx pgx.Tx, item domain.WorkItem, principal uuid.UUID) (bool, error) {
	if principal == uuid.Nil || principal == item.CreatedBy ||
		(item.AssigneeID != nil && *item.AssigneeID == principal) ||
		(item.AccountableOwnerID != nil && *item.AccountableOwnerID == principal) {
		return true, nil
	}
	var found bool
	err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM qual.work_role_entries
			WHERE project_id=$1 AND work_item_id=$2 AND actor_id=$3 AND capacity IN ('executor','tester'))
		OR EXISTS(SELECT 1 FROM qual.audit_records
			WHERE project_id=$1 AND entity_id=$2 AND operation='work_item.claim' AND actor_id=$3)
		OR EXISTS(SELECT 1 FROM qual.audit_records
			WHERE project_id=$1 AND entity_id=$2 AND operation='work_item.reassign'
			  AND diff->>'new_assignee_id'=$3::uuid::text)`,
		item.ProjectID, item.ID, principal).Scan(&found)
	if err != nil || found {
		return found, err
	}
	bound, err := taskRunBindingAvailable(ctx, tx)
	if err != nil {
		return false, err
	}
	if !bound {
		err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM qual.test_attempts a
			JOIN qual.test_run_items ri ON ri.id=a.run_item_id AND ri.project_id=a.project_id
			JOIN qual.test_case_coverage c ON c.project_id=ri.project_id
			  AND c.test_case_id=ri.test_case_id AND c.work_item_id=$2
			WHERE a.project_id=$1 AND a.reported_by=$3)`,
			item.ProjectID, item.ID, principal).Scan(&found)
		return found, err
	}
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM qual.test_attempts a
		JOIN qual.test_run_items ri ON ri.id=a.run_item_id AND ri.project_id=a.project_id
		JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id
		WHERE a.project_id=$1 AND a.reported_by=$3
		  AND (r.work_item_id=$2 OR
			   (r.work_item_id IS NULL AND EXISTS(
				SELECT 1 FROM qual.test_case_coverage c
				WHERE c.project_id=ri.project_id AND c.test_case_id=ri.test_case_id
				  AND c.work_item_id=$2))))`,
		item.ProjectID, item.ID, principal).Scan(&found)
	return found, err
}

// dropActiveReviewer is called under the WorkItem lock whenever the reviewed
// version leaves in_review. A previously recorded Reviewer fact is immutable,
// but its current authority ceases atomically with return/done/cancel.
func dropActiveReviewer(ctx context.Context, tx pgx.Tx, item domain.WorkItem) error {
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('qual.work_review_assignments') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	_, err := tx.Exec(ctx, `DELETE FROM qual.work_review_assignments
		WHERE project_id=$1 AND work_item_id=$2`, item.ProjectID, item.ID)
	return err
}

// ClaimReview is only meaningful for task_scoped projects. It never changes
// WorkItem assignee or Principal identity, and it cannot claim for another
// user. A former assignee cannot gain independence after handing the task off.
func (r Repository) ClaimReview(ctx context.Context, actor identity.Actor, project identity.Project, itemID uuid.UUID,
	expected int64, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "review_claim", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		mode, err := r.lockTaskMode(ctx, tx, project)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if mode != domain.TaskScopedAuthorization {
			return domain.WorkItem{}, identity.ErrForbidden
		}
		var installed bool
		if err := tx.QueryRow(ctx, "SELECT to_regclass('qual.work_review_assignments') IS NOT NULL").Scan(&installed); err != nil {
			return domain.WorkItem{}, err
		}
		if !installed {
			return domain.WorkItem{}, domain.ErrDoneGate
		}
		item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		if item.Version != expected {
			return domain.WorkItem{}, domain.ErrConflict
		}
		policy, _, err := resolveWorkReviewPolicy(ctx, tx, item)
		if err != nil {
			return domain.WorkItem{}, err
		}
		current, _, err := activeReviewer(ctx, tx, item)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if current != nil {
			return domain.WorkItem{}, domain.ErrConflict
		}
		if err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, item,
			domain.TaskClaimReview, idomain.WorkReview, policy.Mode, policy.ForceIndependent); err != nil {
			return domain.WorkItem{}, err
		}
		former, err := wasTaskExecutor(ctx, tx, item, actor.PrincipalID)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if former {
			return domain.WorkItem{}, identity.ErrForbidden
		}
		before := item.Version
		if err := item.Touch(expected, time.Now()); err != nil {
			return domain.WorkItem{}, err
		}
		if err := updateTouched(ctx, tx, item, before); err != nil {
			return domain.WorkItem{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO qual.work_review_assignments
			(workspace_id,project_id,work_item_id,reviewer_id,claimed_work_version)
			VALUES($1,$2,$3,$4,$5)`,
			project.WorkspaceID, project.ID, item.ID, actor.PrincipalID, item.Version)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if err := r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.review_claim", item.ID,
			&before, item.Version, map[string]any{
				"reviewer_id": actor.PrincipalID, "claimed_work_version": item.Version,
				"policy_mode": policy.Mode,
			}); err != nil {
			return domain.WorkItem{}, err
		}
		return item, nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var result domain.WorkItem
	err = json.Unmarshal(data, &result)
	return result, err
}

// ReleaseReview revokes only the live review relationship; a later reviewer
// must claim again and produce a NEW valid review fact for that claim session.
func (r Repository) ReleaseReview(ctx context.Context, actor identity.Actor, project identity.Project, itemID uuid.UUID,
	expected int64, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "review_release", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		mode, err := r.lockTaskMode(ctx, tx, project)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if mode != domain.TaskScopedAuthorization {
			return domain.WorkItem{}, identity.ErrForbidden
		}
		item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		if item.Version != expected {
			return domain.WorkItem{}, domain.ErrConflict
		}
		if err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, item,
			domain.TaskReleaseReview, idomain.WorkReview, "", false); err != nil {
			return domain.WorkItem{}, err
		}
		before := item.Version
		if err := item.Touch(expected, time.Now()); err != nil {
			return domain.WorkItem{}, err
		}
		if err := dropActiveReviewer(ctx, tx, item); err != nil {
			return domain.WorkItem{}, err
		}
		if err := updateTouched(ctx, tx, item, before); err != nil {
			return domain.WorkItem{}, err
		}
		if err := r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.review_release", item.ID,
			&before, item.Version, map[string]any{"former_reviewer_id": actor.PrincipalID}); err != nil {
			return domain.WorkItem{}, err
		}
		return item, nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var result domain.WorkItem
	err = json.Unmarshal(data, &result)
	return result, err
}
