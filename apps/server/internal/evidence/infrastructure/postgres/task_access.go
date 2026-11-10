package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	workdomain "qual/internal/work/domain"
	workpg "qual/internal/work/infrastructure/postgres"
)

// Task and external test identities are never inferred from arbitrary
// Artifact targets. For scoped projects, only an explicit WorkItem target or
// a TestRun/TestAttempt with an immutable Goose20 WorkItem binding qualifies.
func (r Repository) TaskMode(ctx context.Context, pid uuid.UUID) (workdomain.TaskAuthorizationMode, error) {
	return (workpg.Repository{Pool: r.Pool}).GetTaskMode(ctx, pid)
}
func (r Repository) TargetWork(ctx context.Context, pid uuid.UUID, kind domain.TargetKind, id uuid.UUID) (*uuid.UUID, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	workID, err := r.targetWorkTx(ctx, tx, pid, kind, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return workID, nil
}

func (r Repository) targetWorkTx(ctx context.Context, tx pgx.Tx, pid uuid.UUID, kind domain.TargetKind, id uuid.UUID) (*uuid.UUID, error) {
	switch kind {
	case domain.WorkItem:
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`, pid, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, domain.ErrNotFound
		}
		return &id, nil
	case domain.TestCase:
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_cases WHERE project_id=$1 AND id=$2)`, pid, id).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, domain.ErrNotFound
		}
		return nil, nil
	case domain.TestRun, domain.TestAttempt:
		var runID uuid.UUID
		if kind == domain.TestRun {
			runID = id
		} else {
			err := tx.QueryRow(ctx, `SELECT i.run_id FROM qual.test_attempts a
    JOIN qual.test_run_items i ON i.project_id=a.project_id AND i.id=a.run_item_id
    WHERE a.project_id=$1 AND a.id=$2`, pid, id).Scan(&runID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
		}
		var schema bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
   WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&schema); err != nil {
			return nil, err
		}
		if !schema {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE project_id=$1 AND id=$2)`, pid, runID).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				return nil, domain.ErrNotFound
			}
			return nil, nil
		}
		var workID *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, pid, runID).Scan(&workID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return workID, err
	default:
		return nil, domain.ErrValidation
	}
}
func (r Repository) ArtifactTarget(ctx context.Context, pid, id uuid.UUID) (domain.Artifact, error) {
	artifact, _, err := r.GetArtifact(ctx, pid, id)
	return artifact, err
}
func (r Repository) scopedArtifactWrite(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	project identity.Project, kind domain.TargetKind, target uuid.UUID) error {
	workID, err := r.targetWorkTx(ctx, tx, project.ID, kind, target)
	if err != nil {
		return err
	}
	gate := workpg.Repository{Pool: r.Pool}
	if workID != nil {
		return gate.AuthorizeTaskResource(ctx, tx, actor, project, *workID, workdomain.TaskAttachEvidence, idomain.ArtifactWrite)
	}
	return gate.AuthorizeUnboundResource(ctx, tx, actor, project, idomain.ArtifactWrite)
}
func (r Repository) AuthorizeTaskArtifactRead(ctx context.Context, actor identity.Actor,
	project identity.Project, kind domain.TargetKind, target uuid.UUID) error {
	workID, err := r.TargetWork(ctx, project.ID, kind, target)
	if err != nil {
		return err
	}
	gate := workpg.Repository{Pool: r.Pool}
	if workID != nil {
		return gate.AuthorizeTaskResourceRead(ctx, actor, project, *workID, workdomain.TaskReadEvidence, idomain.ArtifactRead)
	}
	return gate.AuthorizeUnboundResourceRead(ctx, actor, project, idomain.ArtifactRead)
}

// Task-bound external URL facts share the evidence relationship check;
// metadata links cannot bypass executor/reviewer authorization.
func (r Repository) scopedReferenceWrite(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	project identity.Project, kind domain.TargetKind, target uuid.UUID) error {
	workID, err := r.targetWorkTx(ctx, tx, project.ID, kind, target)
	if err != nil {
		return err
	}
	gate := workpg.Repository{Pool: r.Pool}
	legacy := idomain.TestWrite
	if kind == domain.WorkItem {
		legacy = idomain.WorkWrite
	}
	if workID != nil {
		return gate.AuthorizeTaskResource(ctx, tx, actor, project, *workID, workdomain.TaskAttachEvidence, legacy)
	}
	return gate.AuthorizeUnboundResource(ctx, tx, actor, project, legacy)
}
