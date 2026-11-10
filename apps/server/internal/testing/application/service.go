// Package application exposes authorized testing use cases shared by REST,
// MCP, CLI and Web. All mutations are facts; none execute tests or Agents.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/testing/domain"
	workdomain "qual/internal/work/domain"
)

var (
	ErrIdempotency  = errors.New("testing idempotency key conflict")
	ErrPrecondition = errors.New("idempotency key and expected revision required")
	ErrScope        = errors.New("testing scope denied")
)

type Meta struct {
	RequestID      string
	IdempotencyKey string
}
type CaseInput struct {
	Specification domain.Specification `json:"specification"`
	SuiteID       *uuid.UUID           `json:"suite_id,omitempty"`
}
type EditCaseInput struct {
	Specification    domain.Specification `json:"specification"`
	SuiteID          *uuid.UUID           `json:"suite_id,omitempty"`
	ExpectedRevision int64                `json:"expected_revision"`
}
type PublishInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}
type AttemptRecord struct {
	ItemID uuid.UUID
	Input  domain.AttemptInput
}
type LinkCoverageInput struct {
	WorkItemID uuid.UUID `json:"work_item_id"`
}
type JUnitInput struct {
	Filename string
	Raw      []byte
}
type Repository interface {
	CoverageRepository
	CreateSuite(context.Context, identity.Actor, identity.Project, string, Meta, [32]byte) (domain.Suite, error)
	ListSuites(context.Context, uuid.UUID) ([]domain.Suite, error)
	CreateCase(context.Context, identity.Actor, identity.Project, CaseInput, Meta, [32]byte) (domain.TestCase, error)
	UpdateCase(context.Context, identity.Actor, identity.Project, uuid.UUID, EditCaseInput, Meta, [32]byte) (domain.TestCase, error)
	PublishCase(context.Context, identity.Actor, identity.Project, uuid.UUID, PublishInput, Meta, [32]byte) (domain.TestCaseVersion, error)
	GetCase(context.Context, uuid.UUID, uuid.UUID) (domain.TestCase, error)
	ListCases(context.Context, uuid.UUID) ([]domain.TestCase, error)
	CaseVersions(context.Context, uuid.UUID, uuid.UUID) ([]domain.TestCaseVersion, error)
	LinkCoverage(context.Context, identity.Actor, identity.Project, uuid.UUID, uuid.UUID, Meta, [32]byte) (domain.TestCase, error)
	CreateRun(context.Context, identity.Actor, identity.Project, domain.RunInput, Meta, [32]byte) (domain.TestRun, error)
	GetRun(context.Context, uuid.UUID, uuid.UUID) (domain.TestRun, error)
	ListRuns(context.Context, uuid.UUID) ([]domain.TestRun, error)
	RecordAttempt(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.AttemptInput, Meta, [32]byte) (domain.Attempt, error)
	Attempts(context.Context, uuid.UUID, uuid.UUID) ([]domain.Attempt, error)
	AttemptDefects(context.Context, uuid.UUID, uuid.UUID) ([]domain.AttemptDefect, error)
	LinkDefect(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.BugLinkInput, Meta, [32]byte) (domain.BugLink, error)
	ImportJUnit(context.Context, identity.Actor, identity.Project, uuid.UUID, JUnitInput, []domain.ReportCase, Meta, [32]byte) (domain.ImportSummary, error)
	TaskMode(context.Context, uuid.UUID) (workdomain.TaskAuthorizationMode, error)
	RunWork(context.Context, uuid.UUID, uuid.UUID) (*uuid.UUID, error)
	RunItemWork(context.Context, uuid.UUID, uuid.UUID) (*uuid.UUID, error)
	AttemptWork(context.Context, uuid.UUID, uuid.UUID) (*uuid.UUID, error)
	AuthorizeTaskTestRead(context.Context, identity.Actor, identity.Project, *uuid.UUID) error
}

type Service struct {
	Identity identity.Authenticator
	Tests    Repository
}

func validMeta(m Meta) error {
	if m.RequestID == "" || len(m.IdempotencyKey) < 8 || len(m.IdempotencyKey) > 128 {
		return ErrPrecondition
	}
	return nil
}
func hashCommand(op string, v any) [32]byte {
	b, _ := json.Marshal(struct {
		Op   string `json:"op"`
		Data any    `json:"data"`
	}{op, v})
	return sha256.Sum256(b)
}
func (s Service) project(ctx context.Context, actor identity.Actor, projectID uuid.UUID, p idomain.Permission) (identity.Project, error) {
	return s.Identity.AuthorizeProject(ctx, actor, projectID, p)
}

