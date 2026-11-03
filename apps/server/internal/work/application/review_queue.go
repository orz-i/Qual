package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// ReviewQueueFilter applies bounded, non-snapshot offset pagination.
type ReviewQueueFilter struct {
	Limit  int
	Offset int
}

type ReviewLane string

const (
	ReviewAwaiting     ReviewLane = "awaiting"
	ReviewNeedsWork    ReviewLane = "needs_work"
	ReviewReadyToClose ReviewLane = "ready_to_close"
)

// ClassifyReviewLane derives the queue classification from actual immutable
// Reviewer history and a server-observed later in_review transition. A fresh
// re-submission after the last Reviewer verdict requests a NEW decision; it
// does not erase the prior needs_work or turn an old passed into approval.
func ClassifyReviewLane(status domain.Status, review *ReviewQueueDecision, resubmittedAfterReview bool) ReviewLane {
	if status == domain.InReview && review != nil && resubmittedAfterReview {
		return ReviewAwaiting
	}
	if review != nil {
		switch review.Outcome {
		case domain.NeedsWork, domain.Failed:
			return ReviewNeedsWork
		case domain.Passed:
			if status == domain.InReview && !review.SelfReview {
				return ReviewReadyToClose
			}
		}
	}
	return ReviewAwaiting
}

// ReviewQueueDecision is the most recent immutable Reviewer entry for a work item.
// It never implies an automatic status transition or independent approval.
type ReviewQueueDecision struct {
	ID             uuid.UUID            `json:"id"`
	ActorID        uuid.UUID            `json:"actor_id"`
	Outcome        domain.Outcome       `json:"outcome"`
	Summary        string               `json:"summary"`
	ReferenceKind  domain.ReferenceKind `json:"reference_kind"`
	ReferenceValue string               `json:"reference_value"`
	WorkVersion    int64                `json:"work_version"`
	SelfReview     bool                 `json:"self_review"`
	RecordedAt     time.Time            `json:"recorded_at"`
}

type ReviewQueueItem struct {
	WorkItemID             uuid.UUID            `json:"work_item_id"`
	Key                    string               `json:"key"`
	Title                  string               `json:"title"`
	Kind                   domain.Kind          `json:"kind"`
	Status                 domain.Status        `json:"status"`
	ReviewLane             ReviewLane           `json:"review_lane"`
	ResubmittedAfterReview bool                 `json:"resubmitted_after_review"`
	Priority               domain.Priority      `json:"priority"`
	Version                int64                `json:"version"`
	AssigneeID             *uuid.UUID           `json:"assignee_id"`
	UpdatedAt              time.Time            `json:"updated_at"`
	LatestReview           *ReviewQueueDecision `json:"latest_review,omitempty"`
}

type ReviewQueuePage struct {
	Items      []ReviewQueueItem `json:"items"`
	HasMore    bool              `json:"has_more"`
	NextOffset *int              `json:"next_offset,omitempty"`
}

// ReviewQueue is a read-only project WorkItem + RoleEntry projection.
// Reviewers and Auditors never gain additional permissions through reading it.
func (s Service) ReviewQueue(ctx context.Context, actor identity.Actor, pid uuid.UUID, filter ReviewQueueFilter) (ReviewQueuePage, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, idomain.WorkRead); err != nil {
		return ReviewQueuePage{}, err
	}
	if s.Roles == nil {
		return ReviewQueuePage{}, ErrNotFound
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 || filter.Offset > 10000 {
		return ReviewQueuePage{}, domain.ErrValidation
	}
	return s.Roles.ListReviewQueue(ctx, pid, filter)
}
