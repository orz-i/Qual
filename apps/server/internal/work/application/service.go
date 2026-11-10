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
	Action            domain.TaskAction
	NextStatus        domain.Status
	NewAssigneeID     uuid.UUID
	Expected          int64
	Meta              Meta
	RequestHash       [32]byte
	LegacyDoneAttempt bool
	Apply             func(*domain.WorkItem) error
}

type TaskModeRepository interface {
	GetTaskMode(context.Context, uuid.UUID) (domain.TaskAuthorizationMode, error)
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
	Identity            identity.Authenticator
	Work                Repository
	TaskPolicy          TaskModeRepository
	Roles               RolesRepository
	Attention           AttentionRepository
	AttentionStaleAfter time.Duration
	Completion          CompletionRepository
	ReviewPolicy        ProjectReviewPolicyRepository
	AcceptanceHistory   AcceptanceHistoryRepository
	WorkReviewPolicy    WorkReviewPolicyRepository
	ReviewAssignments   ReviewAssignmentRepository
	Clock               func() time.Time
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

// authorizeTaskProject keeps the existing coarse project gate in legacy mode.
// Task-scoped writes use only the project participation credentials here; the
// authoritative action/relationship check happens under the WorkItem row lock.
func (s Service) authorizeTaskProject(ctx context.Context, actor identity.Actor, projectID uuid.UUID, legacy idomain.Permission) (identity.Project, error) {
	mode := domain.LegacyAuthorization
	if s.TaskPolicy != nil {
		var err error
		mode, err = s.TaskPolicy.GetTaskMode(ctx, projectID)
		if err != nil {
			return identity.Project{}, err
		}
	}
	if mode == domain.TaskScopedAuthorization {
		project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead)
		if err != nil {
			return identity.Project{}, err
		}
		if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkClaim); err != nil {
			return identity.Project{}, err
		}
		return project, nil
	}
	if mode != domain.LegacyAuthorization {
		return identity.Project{}, identity.ErrForbidden
	}
	return s.Identity.AuthorizeProject(ctx, actor, projectID, legacy)
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
	// Project-level WorkItem creation is explicitly authorized, not inferred
	// from task ownership. In scoped mode a real member may create with the
	// existing work:write grant (the repository also checks work:read/claim),
	// while access:manage remains the independently authorized admin path.
	mode := domain.LegacyAuthorization
	if s.TaskPolicy != nil {
		var err error
		mode, err = s.TaskPolicy.GetTaskMode(ctx, projectID)
		if err != nil {
			return domain.WorkItem{}, err
		}
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil && mode == domain.TaskScopedAuthorization {
		project, err = s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.AccessManage)
	}
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
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Action: domain.TaskUpdate, Operation: "update", Expected: expected, Meta: meta,
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
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkClaim)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Action: domain.TaskClaim, Operation: "claim", Expected: expected, Meta: meta,
		RequestHash: hashCommand("claim", struct {
			ID       uuid.UUID
			Expected int64
		}{itemID, expected}),
		Apply: func(w *domain.WorkItem) error { return w.Claim(actor.PrincipalID, expected, s.now()) },
	})
}

// transferProject uses the usual task participation check, with an explicit
// access:manage fallback for stalled-task recovery. PostgreSQL rechecks both
// authorizations under the WorkItem lock; a successful precheck is not a grant.
func (s Service) transferProject(ctx context.Context, actor identity.Actor, projectID uuid.UUID) (identity.Project, error) {
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkClaim)
	if err == nil {
		return project, nil
	}
	if !errors.Is(err, identity.ErrForbidden) {
		return identity.Project{}, err
	}
	return s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.AccessManage)
}

func (s Service) Release(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	project, err := s.transferProject(ctx, actor, projectID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Action: domain.TaskRelease, Operation: "release", Expected: expected, Meta: meta,
		RequestHash: hashCommand("release", struct {
			ID       uuid.UUID
			Expected int64
		}{itemID, expected}),
		Apply: func(w *domain.WorkItem) error { return w.Release(expected, s.now()) },
	})
}

func (s Service) Reassign(ctx context.Context, actor identity.Actor, projectID, itemID, newAssigneeID uuid.UUID, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	if newAssigneeID == uuid.Nil {
		return domain.WorkItem{}, domain.ErrValidation
	}
	project, err := s.transferProject(ctx, actor, projectID)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Action: domain.TaskReassign, Operation: "reassign", Expected: expected, Meta: meta,
		NewAssigneeID: newAssigneeID,
		RequestHash: hashCommand("reassign", struct {
			ID, NewAssigneeID uuid.UUID
			Expected          int64
		}{itemID, newAssigneeID, expected}),
		Apply: func(w *domain.WorkItem) error { return w.Reassign(newAssigneeID, expected, s.now()) },
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
	project, err := s.authorizeTaskProject(ctx, actor, projectID, permission)
	if err != nil {
		return domain.WorkItem{}, err
	}
	return s.Work.Mutate(ctx, actor, project, itemID, Mutation{
		Operation: "transition", Expected: expected, Meta: meta,
		LegacyDoneAttempt: next == domain.Done, NextStatus: next,
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
