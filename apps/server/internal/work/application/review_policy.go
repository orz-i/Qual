package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// ProjectReviewPolicyView is a service-computed policy and CAS version.
// Version zero means no explicit project row: independent, Owner disabled.
type ProjectReviewPolicyView struct {
	ProjectID                   uuid.UUID               `json:"project_id"`
	DefaultMode                 domain.ReviewPolicyMode `json:"default_mode"`
	OwnerAcceptanceAllowed      bool                    `json:"owner_acceptance_allowed"`
	ForceIndependentForHighRisk bool                    `json:"force_independent_for_high_risk"`
	Version                     int64                   `json:"version"`
	Source                      string                  `json:"source"`
}

type ProjectReviewPolicyChange struct {
	DefaultMode                 domain.ReviewPolicyMode `json:"default_mode"`
	OwnerAcceptanceAllowed      bool                    `json:"owner_acceptance_allowed"`
	ForceIndependentForHighRisk bool                    `json:"force_independent_for_high_risk"`
	ExpectedVersion             int64                   `json:"expected_version"`
	Reason                      string                  `json:"reason"`
}

func (in ProjectReviewPolicyChange) Validate() error {
	if in.ExpectedVersion < 0 ||
		(in.DefaultMode != domain.IndependentReview && in.DefaultMode != domain.OwnerAcceptance) ||
		(in.DefaultMode == domain.OwnerAcceptance && !in.OwnerAcceptanceAllowed) ||
		strings.TrimSpace(in.Reason) != in.Reason ||
		utf8.RuneCountInString(in.Reason) < 16 || utf8.RuneCountInString(in.Reason) > 2000 {
		return domain.ErrValidation
	}
	return nil
}

type ProjectReviewPolicyRepository interface {
	GetProjectReviewPolicy(context.Context, uuid.UUID) (ProjectReviewPolicyView, error)
	ChangeProjectReviewPolicy(context.Context, identity.Actor, identity.Project,
		ProjectReviewPolicyChange, Meta, [32]byte) (ProjectReviewPolicyView, error)
}

func (s Service) GetProjectReviewPolicy(ctx context.Context, actor identity.Actor, projectID uuid.UUID) (ProjectReviewPolicyView, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return ProjectReviewPolicyView{}, err
	}
	if s.ReviewPolicy == nil {
		return ProjectReviewPolicyView{}, domain.ErrDoneGate
	}
	return s.ReviewPolicy.GetProjectReviewPolicy(ctx, projectID)
}

func (s Service) ChangeProjectReviewPolicy(ctx context.Context, actor identity.Actor, projectID uuid.UUID,
	change ProjectReviewPolicyChange, meta Meta) (ProjectReviewPolicyView, error) {
	if err := validMeta(meta); err != nil {
		return ProjectReviewPolicyView{}, err
	}
	if err := change.Validate(); err != nil {
		return ProjectReviewPolicyView{}, err
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.AccessManage)
	if err != nil {
		return ProjectReviewPolicyView{}, err
	}
	if s.ReviewPolicy == nil {
		return ProjectReviewPolicyView{}, domain.ErrDoneGate
	}
	return s.ReviewPolicy.ChangeProjectReviewPolicy(ctx, actor, project, change, meta,
		hashCommand("review_policy.change", struct {
			ProjectID uuid.UUID
			Change    ProjectReviewPolicyChange
		}{projectID, change}))
}
