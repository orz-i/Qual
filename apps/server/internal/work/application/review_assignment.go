package application

import (
	"context"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// ReviewAssignmentRepository persists only the current reviewer relationship.
// Append-only RoleEntry/Audit remain historical evidence, never a live grant.
type ReviewAssignmentRepository interface {
	ClaimReview(context.Context, identity.Actor, identity.Project, uuid.UUID, int64, Meta, [32]byte) (domain.WorkItem, error)
	ReleaseReview(context.Context, identity.Actor, identity.Project, uuid.UUID, int64, Meta, [32]byte) (domain.WorkItem, error)
}

func (s Service) ClaimReview(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, expected int64, meta Meta) (domain.WorkItem, error) {
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
	if s.ReviewAssignments == nil {
		return domain.WorkItem{}, domain.ErrDoneGate
	}
	return s.ReviewAssignments.ClaimReview(ctx, actor, project, itemID, expected, meta, hashCommand("review_claim", struct {
		ID      uuid.UUID
		Version int64
	}{itemID, expected}))
}
func (s Service) ReleaseReview(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, expected int64, meta Meta) (domain.WorkItem, error) {
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
	if s.ReviewAssignments == nil {
		return domain.WorkItem{}, domain.ErrDoneGate
	}
	return s.ReviewAssignments.ReleaseReview(ctx, actor, project, itemID, expected, meta, hashCommand("review_release", struct {
		ID      uuid.UUID
		Version int64
	}{itemID, expected}))
}
