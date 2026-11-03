package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// AcceptanceHistory is a read-only projection. A historical Done without a
// persisted decision is explicitly legacy_unclassified, NEVER independent.
type AcceptanceHistory struct {
	WorkItemID     uuid.UUID                 `json:"work_item_id"`
	WorkStatus     domain.Status             `json:"work_status"`
	WorkVersion    int64                     `json:"work_version"`
	AcceptanceMode string                    `json:"acceptance_mode"`
	Decision       *AcceptanceDecisionRecord `json:"decision"`
}

type AcceptanceDecisionRecord struct {
	ID                 uuid.UUID                 `json:"id"`
	ActorID            uuid.UUID                 `json:"actor_id"`
	Mode               domain.ReviewPolicyMode   `json:"acceptance_mode"`
	Independent        bool                      `json:"independent"`
	ExplicitSelfReview bool                      `json:"explicit_self_review"`
	ReviewerEntryID    *uuid.UUID                `json:"reviewer_entry_id"`
	Reason             string                    `json:"reason"`
	PolicySource       domain.ReviewPolicySource `json:"policy_source"`
	PolicyVersion      int64                     `json:"policy_version"`
	ReferenceKind      domain.ReferenceKind      `json:"reference_kind"`
	ReferenceValue     string                    `json:"reference_value"`
	WorkVersionBefore  int64                     `json:"work_version_before"`
	WorkVersionAfter   int64                     `json:"work_version_after"`
	AcceptedAt         time.Time                 `json:"accepted_at"`
}

type AcceptanceHistoryRepository interface {
	ReadAcceptanceHistory(context.Context, uuid.UUID, uuid.UUID) (AcceptanceHistory, error)
}

func (s Service) ReadAcceptanceHistory(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) (AcceptanceHistory, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return AcceptanceHistory{}, err
	}
	if s.AcceptanceHistory == nil {
		return AcceptanceHistory{}, domain.ErrDoneGate
	}
	return s.AcceptanceHistory.ReadAcceptanceHistory(ctx, projectID, itemID)
}
