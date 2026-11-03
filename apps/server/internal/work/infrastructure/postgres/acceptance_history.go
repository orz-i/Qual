package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// ReadAcceptanceHistory never manufactures Reviewer approval for pre-Goose13
// Done. The projection is allowed under work:read; it exposes no credentials.
func (r Repository) ReadAcceptanceHistory(ctx context.Context, projectID, itemID uuid.UUID) (application.AcceptanceHistory, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return application.AcceptanceHistory{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	h := application.AcceptanceHistory{WorkItemID: itemID}
	err = tx.QueryRow(ctx, `SELECT status, version FROM qual.work_items WHERE project_id=$1 AND id=$2`, projectID, itemID).Scan(&h.WorkStatus, &h.WorkVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.AcceptanceHistory{}, application.ErrNotFound
	}
	if err != nil {
		return h, err
	}

	var enabled bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('qual.work_acceptance_decisions') IS NOT NULL`).Scan(&enabled); err != nil {
		return h, err
	}
	if enabled {
		var d application.AcceptanceDecisionRecord
		err = tx.QueryRow(ctx, `SELECT id,actor_id,acceptance_mode,independent,
          explicit_self_review,reviewer_entry_id,reason,policy_source,policy_version,
          reference_kind,reference_value,work_version_before,work_version_after,accepted_at
          FROM qual.work_acceptance_decisions WHERE project_id=$1 AND work_item_id=$2`,
			projectID, itemID).Scan(&d.ID, &d.ActorID, &d.Mode, &d.Independent, &d.ExplicitSelfReview,
			&d.ReviewerEntryID, &d.Reason, &d.PolicySource, &d.PolicyVersion, &d.ReferenceKind, &d.ReferenceValue,
			&d.WorkVersionBefore, &d.WorkVersionAfter, &d.AcceptedAt)
		if err == nil {
			// Comments and other append-only non-material history can advance the
			// aggregate version after Done. Preserve the original decision's
			// signed version; never misclassify it as stale or fabricate a new
			// Reviewer just because the WorkItem received a comment later.
			if h.WorkStatus != domain.Done || d.WorkVersionAfter > h.WorkVersion ||
				(d.Mode == domain.IndependentReview && (!d.Independent || d.ExplicitSelfReview)) ||
				(d.Mode == domain.OwnerAcceptance && (d.Independent || !d.ExplicitSelfReview)) {
				return application.AcceptanceHistory{}, domain.ErrDoneGate
			}
			h.AcceptanceMode = string(d.Mode)
			h.Decision = &d
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return h, err
		}
	}
	if h.Decision == nil {
		if h.WorkStatus == domain.Done {
			h.AcceptanceMode = "legacy_unclassified"
		} else {
			h.AcceptanceMode = "not_completed"
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return application.AcceptanceHistory{}, err
	}
	return h, nil
}
