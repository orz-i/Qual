package postgres

import (
	"bytes"
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

func defaultProjectReviewPolicy(projectID uuid.UUID) application.ProjectReviewPolicyView {
	return application.ProjectReviewPolicyView{
		ProjectID: projectID, DefaultMode: domain.IndependentReview,
		OwnerAcceptanceAllowed: false, ForceIndependentForHighRisk: true,
		Version: 0, Source: "project_default",
	}
}

func (r Repository) GetProjectReviewPolicy(ctx context.Context, projectID uuid.UUID) (application.ProjectReviewPolicyView, error) {
	var enabled bool
	if err := r.Pool.QueryRow(ctx, `SELECT to_regclass('qual.project_review_policies') IS NOT NULL`).Scan(&enabled); err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	if !enabled {
		return application.ProjectReviewPolicyView{}, domain.ErrDoneGate
	}
	p := defaultProjectReviewPolicy(projectID)
	var mode string
	err := r.Pool.QueryRow(ctx, `SELECT default_mode,owner_acceptance_allowed,
      force_independent_for_high_risk,version
      FROM qual.project_review_policies WHERE project_id=$1`, projectID).Scan(
		&mode, &p.OwnerAcceptanceAllowed, &p.ForceIndependentForHighRisk, &p.Version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return p, nil
	case err != nil:
		return application.ProjectReviewPolicyView{}, err
	default:
		p.DefaultMode = domain.ReviewPolicyMode(mode)
		p.Source = "project"
		return p, nil
	}
}

// ChangeProjectReviewPolicy requires verified access:manage from Application.
// Broadening Owner acceptance additionally requires a real Human Workspace
// Admin; even a delegated Agent manager may only tighten a policy.
// Project FOR UPDATE synchronizes policy insertion/changes with Done Gate's
// shared lock, including the no-explicit-policy-row case.
func (r Repository) ChangeProjectReviewPolicy(ctx context.Context, actor identity.Actor, project identity.Project,
	input application.ProjectReviewPolicyChange, meta application.Meta, hash [32]byte) (application.ProjectReviewPolicyView, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('qual.project_review_policies') IS NOT NULL`).Scan(&enabled); err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	if !enabled {
		return application.ProjectReviewPolicyView{}, domain.ErrDoneGate
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.idempotency_records
      (project_id,actor_id,operation,request_key,request_hash)
      VALUES($1,$2,'review_policy.change',$3,$4) ON CONFLICT DO NOTHING`,
		project.ID, actor.PrincipalID, meta.IdempotencyKey, hash[:])
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	var oldHash, oldResponse []byte
	var savedStatus int
	err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
      FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2
       AND operation='review_policy.change' AND request_key=$3 FOR UPDATE`,
		project.ID, actor.PrincipalID, meta.IdempotencyKey).Scan(&oldHash, &oldResponse, &savedStatus)
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	if !bytes.Equal(oldHash, hash[:]) {
		return application.ProjectReviewPolicyView{}, application.ErrIdempotency
	}
	if savedStatus == 200 {
		var replay application.ProjectReviewPolicyView
		if err := json.Unmarshal(oldResponse, &replay); err != nil {
			return replay, err
		}
		if err := tx.Commit(ctx); err != nil {
			return replay, err
		}
		return replay, nil
	}
	if savedStatus != 0 {
		return application.ProjectReviewPolicyView{}, application.ErrIdempotency
	}
	var projectID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM qual.projects
       WHERE id=$1 AND workspace_id=$2 FOR UPDATE`,
		project.ID, project.WorkspaceID).Scan(&projectID); err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	old := defaultProjectReviewPolicy(project.ID)
	var mode string
	err = tx.QueryRow(ctx, `SELECT default_mode,owner_acceptance_allowed,
       force_independent_for_high_risk,version
       FROM qual.project_review_policies WHERE project_id=$1 FOR UPDATE`, project.ID).Scan(
		&mode, &old.OwnerAcceptanceAllowed, &old.ForceIndependentForHighRisk, &old.Version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return application.ProjectReviewPolicyView{}, err
	}
	if err == nil {
		old.DefaultMode, old.Source = domain.ReviewPolicyMode(mode), "project"
	}
	if old.Version != input.ExpectedVersion {
		return application.ProjectReviewPolicyView{}, domain.ErrConflict
	}
	if old.DefaultMode == input.DefaultMode &&
		old.OwnerAcceptanceAllowed == input.OwnerAcceptanceAllowed &&
		old.ForceIndependentForHighRisk == input.ForceIndependentForHighRisk {
		return application.ProjectReviewPolicyView{}, domain.ErrValidation
	}
	loosening := (!old.OwnerAcceptanceAllowed && input.OwnerAcceptanceAllowed) ||
		(old.ForceIndependentForHighRisk && !input.ForceIndependentForHighRisk) ||
		(old.DefaultMode != domain.OwnerAcceptance && input.DefaultMode == domain.OwnerAcceptance)
	if loosening {
		if actor.Type != idomain.Human {
			return application.ProjectReviewPolicyView{}, identity.ErrForbidden
		}
		var workspaceAdmin bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.memberships m
           WHERE m.workspace_id=$1 AND m.principal_id=$2
             AND m.project_id IS NULL AND m.role='workspace_admin')`,
			project.WorkspaceID, actor.PrincipalID).Scan(&workspaceAdmin)
		if err != nil {
			return application.ProjectReviewPolicyView{}, err
		}
		if !workspaceAdmin {
			return application.ProjectReviewPolicyView{}, identity.ErrForbidden
		}
	}
	next := application.ProjectReviewPolicyView{
		ProjectID:                   project.ID,
		DefaultMode:                 input.DefaultMode,
		OwnerAcceptanceAllowed:      input.OwnerAcceptanceAllowed,
		ForceIndependentForHighRisk: input.ForceIndependentForHighRisk,
		Version:                     old.Version + 1,
		Source:                      "project",
	}
	if old.Version == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO qual.project_review_policies
          (workspace_id,project_id,default_mode,owner_acceptance_allowed,
           force_independent_for_high_risk,version,updated_by)
          VALUES($1,$2,$3,$4,$5,$6,$7)`,
			project.WorkspaceID, project.ID, next.DefaultMode,
			next.OwnerAcceptanceAllowed, next.ForceIndependentForHighRisk, next.Version, actor.PrincipalID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE qual.project_review_policies
          SET default_mode=$1,owner_acceptance_allowed=$2,
           force_independent_for_high_risk=$3,version=$4,updated_by=$5,updated_at=$6
          WHERE project_id=$7 AND version=$8`,
			next.DefaultMode, next.OwnerAcceptanceAllowed,
			next.ForceIndependentForHighRisk, next.Version, actor.PrincipalID,
			time.Now().UTC(), project.ID, old.Version)
	}
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.review_policy_changes
      (id,workspace_id,project_id,actor_id,credential_id,request_id,
       old_mode,new_mode,old_owner_acceptance_allowed,new_owner_acceptance_allowed,
       old_force_independent_for_high_risk,new_force_independent_for_high_risk,
       old_version,new_version,reason)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		uuid.New(), project.WorkspaceID, project.ID, actor.PrincipalID,
		actor.CredentialID, meta.RequestID,
		old.DefaultMode, next.DefaultMode, old.OwnerAcceptanceAllowed,
		next.OwnerAcceptanceAllowed, old.ForceIndependentForHighRisk,
		next.ForceIndependentForHighRisk, old.Version, next.Version, input.Reason)
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	prevVersion := old.Version
	if err := r.emit(ctx, tx, actor, project, meta.RequestID,
		"project.review_policy_changed", project.ID, &prevVersion, next.Version,
		map[string]any{"from": old, "to": next, "reason": input.Reason}); err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	body, err := json.Marshal(next)
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records
      SET response_json=$1,response_status=200
      WHERE project_id=$2 AND actor_id=$3 AND operation='review_policy.change' AND request_key=$4`,
		body, project.ID, actor.PrincipalID, meta.IdempotencyKey)
	if err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return application.ProjectReviewPolicyView{}, err
	}
	return next, nil
}
