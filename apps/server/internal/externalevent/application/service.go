// Package application authorizes untrusted external Event facts; no Agent orchestration.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"qual/internal/externalevent/domain"
	identity "qual/internal/identity/application"
	permissions "qual/internal/identity/domain"
	work "qual/internal/work/application"
)

var ErrPrecondition = errors.New("event Idempotency-Key required")

type Repository interface {
	Record(context.Context, identity.Actor, identity.Project, domain.Input, work.Meta, [32]byte, time.Time) (domain.Event, error)
	List(context.Context, uuid.UUID, uuid.UUID, bool, int, uuid.UUID) (domain.Page, error)
}
type Service struct {
	Identity identity.Authenticator
	Events   Repository
	Clock    func() time.Time
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s Service) Record(ctx context.Context, actor identity.Actor, pid uuid.UUID, in domain.Input, meta work.Meta) (domain.Event, error) {
	if len(meta.IdempotencyKey) < 8 || len(meta.IdempotencyKey) > 128 || meta.RequestID == "" {
		return domain.Event{}, ErrPrecondition
	}
	if err := in.Validate(s.now()); err != nil {
		return domain.Event{}, err
	}
	// PostgreSQL timestamptz stores microseconds, not arbitrary source
	// nanoseconds. Normalize BEFORE hashing, comparison and persistence so a
	// genuine same-event retry with RFC3339Nano is never a false 409.
	in.OccurredAt = in.OccurredAt.UTC().Truncate(time.Microsecond)
	project, err := s.Identity.AuthorizeProject(ctx, actor, pid, permissions.EventWrite)
	if err != nil {
		return domain.Event{}, err
	}
	raw, _ := json.Marshal(in)
	digest := sha256.Sum256(raw)
	return s.Events.Record(ctx, actor, project, in, meta, digest, s.now())
}
func (s Service) List(ctx context.Context, actor identity.Actor, pid, target uuid.UUID, byThread bool, limit int, cursor uuid.UUID) (domain.Page, error) {
	if target == uuid.Nil {
		return domain.Page{}, domain.ErrValidation
	}
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 {
		return domain.Page{}, domain.ErrValidation
	}
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, permissions.WorkRead); err != nil {
		return domain.Page{}, err
	}
	return s.Events.List(ctx, pid, target, byThread, limit, cursor)
}
