// Package application authenticates and validates external AgentRun reports.
// Qual records facts but never launches, restarts or orchestrates Agent hosts.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"qual/internal/agentrun/domain"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	workapp "qual/internal/work/application"
)

var (
	ErrPrecondition = errors.New("idempotency key and current version required")
	ErrIdempotency  = errors.New("AgentRun idempotency conflict")
)

type Repository interface {
	Register(context.Context, identity.Actor, identity.Project, domain.Registration, workapp.Meta, [32]byte, time.Time) (domain.Run, error)
	Report(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.ReportInput, int64, workapp.Meta, [32]byte, time.Time, time.Duration) (domain.Receipt, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (domain.Run, error)
	ListForWorkItem(context.Context, uuid.UUID, uuid.UUID, int, int) ([]domain.Run, error)
	ListReports(context.Context, uuid.UUID, uuid.UUID, int64, int) ([]domain.Report, error)
}

type Service struct {
	Identity                identity.Authenticator
	Runs                    Repository
	Clock                   func() time.Time
	StaleAfter              time.Duration
	HeartbeatSampleInterval time.Duration
}

func (s Service) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}
func (s Service) staleAfter() time.Duration {
	if s.StaleAfter > 0 {
		return s.StaleAfter
	}
	return 5 * time.Minute
}

// At most one heartbeat is persisted per accepted interval. Sampling does
// not drop or rewrite substantive status/error/evidence reports.
func (s Service) sampleInterval() time.Duration {
	if s.HeartbeatSampleInterval >= time.Minute && s.HeartbeatSampleInterval <= time.Hour {
		return s.HeartbeatSampleInterval
	}
	return time.Minute
}
func validateMeta(meta workapp.Meta) error {
	if meta.RequestID == "" || len(meta.IdempotencyKey) < 8 || len(meta.IdempotencyKey) > 128 {
		return ErrPrecondition
	}
	return nil
}
func digest(op string, data any) [32]byte {
	raw, _ := json.Marshal(struct {
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{op, data})
	return sha256.Sum256(raw)
}
func (s Service) project(ctx context.Context, actor identity.Actor, pid uuid.UUID, perm idomain.Permission) (identity.Project, error) {
	return s.Identity.AuthorizeProject(ctx, actor, pid, perm)
}
func (s Service) Register(ctx context.Context, actor identity.Actor, pid uuid.UUID, in domain.Registration, meta workapp.Meta) (domain.Run, error) {
	if err := validateMeta(meta); err != nil {
		return domain.Run{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.Run{}, err
	}
	p, err := s.project(ctx, actor, pid, idomain.RunReport)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := s.Runs.Register(ctx, actor, p, in, meta, digest("agent_run.register", in), s.now())
	if err != nil {
		return run, err
	}
	return s.observe(run), nil
}
func (s Service) Report(ctx context.Context, actor identity.Actor, pid, runID uuid.UUID, in domain.ReportInput, expected int64, meta workapp.Meta) (domain.Receipt, error) {
	if err := validateMeta(meta); err != nil {
		return domain.Receipt{}, err
	}
	if expected < 1 {
		return domain.Receipt{}, ErrPrecondition
	}
	now := s.now()
	if err := in.Validate(now); err != nil {
		return domain.Receipt{}, err
	}
	p, err := s.project(ctx, actor, pid, idomain.RunReport)
	if err != nil {
		return domain.Receipt{}, err
	}
	// A telemetry-only credential must not probe or attach private artifacts.
	// Both Artifact and ExternalReference identity belong to Evidence ACLs.
	if in.EvidenceArtifactID != nil || in.ExternalReferenceID != nil {
		if _, err := s.project(ctx, actor, pid, idomain.ArtifactRead); err != nil {
			return domain.Receipt{}, err
		}
	}
	payload := struct {
		RunID    uuid.UUID
		Expected int64
		Report   domain.ReportInput
	}{runID, expected, in}
	result, err := s.Runs.Report(ctx, actor, p, runID, in, expected, meta, digest("agent_run.report", payload), now, s.sampleInterval())
	if err != nil {
		return result, err
	}
	result.Run = s.observe(result.Run)
	return result, nil
}
func (s Service) observe(run domain.Run) domain.Run {
	run.Observability = domain.Observe(run.Status, run.LastSeenAt, s.now(), s.staleAfter())
	return run
}
func (s Service) Get(ctx context.Context, actor identity.Actor, pid, runID uuid.UUID) (domain.Run, error) {
	if _, err := s.project(ctx, actor, pid, idomain.WorkRead); err != nil {
		return domain.Run{}, err
	}
	run, err := s.Runs.Get(ctx, pid, runID)
	return s.observe(run), err
}
func (s Service) ListForWorkItem(ctx context.Context, actor identity.Actor, pid, itemID uuid.UUID, limit, offset int) ([]domain.Run, error) {
	if _, err := s.project(ctx, actor, pid, idomain.WorkRead); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
		return nil, domain.ErrValidation
	}
	rows, err := s.Runs.ListForWorkItem(ctx, pid, itemID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i] = s.observe(rows[i])
	}
	return rows, nil
}
func (s Service) ListReports(ctx context.Context, actor identity.Actor, pid, runID uuid.UUID, before int64, limit int) ([]domain.Report, error) {
	if _, err := s.project(ctx, actor, pid, idomain.WorkRead); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 30
	}
	if limit < 1 || limit > 100 || before < 0 {
		return nil, domain.ErrValidation
	}
	reports, err := s.Runs.ListReports(ctx, pid, runID, before, limit)
	if err != nil {
		return nil, err
	}
	// Evidence identities require their separate authorization. Denied scopes
	// must not leak artifact/reference existence through this list.
	_, err = s.project(ctx, actor, pid, idomain.ArtifactRead)
	if err != nil {
		if !errors.Is(err, identity.ErrForbidden) {
			return nil, err
		}
		for i := range reports {
			reports[i].EvidenceArtifactID = nil
			reports[i].ExternalReferenceID = nil
		}
	}
	return reports, nil
}
