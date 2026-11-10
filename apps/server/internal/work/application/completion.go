package application

import (
	"context"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/domain"
)

// Complete is the only explicit acceptance use case. It never takes Actor,
// CredentialID, policy source or self-review facts from the caller. Those are
// resolved with active identity and inside the WorkItem's locked transaction.
func (s Service) Complete(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID,
	decision domain.DoneDecision, meta Meta) (domain.WorkItem, error) {
	if err := validMeta(meta); err != nil {
		return domain.WorkItem{}, err
	}
	if decision.ExpectedVersion < 1 {
		return domain.WorkItem{}, ErrPrecondition
	}
	project, err := s.authorizeTaskProject(ctx, actor, projectID, idomain.WorkReview)
	if err != nil {
		return domain.WorkItem{}, err
	}
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, idomain.WorkRead); err != nil {
		return domain.WorkItem{}, err
	}
	if s.Completion == nil {
		return domain.WorkItem{}, domain.ErrDoneGate
	}
	return s.Completion.Complete(ctx, actor, project, itemID, decision, meta,
		hashCommand("complete", struct {
			ItemID uuid.UUID
			Input  domain.DoneDecision
		}{itemID, decision}))
}
