package application

import (
	"context"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

func (s Service) AddComment(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID,
	body string, expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	if err := domain.ValidateComment(body); err != nil {
		return domain.WorkItem{}, err
	}
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil {
		return domain.WorkItem{}, err
	}
	hash := hashCommand("comment", struct {
		ID      uuid.UUID
		Body    string
		Version int64
	}{itemID, body, expected})
	return s.Work.AddComment(ctx, actor, project, itemID, expected, body, meta, hash)
}

func (s Service) LinkDependency(ctx context.Context, actor identity.Actor, projectID, fromID, toID uuid.UUID,
	expected int64, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if expected < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	if fromID == uuid.Nil || toID == uuid.Nil || fromID == toID {
		return domain.WorkItem{}, domain.ErrDependency
	}
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkWrite)
	if err != nil {
		return domain.WorkItem{}, err
	}
	hash := hashCommand("link_dependency", struct {
		From, To uuid.UUID
		Version  int64
	}{fromID, toID, expected})
	return s.Work.LinkDependency(ctx, actor, project, fromID, toID, expected, meta, hash)
}

func (s Service) Comments(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) ([]domain.Comment, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return nil, err
	}
	// Prevent revealing comments for non-existent or out-of-project work items.
	if _, err := s.Work.Get(ctx, projectID, itemID); err != nil {
		return nil, err
	}
	return s.Work.Comments(ctx, projectID, itemID)
}

func (s Service) Dependencies(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID) ([]domain.Dependency, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return nil, err
	}
	if _, err := s.Work.Get(ctx, projectID, itemID); err != nil {
		return nil, err
	}
	return s.Work.Dependencies(ctx, projectID, itemID)
}
