package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// resolveWorkReviewPolicy is the only authority for policy precedence.
// Both /complete and the read-only UI use it under a PostgreSQL snapshot.
// WorkItem overrides never break a project-level safety upper bound.
func resolveWorkReviewPolicy(ctx context.Context, tx pgx.Tx, item domain.WorkItem) (domain.EffectiveReviewPolicy, application.WorkReviewPolicyView, error) {
	policy := domain.EffectiveReviewPolicy{
		Mode: domain.IndependentReview, Source: domain.ProjectReviewPolicySource,
		Version: 1, AllowOwnerAcceptance: false,
	}
	view := application.WorkReviewPolicyView{
		ProjectID: item.ProjectID, WorkItemID: item.ID, WorkVersion: item.Version,
		Mode: domain.IndependentReview, Source: "project_default", ProjectPolicyVersion: 0,
		OverrideVersion: 0, OwnerAcceptanceAllowed: false, ForceIndependentForHighRisk: true,
	}
	var projectMode string
	var projectVersion int64
	var ownerAllowed, forceHighRisk bool
	err := tx.QueryRow(ctx, `SELECT default_mode,owner_acceptance_allowed,force_independent_for_high_risk,version
        FROM qual.project_review_policies WHERE project_id=$1 FOR SHARE`, item.ProjectID).
		Scan(&projectMode, &ownerAllowed, &forceHighRisk, &projectVersion)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		ownerAllowed = false
		forceHighRisk = true
	case err != nil:
		return policy, view, err
	default:
		policy.Mode = domain.ReviewPolicyMode(projectMode)
		policy.AllowOwnerAcceptance = ownerAllowed
		policy.Version = projectVersion
		view.Source = "project"
		view.ProjectPolicyVersion = projectVersion
		view.OwnerAcceptanceAllowed = ownerAllowed
		view.ForceIndependentForHighRisk = forceHighRisk
	}
	var overrideMode string
	var overrideVersion int64
	err = tx.QueryRow(ctx, `SELECT mode,version FROM qual.work_review_policy_overrides
        WHERE project_id=$1 AND work_item_id=$2 FOR SHARE`, item.ProjectID, item.ID).
		Scan(&overrideMode, &overrideVersion)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return policy, view, err
	default:
		policy.Mode = domain.ReviewPolicyMode(overrideMode)
		policy.Source = domain.WorkReviewPolicySource
		policy.Version = overrideVersion
		view.Source = "work_item"
		view.OverrideVersion = overrideVersion
	}
	view.HighRisk = item.Priority == domain.Urgent ||
		(item.Kind == domain.Bug && item.BugDetails != nil &&
			(item.BugDetails.Severity == "blocker" || item.BugDetails.Severity == "critical"))
	policy.ForceIndependent = view.HighRisk && view.ForceIndependentForHighRisk
	view.ForcedIndependent = policy.ForceIndependent
	// Denying Owner at the project level or forcing independent for urgent
	// work MUST still permit a valid independent decision.
	if policy.Mode == domain.OwnerAcceptance && (!policy.AllowOwnerAcceptance || policy.ForceIndependent) {
		policy.Mode = domain.IndependentReview
		view.ForcedIndependent = true
	}
	view.Mode = policy.Mode
	return policy, view, nil
}
