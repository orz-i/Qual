// Package application defines work use cases and their persistence ports.
// Every transport (REST, future MCP, CLI) calls the same use-case service.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

var (
	ErrNotFound     = errors.New("work item not found")
	ErrIdempotency  = errors.New("idempotency key reused with different request")
	ErrPrecondition = errors.New("idempotency key and expected version required")
)

type Meta struct {
	RequestID      string
	IdempotencyKey string
}

type Filter struct {
	Status domain.Status
	Kind   domain.Kind
	Limit  int
	Cursor string
}

type Page struct {
	Items      []domain.WorkItem `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type Mutation struct {
	Operation         string
	Expected          int64
	Meta              Meta
	RequestHash       [32]byte
	LegacyDoneAttempt bool
	Apply             func(*domain.WorkItem) error
}

type Repository interface {
	Create(context.Context, identity.Actor, identity.Project, domain.Draft, Meta, [32]byte) (domain.WorkItem, error)
	Mutate(context.Context, identity.Actor, identity.Project, uuid.UUID, Mutation) (domain.WorkItem, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (domain.WorkItem, error)
	List(context.Context, uuid.UUID, Filter) (Page, error)
	AddComment(context.Context, identity.Actor, identity.Project, uuid.UUID, int64, string, Meta, [32]byte) (domain.WorkItem, error)
	LinkDependency(context.Context, identity.Actor, identity.Project, uuid.UUID, uuid.UUID, int64, Meta, [32]byte) (domain.WorkItem, error)
	Comments(context.Context, uuid.UUID, uuid.UUID) ([]domain.Comment, error)
	Dependencies(context.Context, uuid.UUID, uuid.UUID) ([]domain.Dependency, error)
	Activity(context.Context, uuid.UUID, int) ([]ActivityRecord, error)
	Audit(context.Context, uuid.UUID, int) ([]AuditRecord, error)
}

// CompletionRepository is deliberately separate from the legacy Repository
// contract so staged deployments can start on Goose12 without pretending the
// new review policy has been rolled out. On Goose13 it is the sole Done path.
type CompletionRepository interface {
	Complete(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.DoneDecision, Meta, [32]byte) (domain.WorkItem, error)
}

type Service struct {
	Identity          identity.Authenticator
	Work              Repository
	Roles             RolesRepository
	Completion        CompletionRepository
	ReviewPolicy      ProjectReviewPolicyRepository
	AcceptanceHistory AcceptanceHistoryRepository
	WorkReviewPolicy  WorkReviewPolicyRepository
	Clock             func() time.Time
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func validMeta(meta Meta) error {
	if meta.RequestID == "" || len(meta.IdempotencyKey) < 8 || len(meta.IdempotencyKey) > 128 {
		return ErrPrecondition
	}
	return nil
}

func hashCommand(op string, payload any) [32]byte {
	body, _ := json.Marshal(struct {
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{op, payload})
	return sha256.Sum256(body)
}

func (s Service) Create(ctx context.Context, actor identity.Actor, projectID uuid.UUID, draft domain.Draft, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if draft.Priority == "" {
		draft.Priority = domain.Medium
	}
	if err := domain.ValidateDraft(draft); err != nil {
		return domain.WorkItem{}, err
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Create(ctx, actor, project, draft, meta, hashCommand("create", draft))
}

func (s Service) Get(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) (domain.WorkItem, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Get(ctx, projectID, itemID)
}

func (s Service) List(ctx context.Context, actor identity.Actor, projectID uuid.UUID, filter Filter) (Page, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return Page{}, err
	}
	if filter.Limit == 0 {
		filter.Limit = 30
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		return Page{}, domain.ErrValidation
	}
	if filter.Status != "" && filter.Status != domain.Backlog && filter.Status != domain.Todo && filter.Status != domain.InProgress && filter.Status != domain.InReview && filter.Status != domain.Done && filter.Status != domain.Cancelled {
		return Page{}, domain.ErrValidation
	}
	if filter.Kind != "" && filter.Kind != domain.Task && filter.Kind != domain.Bug {
		return Page{}, domain.ErrValidation
	}
	return s.Work.List(ctx, projectID, filter)
}

func (s Service) Update(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, ch domain.Changes, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Operation: "update", Expected: expected, Meta: meta,
		RequestHash: hashCommand("update", struct {
			ID       uuid.UUID
			Expected int64
			Changes  domain.Changes
		}{itemID, expected, ch}),
		Apply: func(w *domain.WorkItem) error { return w.Update(ch, expected, s.now()) },
	})
}

func (s Service) Claim(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkClaim)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Operation: "claim", Expected: expected, Meta: meta,
		RequestHash: hashCommand("claim", struct {
			ID       uuid.UUID
			Expected int64
		}{itemID, expected}),
		Apply: func(w *domain.WorkItem) error { return w.Claim(actor.PrincipalID, expected, s.now()) },
	})
}

func (s Service) Transition(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, next domain.Status, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	permission := idomain.WorkWrite
	if next == domain.Done {
		permission = idomain.WorkReview
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, permission)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Operation: "transition", Expected: expected, Meta: meta,
		LegacyDoneAttempt: next == domain.Done,
		RequestHash: hashCommand("transition", struct {
			ID       uuid.UUID
			Next     domain.Status
			Expected int64
		}{itemID, next, expected}),
		Apply: func(w *domain.WorkItem) error {
			return w.Transition(next, expected, permission == idomain.WorkReview, s.now())
		},
	})
}
