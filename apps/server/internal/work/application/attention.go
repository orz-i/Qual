package application

import (
	"context"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// Attention is a bounded read projection, not a WorkItem or Review decision.
type AttentionItem struct {
	WorkItemID  uuid.UUID       `json:"work_item_id"`
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Status      domain.Status   `json:"status"`
	Priority    domain.Priority `json:"priority"`
	AssigneeID  *uuid.UUID      `json:"assignee_id"`
	Reason      string          `json:"reason"`
	SourceKind  string          `json:"source_kind"`
	SourceID    uuid.UUID       `json:"source_id"`
	SourceLabel string          `json:"source_label"`
	ObservedAt  time.Time       `json:"observed_at"`
	SourceAt    *time.Time      `json:"source_at,omitempty"`
	HintOnly    bool            `json:"hint_only"`
}

type AttentionFilter struct {
	Limit      int
	Offset     int
	Now        time.Time
	StaleAfter time.Duration
}

type AttentionPage struct {
	Items         []AttentionItem `json:"items"`
	HasMore       bool            `json:"has_more"`
	NextOffset    *int            `json:"next_offset,omitempty"`
	PagingLimited bool            `json:"paging_limited"`
}

type AttentionRepository interface {
	ListAttention(context.Context, uuid.UUID, AttentionFilter) (AttentionPage, error)
}

// A work:read grant alone permits bounded facts, never raw Audit, approval or
// private Artifact bytes. No mutation is performed by this endpoint.
func (s Service) AttentionInbox(ctx context.Context, actor identity.Actor, pid uuid.UUID, f AttentionFilter) (AttentionPage, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, idomain.WorkRead); err != nil {
		return AttentionPage{}, err
	}
	if s.Attention == nil {
		return AttentionPage{}, ErrNotFound
	}
	if f.Limit == 0 {
		f.Limit = 30
	}
	if f.Limit < 1 || f.Limit > 50 || f.Offset < 0 || f.Offset > 450 {
		return AttentionPage{}, domain.ErrValidation
	}
	if f.Now.IsZero() {
		f.Now = s.now().UTC()
	}
	if f.StaleAfter <= 0 {
		f.StaleAfter = s.AttentionStaleAfter
	}
	if f.StaleAfter <= 0 {
		f.StaleAfter = 5 * time.Minute
	}
	return s.Attention.ListAttention(ctx, pid, f)
}