// taskProject is a coarse preflight: the final privilege/relationship gate
// runs in PostgreSQL under the same transaction as the immutable fact.
func (s Service) taskProject(ctx context.Context, actor identity.Actor, pid uuid.UUID,
	workID *uuid.UUID, permission idomain.Permission) (identity.Project, error) {
	mode, err := s.Tests.TaskMode(ctx, pid)
	if err != nil {
		return identity.Project{}, err
	}
	if mode == workdomain.TaskScopedAuthorization && actor.Type != idomain.Integration {
		if workID != nil {
			p, err := s.project(ctx, actor, pid, idomain.WorkRead)
			if err != nil {
				return identity.Project{}, err
			}
			if _, err := s.project(ctx, actor, pid, idomain.WorkClaim); err != nil {
				return identity.Project{}, err
			}
			return p, nil
		}
		if _, err := s.project(ctx, actor, pid, idomain.AccessManage); err != nil {
			return identity.Project{}, err
		}
	}
	return s.project(ctx, actor, pid, permission)
}

func (s Service) taskRead(ctx context.Context, actor identity.Actor, pid uuid.UUID,
	workID *uuid.UUID) (identity.Project, error) {
	mode, err := s.Tests.TaskMode(ctx, pid)
	if err != nil {
		return identity.Project{}, err
	}
	if mode == workdomain.TaskScopedAuthorization && actor.Type != idomain.Integration && workID != nil {
		p, err := s.project(ctx, actor, pid, idomain.WorkRead)
		if err != nil {
			return identity.Project{}, err
		}
		if _, err := s.project(ctx, actor, pid, idomain.WorkClaim); err != nil {
			return identity.Project{}, err
		}
		return p, nil
	}
	return s.taskProject(ctx, actor, pid, nil, idomain.TestRead)
}

