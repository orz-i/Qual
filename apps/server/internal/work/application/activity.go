package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

type ActivityRecord struct {
	ID         uuid.UUID `json:"id"`
	ActorID    uuid.UUID `json:"actor_id"`
	Operation  string    `json:"operation"`
	EntityID   uuid.UUID `json:"entity_id"`
	OccurredAt time.Time `json:"occurred_at"`
}
type AuditRecord struct {
	ID           uuid.UUID `json:"id"`
	ActorID      uuid.UUID `json:"actor_id"`
	CredentialID uuid.UUID `json:"credential_id"`
	RequestID    string    `json:"request_id"`
	Operation    string    `json:"operation"`
	EntityID     uuid.UUID `json:"entity_id"`
	OldVersion   *int64    `json:"old_version"`
	NewVersion   int64     `json:"new_version"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func activityLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, domain.ErrValidation
	}
	return limit, nil
}

func (s Service) Activity(ctx context.Context, actor identity.Actor, projectID uuid.UUID, limit int) ([]ActivityRecord, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return nil, err
	}
	limit, err := activityLimit(limit)
	if err != nil {
		return nil, err
	}
	return s.Work.Activity(ctx, projectID, limit)
}

func (s Service) Audit(ctx context.Context, actor identity.Actor, projectID uuid.UUID, limit int) ([]AuditRecord, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.AuditRead); err != nil {
		return nil, err
	}
	limit, err := activityLimit(limit)
	if err != nil {
		return nil, err
	}
	return s.Work.Audit(ctx, projectID, limit)
}
