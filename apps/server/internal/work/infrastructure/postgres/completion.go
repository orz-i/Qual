package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

func acceptanceSchemaEnabled(ctx context.Context, tx pgx.Tx) (bool, error) {
	var present bool
	err := tx.QueryRow(ctx, `SELECT to_regclass('qual.work_acceptance_decisions') IS NOT NULL`).Scan(&present)
	return present, err
}

// Complete serializes WorkItem version, latest role history, policy resolution,
// explicit acceptance, Audit, Activity, Outbox and Idempotency in one PG tx.
// No existing WorkItem or Reviewer history is rewritten.
func (r Repository) Complete(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID uuid.UUID, decision domain.DoneDecision, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "complete", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		enabled, err := acceptanceSchemaEnabled(ctx, tx)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if !enabled {
			return domain.WorkItem{}, domain.ErrDoneGate
		}
		// Lock the project policy namespace, including the case where the
		// project still has no explicit row and inherits conservative defaults.
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM qual.projects WHERE id=$1 AND workspace_id=$2 FOR SHARE`,
			project.ID, project.WorkspaceID).Scan(&locked); err != nil {
			return domain.WorkItem{}, err
		}
		item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		// Check CAS before querying evidence or emitting any record.
		if item.Version != decision.ExpectedVersion {
			return domain.WorkItem{}, domain.ErrConflict
		}

		policy, _, err := resolveWorkReviewPolicy(ctx, tx, item)
		if err != nil {
			return domain.WorkItem{}, err
		}
		var material int64
		err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(new_version),0)
          FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2
           AND operation IN ('work_item.created','work_item.update','work_item.dependency_linked','work_item.review_policy_changed')`,
			project.ID, item.ID).Scan(&material)
		if err != nil {
			return domain.WorkItem{}, err
		}

		var reviews []domain.RoleEntry
		var latest domain.RoleEntry
		var outcome, kind, status string
		err = tx.QueryRow(ctx, `SELECT id,project_id,work_item_id,actor_id,
         outcome,reference_kind,reference_value,work_version,work_status,self_review,recorded_at
         FROM qual.work_role_entries
         WHERE project_id=$1 AND work_item_id=$2 AND capacity='reviewer'
         ORDER BY recorded_at DESC,id DESC LIMIT 1`, project.ID, item.ID).Scan(
			&latest.ID, &latest.ProjectID, &latest.WorkItemID, &latest.ActorID,
			&outcome, &kind, &latest.ReferenceValue, &latest.WorkVersion, &status,
			&latest.SelfReview, &latest.RecordedAt)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return domain.WorkItem{}, err
		default:
			latest.Capacity = domain.Reviewer
			latest.Outcome = domain.Outcome(outcome)
			latest.ReferenceKind = domain.ReferenceKind(kind)
			latest.WorkStatus = domain.Status(status)
			reviews = append(reviews, latest)
		}

		// Only actual, project-local and already Ready references count.
		// An external Git value is NOT verified merely because it matches SHA.
		var validReference bool
		id, parseErr := uuid.Parse(decision.ReferenceValue)
		if parseErr == nil && id != uuid.Nil && id.String() == decision.ReferenceValue {
			switch decision.ReferenceKind {
			case domain.TestRunReference:
				err = tx.QueryRow(ctx, `SELECT EXISTS(
                 SELECT 1 FROM qual.test_runs r
                 JOIN qual.test_run_items ri ON ri.run_id=r.id AND ri.project_id=r.project_id
                 JOIN qual.test_attempts a ON a.run_item_id=ri.id AND a.project_id=r.project_id
                 JOIN qual.test_case_coverage c ON c.test_case_id=ri.test_case_id
                   AND c.project_id=r.project_id AND c.work_item_id=$3
                 WHERE r.id=$1 AND r.project_id=$2
               )`, id, project.ID, item.ID).Scan(&validReference)
			case domain.ArtifactReference:
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.artifacts
                 WHERE id=$1 AND project_id=$2 AND state='ready'
                   AND target_kind='work_item' AND target_id=$3)`, id, project.ID, item.ID).Scan(&validReference)
			}
			if err != nil {
				return domain.WorkItem{}, err
			}
		}
		facts := domain.DoneGateFacts{
			WorkItem: item, Policy: policy,
			// WorkItem is locked: use the actual persisted assignee, never a
			// client-supplied principal or a Human-only special case.
			Actor: domain.DoneGateActor{
				PrincipalID: actor.PrincipalID, HasWorkReview: true,
				DelegatedOwner: domain.IsAssignedOwnerDelegate(item, actor.PrincipalID),
			},
			Decision: decision, ReviewHistory: reviews,
			LastMaterialVersion: material, ReferenceVerified: validReference,
		}
		if err := domain.EvaluateDoneGate(facts); err != nil {
			return domain.WorkItem{}, err
		}
		before := item.Version
		if err := item.Transition(domain.Done, before, true, time.Now()); err != nil {
			return domain.WorkItem{}, err
		}
		tag, err := tx.Exec(ctx, `UPDATE qual.work_items
          SET status='done',version=$1,updated_at=$2
          WHERE project_id=$3 AND id=$4 AND version=$5`,
			item.Version, item.UpdatedAt, project.ID, item.ID, before)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if tag.RowsAffected() != 1 {
			return domain.WorkItem{}, domain.ErrConflict
		}
		independent := decision.Mode == domain.IndependentReview
		var reviewerID *uuid.UUID
		if independent {
			reviewerID = &latest.ID
		}
		acceptanceID := uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO qual.work_acceptance_decisions
          (id,workspace_id,project_id,work_item_id,actor_id,credential_id,
           acceptance_mode,policy_source,policy_version,reviewer_entry_id,
           independent,explicit_self_review,reason,reference_kind,reference_value,
           work_version_before,work_version_after,request_id)
          VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			acceptanceID, project.WorkspaceID, project.ID, item.ID, actor.PrincipalID, actor.CredentialID,
			decision.Mode, policy.Source, policy.Version, reviewerID,
			independent, !independent, decision.Reason, decision.ReferenceKind, decision.ReferenceValue,
			before, item.Version, meta.RequestID)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if err := r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.completed",
			item.ID, &before, item.Version, map[string]any{
				"acceptance_id": acceptanceID, "acceptance_mode": decision.Mode,
				"independent": independent, "policy_source": policy.Source,
				"policy_version": policy.Version, "reviewer_entry_id": reviewerID,
				"reference_kind": decision.ReferenceKind, "reference_value": decision.ReferenceValue,
			}); err != nil {
			return domain.WorkItem{}, err
		}
		return item, nil
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var item domain.WorkItem
	err = json.Unmarshal(data, &item)
	return item, err
}
