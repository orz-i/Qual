package application

import (
	"context"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
)

type CoveredWorkItem struct {
	ID     uuid.UUID `json:"id"`
	Key    string    `json:"key"`
	Title  string    `json:"title"`
	Status string    `json:"status"`
}
type CoveringTestCase struct {
	ID               uuid.UUID `json:"id"`
	Key              string    `json:"key"`
	Title            string    `json:"title"`
	PublishedVersion int       `json:"published_version"`
}
type CoverageRepository interface {
	Coverage(context.Context, uuid.UUID, uuid.UUID) ([]CoveredWorkItem, error)
	CoveredCases(context.Context, uuid.UUID, uuid.UUID) ([]CoveringTestCase, error)
}

func (s Service) Coverage(ctx context.Context, actor identity.Actor, pid, cid uuid.UUID) ([]CoveredWorkItem, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, idomain.TestRead); err != nil {
		return nil, err
	}
	return s.Tests.Coverage(ctx, pid, cid)
}
func (s Service) CoveredCases(ctx context.Context, actor identity.Actor, pid, wid uuid.UUID) ([]CoveringTestCase, error) {
	if _, err := s.Identity.AuthorizeProject(ctx, actor, pid, idomain.WorkRead); err != nil {
		return nil, err
	}
	return s.Tests.CoveredCases(ctx, pid, wid)
}