func canManageCases(actor identity.Actor) error {
	if actor.Type == idomain.Integration {
		return identity.ErrForbidden
	}
	return nil
}
func (s Service) CreateSuite(ctx context.Context, actor identity.Actor, pid uuid.UUID, name string, m Meta) (domain.Suite, error) {
	if err := validMeta(m); err != nil {
		return domain.Suite{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.Suite{}, err
	}
	p, err := s.project(ctx, actor, pid, idomain.TestWrite)
	if err != nil {
		return domain.Suite{}, err
	}
	return s.Tests.CreateSuite(ctx, actor, p, name, m, hashCommand("suite.create", name))
}
func (s Service) ListSuites(ctx context.Context, actor identity.Actor, pid uuid.UUID) ([]domain.Suite, error) {
	if _, err := s.project(ctx, actor, pid, idomain.TestRead); err != nil {
		return nil, err
	}
	return s.Tests.ListSuites(ctx, pid)
}
func (s Service) CreateCase(ctx context.Context, actor identity.Actor, pid uuid.UUID, in CaseInput, m Meta) (domain.TestCase, error) {
	if err := validMeta(m); err != nil {
		return domain.TestCase{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.TestCase{}, err
	}
	if err := in.Specification.Validate(); err != nil {
		return domain.TestCase{}, err
	}
	in.Specification = in.Specification.Normalize()
	p, err := s.project(ctx, actor, pid, idomain.TestWrite)
	if err != nil {
		return domain.TestCase{}, err
	}
	return s.Tests.CreateCase(ctx, actor, p, in, m, hashCommand("case.create", in))
}
func (s Service) UpdateCase(ctx context.Context, actor identity.Actor, pid, cid uuid.UUID, in EditCaseInput, m Meta) (domain.TestCase, error) {
	if err := validMeta(m); err != nil {
		return domain.TestCase{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.TestCase{}, err
	}
	if in.ExpectedRevision < 1 {
		return domain.TestCase{}, ErrPrecondition
	}
	if err := in.Specification.Validate(); err != nil {
		return domain.TestCase{}, err
	}
	in.Specification = in.Specification.Normalize()
	p, err := s.project(ctx, actor, pid, idomain.TestWrite)
	if err != nil {
		return domain.TestCase{}, err
	}
	return s.Tests.UpdateCase(ctx, actor, p, cid, in, m, hashCommand("case.update", struct {
		ID   uuid.UUID
		Body EditCaseInput
	}{cid, in}))
}
func (s Service) PublishCase(ctx context.Context, actor identity.Actor, pid, cid uuid.UUID, in PublishInput, m Meta) (domain.TestCaseVersion, error) {
	if err := validMeta(m); err != nil {
		return domain.TestCaseVersion{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.TestCaseVersion{}, err
	}
	if in.ExpectedRevision < 1 {
		return domain.TestCaseVersion{}, ErrPrecondition
	}
	p, err := s.project(ctx, actor, pid, idomain.TestWrite)
	if err != nil {
		return domain.TestCaseVersion{}, err
	}
	return s.Tests.PublishCase(ctx, actor, p, cid, in, m, hashCommand("case.publish", struct {
		ID   uuid.UUID
		Body PublishInput
	}{cid, in}))
}
func (s Service) GetCase(ctx context.Context, actor identity.Actor, pid, cid uuid.UUID) (domain.TestCase, error) {
	if _, err := s.project(ctx, actor, pid, idomain.TestRead); err != nil {
		return domain.TestCase{}, err
	}
	return s.Tests.GetCase(ctx, pid, cid)
}
func (s Service) ListCases(ctx context.Context, actor identity.Actor, pid uuid.UUID) ([]domain.TestCase, error) {
	if _, err := s.project(ctx, actor, pid, idomain.TestRead); err != nil {
		return nil, err
	}
	return s.Tests.ListCases(ctx, pid)
}
func (s Service) CaseVersions(ctx context.Context, actor identity.Actor, pid, cid uuid.UUID) ([]domain.TestCaseVersion, error) {
	if _, err := s.project(ctx, actor, pid, idomain.TestRead); err != nil {
		return nil, err
	}
	return s.Tests.CaseVersions(ctx, pid, cid)
}
func (s Service) LinkCoverage(ctx context.Context, actor identity.Actor, pid, cid, wid uuid.UUID, m Meta) (domain.TestCase, error) {
	if err := validMeta(m); err != nil {
		return domain.TestCase{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.TestCase{}, err
	}
	p, err := s.taskProject(ctx, actor, pid, &wid, idomain.TestWrite)
	if err != nil {
		return domain.TestCase{}, err
	}
	if _, err := s.project(ctx, actor, pid, idomain.WorkRead); err != nil {
		return domain.TestCase{}, err
	}
	return s.Tests.LinkCoverage(ctx, actor, p, cid, wid, m, hashCommand("case.coverage", struct{ Case, Work uuid.UUID }{cid, wid}))
}
func (s Service) CreateRun(ctx context.Context, actor identity.Actor, pid uuid.UUID, in domain.RunInput, m Meta) (domain.TestRun, error) {
	if err := validMeta(m); err != nil {
		return domain.TestRun{}, err
	}
	if in.SourceSystem == "" {
		in.SourceSystem = "manual"
	}
	if err := in.Validate(); err != nil {
		return domain.TestRun{}, err
	}
	p, err := s.taskProject(ctx, actor, pid, in.WorkItemID, idomain.TestWrite)
	if err != nil {
		return domain.TestRun{}, err
	}
	return s.Tests.CreateRun(ctx, actor, p, in, m, hashCommand("run.create", in))
}
func (s Service) GetRun(ctx context.Context, actor identity.Actor, pid, rid uuid.UUID) (domain.TestRun, error) {
	workID, err := s.Tests.RunWork(ctx, pid, rid)
	if err != nil {
		return domain.TestRun{}, err
	}
	project, err := s.taskRead(ctx, actor, pid, workID)
	if err != nil {
		return domain.TestRun{}, err
	}
	if err := s.Tests.AuthorizeTaskTestRead(ctx, actor, project, workID); err != nil {
		return domain.TestRun{}, err
	}
	return s.Tests.GetRun(ctx, pid, rid)
}
func (s Service) ListRuns(ctx context.Context, actor identity.Actor, pid uuid.UUID) ([]domain.TestRun, error) {
	// Global listing in scoped mode requires real project management. To
	// inspect task-bound results agents use WorkItem context and GetRun.
	p, err := s.taskRead(ctx, actor, pid, nil)
	if err != nil {
		return nil, err
	}
	if err := s.Tests.AuthorizeTaskTestRead(ctx, actor, p, nil); err != nil {
		return nil, err
	}
	return s.Tests.ListRuns(ctx, pid)
}
func (s Service) RecordAttempt(ctx context.Context, actor identity.Actor, pid, itemID uuid.UUID, in domain.AttemptInput, m Meta) (domain.Attempt, error) {
	if err := validMeta(m); err != nil {
		return domain.Attempt{}, err
	}
	if in.SourceKind == "" {
		in.SourceKind = "manual"
	}
	if err := in.Validate(); err != nil {
		return domain.Attempt{}, err
	}
	workID, err := s.Tests.RunItemWork(ctx, pid, itemID)
	if err != nil {
		return domain.Attempt{}, err
	}
	p, err := s.taskProject(ctx, actor, pid, workID, idomain.TestWrite)
	if err != nil {
		return domain.Attempt{}, err
	}
	return s.Tests.RecordAttempt(ctx, actor, p, itemID, in, m, hashCommand("attempt.record", struct {
		ID    uuid.UUID
		Input domain.AttemptInput
	}{itemID, in}))
}
func (s Service) Attempts(ctx context.Context, actor identity.Actor, pid, itemID uuid.UUID) ([]domain.Attempt, error) {
	workID, err := s.Tests.RunItemWork(ctx, pid, itemID)
	if err != nil {
		return nil, err
	}
	p, err := s.taskRead(ctx, actor, pid, workID)
	if err != nil {
		return nil, err
	}
	if err := s.Tests.AuthorizeTaskTestRead(ctx, actor, p, workID); err != nil {
		return nil, err
	}
	return s.Tests.Attempts(ctx, pid, itemID)
}

// AttemptDefects is scoped to TestRead and reads only already linked failures.
func (s Service) AttemptDefects(ctx context.Context, actor identity.Actor, pid, attemptID uuid.UUID) ([]domain.AttemptDefect, error) {
	workID, err := s.Tests.AttemptWork(ctx, pid, attemptID)
	if err != nil {
		return nil, err
	}
	p, err := s.taskRead(ctx, actor, pid, workID)
	if err != nil {
		return nil, err
	}
	if err := s.Tests.AuthorizeTaskTestRead(ctx, actor, p, workID); err != nil {
		return nil, err
	}
	return s.Tests.AttemptDefects(ctx, pid, attemptID)
}

func (s Service) LinkDefect(ctx context.Context, actor identity.Actor, pid, attemptID uuid.UUID, in domain.BugLinkInput, m Meta) (domain.BugLink, error) {
	if err := validMeta(m); err != nil {
		return domain.BugLink{}, err
	}
	if err := canManageCases(actor); err != nil {
		return domain.BugLink{}, err
	}
	if in.BugID == nil && in.Title == "" {
		return domain.BugLink{}, domain.ErrValidation
	}
	if in.BugID != nil && in.Title != "" {
		return domain.BugLink{}, domain.ErrValidation
	}
	if _, err := s.project(ctx, actor, pid, idomain.WorkWrite); err != nil {
		return domain.BugLink{}, err
	}
	p, err := s.project(ctx, actor, pid, idomain.TestRead)
	if err != nil {
		return domain.BugLink{}, err
	}
	return s.Tests.LinkDefect(ctx, actor, p, attemptID, in, m, hashCommand("attempt.defect", struct {
		ID    uuid.UUID
		Input domain.BugLinkInput
	}{attemptID, in}))
}
func (s Service) ImportJUnit(ctx context.Context, actor identity.Actor, pid, rid uuid.UUID, in JUnitInput, m Meta) (domain.ImportSummary, error) {
	if err := validMeta(m); err != nil {
		return domain.ImportSummary{}, err
	}
	if len(in.Filename) < 1 || len(in.Filename) > 160 || len(in.Raw) > domain.MaxJUnitXMLBytes {
		return domain.ImportSummary{}, domain.ErrValidation
	}
	cases, err := domain.ParseJUnit(in.Raw)
	if err != nil {
		return domain.ImportSummary{}, err
	}
	workID, err := s.Tests.RunWork(ctx, pid, rid)
	if err != nil {
		return domain.ImportSummary{}, err
	}
	p, err := s.taskProject(ctx, actor, pid, workID, idomain.TestWrite)
	if err != nil {
		return domain.ImportSummary{}, err
	}
	return s.Tests.ImportJUnit(ctx, actor, p, rid, in, cases, m, hashCommand("junit.import", struct {
		ID   uuid.UUID
		Name string
		Hash [32]byte
	}{rid, in.Filename, sha256.Sum256(in.Raw)}))
}
