// Package application authorizes records and cross-context evidence operations.
// All adapters (REST/CLI/MCP/Web) must reuse these use cases.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
	role "qual/internal/identity/domain"
)

var (
	ErrPrecondition = errors.New("idempotency key required")
	ErrIdempotency  = errors.New("evidence idempotency conflict")
)

type Meta struct {
	RequestID      string
	IdempotencyKey string
}

func (m Meta) Validate() error {
	if m.RequestID == "" || len(m.IdempotencyKey) < 8 || len(m.IdempotencyKey) > 128 {
		return ErrPrecondition
	}
	return nil
}
func digest(op string, in any) [32]byte {
	b, _ := json.Marshal(struct {
		Op    string `json:"op"`
		Input any    `json:"input"`
	}{op, in})
	return sha256.Sum256(b)
}

type ObjectStore interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, int64) ([]byte, error)
	Delete(context.Context, string) error
}
type Repository interface {
	Begin(context.Context, identity.Actor, identity.Project, domain.BeginInput, Meta, [32]byte) (domain.Artifact, error)
	MarkUploaded(context.Context, identity.Actor, identity.Project, uuid.UUID, Meta, [32]byte, []byte) (domain.Artifact, error)
	MarkReady(context.Context, identity.Actor, identity.Project, uuid.UUID, Meta, [32]byte, []byte) (domain.Artifact, error)
	GetArtifact(context.Context, uuid.UUID, uuid.UUID) (domain.Artifact, string, error)
	ListArtifacts(context.Context, uuid.UUID, domain.TargetKind, uuid.UUID) ([]domain.Artifact, error)
	AddReference(context.Context, identity.Actor, identity.Project, domain.ReferenceInput, Meta, [32]byte) (domain.Reference, error)
	ListReferences(context.Context, uuid.UUID, domain.TargetKind, uuid.UUID) ([]domain.Reference, error)
	CreateExport(context.Context, identity.Actor, identity.Project, Meta, [32]byte, ObjectStore) (domain.Export, error)
	GetExport(context.Context, uuid.UUID, uuid.UUID) (domain.Export, string, error)
	ListExports(context.Context, uuid.UUID) ([]domain.Export, error)
	CleanupStale(context.Context, ObjectStore, time.Duration) (int, error)
}
type Service struct {
	Identity identity.Authenticator
	Records  Repository
	Threads  ThreadRepository
	Objects  ObjectStore
}

