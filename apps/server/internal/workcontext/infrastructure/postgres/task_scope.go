package postgres

import (
	"context"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	workdomain "qual/internal/work/domain"
	workpg "qual/internal/work/infrastructure/postgres"
	contextapp "qual/internal/workcontext/application"
)

// The WorkItem context is an evidence projection, not a backdoor to a
// project-wide TestRun or Artifact listing. A scoped Agent must currently
// own/hold review for the named WorkItem, or be its real accountable Owner.
func (r Repository) TaskMode(ctx context.Context, projectID uuid.UUID) (workdomain.TaskAuthorizationMode, error) {
	return (workpg.Repository{Pool: r.Pool}).GetTaskMode(ctx, projectID)
}
func (r Repository) AuthorizeTaskContextRead(ctx context.Context, actor identity.Actor,
	project identity.Project, workID uuid.UUID, section contextapp.Section) error {
	action := workdomain.TaskReadEvidence
	legacy := idomain.ArtifactRead
	if section == contextapp.Attempts || section == contextapp.Coverage {
		action = workdomain.TaskReadTest
		legacy = idomain.TestRead
	}
	return (workpg.Repository{Pool: r.Pool}).AuthorizeTaskResourceRead(ctx, actor, project, workID, action, legacy)
}
