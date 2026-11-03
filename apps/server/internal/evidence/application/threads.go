package application

import (
	"context"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
	role "qual/internal/identity/domain"
)

type ThreadRepository interface {
	LinkThread(context.Context, identity.Actor, identity.Project, uuid.UUID, domain.ThreadLinkInput, Meta, [32]byte) (domain.ThreadMutation, error)
	UnlinkThread(context.Context, identity.Actor, identity.Project, uuid.UUID, uuid.UUID, int64, Meta, [32]byte) (domain.ThreadMutation, error)
	ListWorkThreads(context.Context, uuid.UUID, uuid.UUID, int, uuid.UUID) (domain.WorkThreadPage, error)
	ListThreadWorkItems(context.Context, uuid.UUID, uuid.UUID, int, uuid.UUID) (domain.ThreadWorkItemPage, error)
}

func (s Service) LinkThread(ctx context.Context, a identity.Actor, projectID, itemID uuid.UUID, in domain.ThreadLinkInput, meta Meta) (domain.ThreadMutation, error) {
	if err := meta.Validate(); err != nil {
		return domain.ThreadMutation{}, err
	}
	if itemID == uuid.Nil {
		return domain.ThreadMutation{}, domain.ErrValidation
	}
	if err := in.Validate(); err != nil {
		return domain.ThreadMutation{}, err
	}
	if a.Type == role.Integration {
		return domain.ThreadMutation{}, identity.ErrForbidden
	}
	project, err := s.authorize(ctx, a, projectID, role.WorkWrite)
	if err != nil {
		return domain.ThreadMutation{}, err
	}
	return s.Threads.LinkThread(ctx, a, project, itemID, in, meta, digest("work_thread.link", struct {
		ItemID uuid.UUID
		Input  domain.ThreadLinkInput
	}{itemID, in}))
}
func (s Service) UnlinkThread(ctx context.Context, a identity.Actor, projectID, itemID, threadID uuid.UUID, expectedVersion int64, meta Meta) (domain.ThreadMutation, error) {
	if err := meta.Validate(); err != nil {
		return domain.ThreadMutation{}, err
	}
	if itemID == uuid.Nil || threadID == uuid.Nil || expectedVersion < 1 {
		return domain.ThreadMutation{}, domain.ErrValidation
	}
	if a.Type == role.Integration {
		return domain.ThreadMutation{}, identity.ErrForbidden
	}
	project, err := s.authorize(ctx, a, projectID, role.WorkWrite)
	if err != nil {
		return domain.ThreadMutation{}, err
	}
	return s.Threads.UnlinkThread(ctx, a, project, itemID, threadID, expectedVersion, meta,
		digest("work_thread.unlink", struct {
			ItemID   uuid.UUID
			ThreadID uuid.UUID
			Version  int64
		}{itemID, threadID, expectedVersion}))
}
func (s Service) WorkThreads(ctx context.Context, a identity.Actor, projectID, itemID uuid.UUID, limit int, cursor uuid.UUID) (domain.WorkThreadPage, error) {
	if itemID == uuid.Nil || limit < 1 || limit > 100 {
		return domain.WorkThreadPage{}, domain.ErrValidation
	}
	if _, err := s.authorize(ctx, a, projectID, role.WorkRead); err != nil {
		return domain.WorkThreadPage{}, err
	}
	return s.Threads.ListWorkThreads(ctx, projectID, itemID, limit, cursor)
}
func (s Service) ThreadWorkItems(ctx context.Context, a identity.Actor, projectID, threadID uuid.UUID, limit int, cursor uuid.UUID) (domain.ThreadWorkItemPage, error) {
	if threadID == uuid.Nil || limit < 1 || limit > 100 {
		return domain.ThreadWorkItemPage{}, domain.ErrValidation
	}
	if _, err := s.authorize(ctx, a, projectID, role.WorkRead); err != nil {
		return domain.ThreadWorkItemPage{}, err
	}
	return s.Threads.ListThreadWorkItems(ctx, projectID, threadID, limit, cursor)
}