func (s Service) authorize(ctx context.Context, a identity.Actor, pid uuid.UUID, p role.Permission) (identity.Project, error) {
	return s.Identity.AuthorizeProject(ctx, a, pid, p)
}
func (s Service) Begin(ctx context.Context, a identity.Actor, pid uuid.UUID, in domain.BeginInput, m Meta) (domain.Artifact, error) {
	if err := m.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	in.SHA256 = strings.ToLower(in.SHA256)
	if a.Type == role.Integration && in.TargetKind == domain.WorkItem {
		return domain.Artifact{}, identity.ErrForbidden
	}
	if s.Objects == nil {
		return domain.Artifact{}, domain.ErrUnavailable
	}
	p, err := s.authorize(ctx, a, pid, role.ArtifactWrite)
	if err != nil {
		return domain.Artifact{}, err
	}
	return s.Records.Begin(ctx, a, p, in, m, digest("artifact.begin", in))
}
func (s Service) Upload(ctx context.Context, a identity.Actor, pid, id uuid.UUID, data []byte, m Meta) (domain.Artifact, error) {
	if err := m.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	if len(data) < 1 || len(data) > domain.MaxArtifactSize {
		return domain.Artifact{}, domain.ErrValidation
	}
	if s.Objects == nil {
		return domain.Artifact{}, domain.ErrUnavailable
	}
	p, err := s.authorize(ctx, a, pid, role.ArtifactWrite)
	if err != nil {
		return domain.Artifact{}, err
	}
	h := sha256.Sum256(data)
	return s.Records.MarkUploaded(ctx, a, p, id, m, digest("artifact.upload", struct {
		ID   uuid.UUID
		Hash [32]byte
	}{id, h}), data)
}
func (s Service) Complete(ctx context.Context, a identity.Actor, pid, id uuid.UUID, m Meta) (domain.Artifact, error) {
	if err := m.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	if s.Objects == nil {
		return domain.Artifact{}, domain.ErrUnavailable
	}
	p, err := s.authorize(ctx, a, pid, role.ArtifactWrite)
	if err != nil {
		return domain.Artifact{}, err
	}
	return s.Records.MarkReady(ctx, a, p, id, m, digest("artifact.complete", id), nil)
}
func (s Service) Get(ctx context.Context, a identity.Actor, pid, id uuid.UUID) (domain.Artifact, error) {
	if _, err := s.authorize(ctx, a, pid, role.ArtifactRead); err != nil {
		return domain.Artifact{}, err
	}
	item, _, err := s.Records.GetArtifact(ctx, pid, id)
	return item, err
}
func (s Service) List(ctx context.Context, a identity.Actor, pid uuid.UUID, kind domain.TargetKind, target uuid.UUID) ([]domain.Artifact, error) {
	if _, err := s.authorize(ctx, a, pid, role.ArtifactRead); err != nil {
		return nil, err
	}
	if !domain.ValidTarget(kind) || target == uuid.Nil {
		return nil, domain.ErrValidation
	}
	return s.Records.ListArtifacts(ctx, pid, kind, target)
}
func (s Service) Download(ctx context.Context, a identity.Actor, pid, id uuid.UUID) (domain.Artifact, []byte, error) {
	if _, err := s.authorize(ctx, a, pid, role.ArtifactRead); err != nil {
		return domain.Artifact{}, nil, err
	}
	if s.Objects == nil {
		return domain.Artifact{}, nil, domain.ErrUnavailable
	}
	item, key, err := s.Records.GetArtifact(ctx, pid, id)
	if err != nil {
		return item, nil, err
	}
	if item.State != "ready" {
		return item, nil, domain.ErrConflict
	}
	data, err := s.Objects.Get(ctx, key, domain.MaxArtifactSize)
	if err != nil {
		return item, nil, domain.ErrUnavailable
	}
	if err := domain.Verify(data, item.SizeBytes, item.SHA256); err != nil {
		return item, nil, err
	}
	return item, data, nil
}
func (s Service) AddReference(ctx context.Context, a identity.Actor, pid uuid.UUID, in domain.ReferenceInput, m Meta) (domain.Reference, error) {
	if err := m.Validate(); err != nil {
		return domain.Reference{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.Reference{}, err
	}
	// External links are work/provenance facts; a separate API does not fetch the URL.
	var perm role.Permission
	if in.TargetKind == domain.WorkItem {
		perm = role.WorkWrite
	} else {
		perm = role.TestWrite
	}
	p, err := s.authorize(ctx, a, pid, perm)
	if err != nil {
		return domain.Reference{}, err
	}
	return s.Records.AddReference(ctx, a, p, in, m, digest("external_reference.create", in))
}
func (s Service) References(ctx context.Context, a identity.Actor, pid uuid.UUID, kind domain.TargetKind, target uuid.UUID) ([]domain.Reference, error) {
	if !domain.ValidTarget(kind) || target == uuid.Nil {
		return nil, domain.ErrValidation
	}
	var perm role.Permission
	if kind == domain.WorkItem {
		perm = role.WorkRead
	} else {
		perm = role.TestRead
	}
	if _, err := s.authorize(ctx, a, pid, perm); err != nil {
		return nil, err
	}
	return s.Records.ListReferences(ctx, pid, kind, target)
}
func (s Service) Export(ctx context.Context, a identity.Actor, pid uuid.UUID, m Meta) (domain.Export, error) {
	if err := m.Validate(); err != nil {
		return domain.Export{}, err
	}
	if s.Objects == nil {
		return domain.Export{}, domain.ErrUnavailable
	}
	p, err := s.authorize(ctx, a, pid, role.ArchiveExport)
	if err != nil {
		return domain.Export{}, err
	}
	return s.Records.CreateExport(ctx, a, p, m, digest("archive.export", pid), s.Objects)
}
func (s Service) Exports(ctx context.Context, a identity.Actor, pid uuid.UUID) ([]domain.Export, error) {
	if _, err := s.authorize(ctx, a, pid, role.ArchiveExport); err != nil {
		return nil, err
	}
	return s.Records.ListExports(ctx, pid)
}
func (s Service) ExportDownload(ctx context.Context, a identity.Actor, pid, id uuid.UUID) (domain.Export, []byte, error) {
	if _, err := s.authorize(ctx, a, pid, role.ArchiveExport); err != nil {
		return domain.Export{}, nil, err
	}
	if s.Objects == nil {
		return domain.Export{}, nil, domain.ErrUnavailable
	}
	exp, key, err := s.Records.GetExport(ctx, pid, id)
	if err != nil {
		return exp, nil, err
	}
	data, err := s.Objects.Get(ctx, key, 32*1024*1024)
	if err != nil {
		return exp, nil, domain.ErrUnavailable
	}
	if err := domain.Verify(data, exp.SizeBytes, exp.SHA256); err != nil {
		return exp, nil, err
	}
	return exp, data, nil
}
