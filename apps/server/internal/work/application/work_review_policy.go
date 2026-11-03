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

type WorkReviewPolicyView struct {
	ProjectID                   uuid.UUID               `json:"project_id"`
	WorkItemID                  uuid.UUID               `json:"work_item_id"`
	Mode                        domain.ReviewPolicyMode `json:"mode"`
	Source                      string                  `json:"source"`
	ProjectPolicyVersion        int64                   `json:"project_policy_version"`
	OverrideVersion             int64                   `json:"override_version"`
	WorkVersion                 int64                   `json:"work_version"`
	OwnerAcceptanceAllowed      bool                    `json:"owner_acceptance_allowed"`
	ForceIndependentForHighRisk bool                    `json:"force_independent_for_high_risk"`
	HighRisk                    bool                    `json:"high_risk"`
	ForcedIndependent           bool                    `json:"forced_independent"`
}

type WorkReviewPolicyChange struct {
	Mode                  domain.ReviewPolicyMode `json:"mode"`
	ExpectedVersion       int64                   `json:"expected_version"`
	ExpectedPolicyVersion int64                   `json:"expected_policy_version"`
	Reason                string                  `json:"reason"`
}

func validReviewPolicyReason(reason string) error {
	count := utf8.RuneCountInString(reason)
	if reason != strings.TrimSpace(reason) || count < 16 || count > 2000 {
		return domain.ErrValidation
	}
	return nil
}

type WorkReviewPolicyRepository interface {
	GetWorkReviewPolicy(context.Context, uuid.UUID, uuid.UUID) (WorkReviewPolicyView, error)
	ChangeWorkReviewPolicy(context.Context, identity.Actor, identity.Project, uuid.UUID, WorkReviewPolicyChange, Meta, [32]byte) (domain.WorkItem, error)
}

func (s Service) GetWorkReviewPolicy(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) (WorkReviewPolicyView, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return WorkReviewPolicyView{}, err
	}
	if s.WorkReviewPolicy == nil {
		return WorkReviewPolicyView{}, domain.ErrDoneGate
	}
	return s.WorkReviewPolicy.GetWorkReviewPolicy(ctx, projectID, itemID)
}

func (s Service) ChangeWorkReviewPolicy(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID,
	in WorkReviewPolicyChange, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if in.ExpectedVersion < 1 || in.ExpectedPolicyVersion < 0 ||
		(in.Mode != domain.IndependentReview && in.Mode != domain.OwnerAcceptance) {
		return domain.WorkItem{}, domain.ErrValidation
	}
	if err := validReviewPolicyReason(in.Reason); err != nil {
		return domain.WorkItem{}, err
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.AccessManage)
	if err != nil {
		return domain.WorkItem{}, err
	}
	if s.WorkReviewPolicy == nil {
		return domain.WorkItem{}, domain.ErrDoneGate
	}
	return s.WorkReviewPolicy.ChangeWorkReviewPolicy(ctx, actor, project, itemID, in, meta,
		hashCommand("work.review_policy.change", struct {
			WorkItemID uuid.UUID
			Input      WorkReviewPolicyChange
		}{itemID, in}))
}
