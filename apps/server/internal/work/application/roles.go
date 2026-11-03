package application

import (
	"context"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

type RolesRepository interface {
	RecordRole(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.RoleEntryInput, Meta, [32]byte) (domain.RoleEntry, error)
	ListRoles(context.Context, uuid.UUID, uuid.UUID) ([]domain.RoleEntry, error)
	ListReviewQueue(context.Context, uuid.UUID, ReviewQueueFilter) (ReviewQueuePage, error)
}

func (s Service) RecordRole(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, in domain.RoleEntryInput, meta Meta) (domain.RoleEntry, error) {
	if err := validMeta(meta); err != nil {
		return domain.RoleEntry{}, err
	}
	if err := in.Validate(); err != nil {
		return domain.RoleEntry{}, err
	}
	if s.Roles == nil {
		return domain.RoleEntry{}, ErrNotFound
	}
	permission := idomain.WorkWrite
	switch in.Capacity {
	case domain.Tester:
		permission = idomain.TestWrite
	case domain.Reviewer:
		permission = idomain.WorkReview
	case domain.Auditor:
		permission = idomain.AuditRead
	}
	project, err := s.Identity.AuthorizeProject(ctx, actor, projectID, permission)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	// An audit-only observer can inspect Audit but cannot create business facts.
	// A reviewer/test reporter also needs WorkItem read access for context.
	if in.Capacity != domain.Executor {
		if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
			return domain.RoleEntry{}, err
		}
	}
	if in.Capacity == domain.Auditor {
		if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkWrite); err != nil {
			return domain.RoleEntry{}, err
		}
	}
	return s.Roles.RecordRole(ctx, actor, project, itemID, in, meta, hashCommand("role_entry.record", struct {
		ItemID uuid.UUID
		Entry  domain.RoleEntryInput
	}{itemID, in}))
}
func (s Service) ListRoles(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) ([]domain.RoleEntry, error) {
	if s.Roles == nil {
		return nil, ErrNotFound
	}
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return nil, err
	}
	if _, err := s.Work.Get(ctx, projectID, itemID); err != nil {
		return nil, err
	}
	return s.Roles.ListRoles(ctx, projectID, itemID)
}
