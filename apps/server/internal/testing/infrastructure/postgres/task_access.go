package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/testing/application"
	testing "qual/internal/testing/domain"
	workdomain "qual/internal/work/domain"
	work "qual/internal/work/infrastructure/postgres"
)

// TestRun WorkItem binding is authoritative only from Goose20 onward.
// Legacy runs remain unbound and never acquire task-scoped permissions by
// happenstance merely because a TestCase covers multiple WorkItems.
func (r Repository) TaskMode(ctx context.Context, pid uuid.UUID) (workdomain.TaskAuthorizationMode, error) {
	return (work.Repository{Pool: r.Pool}).GetTaskMode(ctx, pid)
}
func (r Repository) boundRunColumn(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
  WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&enabled)
	return enabled, err
}
func (r Repository) runWork(ctx context.Context, pid, rid uuid.UUID) (*uuid.UUID, error) {
	var installed bool
	err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
  WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&installed)
	if err != nil {
		return nil, err
	}
	var workID *uuid.UUID
	if installed {
		err = r.Pool.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, pid, rid).Scan(&workID)
	} else {
		var found bool
		err = r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE project_id=$1 AND id=$2)`, pid, rid).Scan(&found)
		if err == nil && !found {
			return nil, testing.ErrNotFound
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, testing.ErrNotFound
	}
	return workID, err
}
func (r Repository) RunWork(ctx context.Context, pid, rid uuid.UUID) (*uuid.UUID, error) {
	return r.runWork(ctx, pid, rid)
}
func (r Repository) RunItemWork(ctx context.Context, pid, itemID uuid.UUID) (*uuid.UUID, error) {
	var runID uuid.UUID
	err := r.Pool.QueryRow(ctx, `SELECT run_id FROM qual.test_run_items WHERE project_id=$1 AND id=$2`, pid, itemID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, testing.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.runWork(ctx, pid, runID)
}
func (r Repository) AttemptWork(ctx context.Context, pid, attemptID uuid.UUID) (*uuid.UUID, error) {
	var runID uuid.UUID
	err := r.Pool.QueryRow(ctx, `SELECT ri.run_id FROM qual.test_attempts a JOIN qual.test_run_items ri
 ON ri.id=a.run_item_id AND ri.project_id=a.project_id WHERE a.project_id=$1 AND a.id=$2`,
		pid, attemptID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, testing.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.runWork(ctx, pid, runID)
}

func (r Repository) taskRunGuard(ctx context.Context, tx pgx.Tx, actor identity.Actor,
	p identity.Project, workID *uuid.UUID, action workdomain.TaskAction) error {
	gate := work.Repository{Pool: r.Pool}
	if workID != nil {
		return gate.AuthorizeTaskResource(ctx, tx, actor, p, *workID, action, idomain.TestWrite)
	}
	return gate.AuthorizeUnboundResource(ctx, tx, actor, p, idomain.TestWrite)
}

// CaseVersions in a task-bound Run must be covered by exactly that WorkItem.
// If a current executor is already authorized to link their own coverage,
// register the missing link atomically with this Run and its Audit/Outbox.
// A reviewer, Integration Reporter or unrelated Agent cannot manufacture
// a new coverage relationship merely by submitting a TestRun.
func (r Repository) validateRunCoverage(ctx context.Context, tx pgx.Tx,
	actor identity.Actor, p identity.Project, m application.Meta, workID, caseID uuid.UUID) error {
	var linked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_case_coverage
  WHERE project_id=$1 AND work_item_id=$2 AND test_case_id=$3)`, p.ID, workID, caseID).Scan(&linked)
	if err != nil {
		return err
	}
	if linked {
		return nil
	}
	if actor.Type == idomain.Integration {
		return identity.ErrForbidden
	}
	if err := r.taskRunGuard(ctx, tx, actor, p, &workID, workdomain.TaskRecordTester); err != nil {
		return err
	}
	var revision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM qual.test_cases WHERE project_id=$1 AND id=$2`,
		p.ID, caseID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return testing.ErrNotFound
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO qual.test_case_coverage(project_id,test_case_id,work_item_id)
 VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, p.ID, caseID, workID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return r.emit(ctx, tx, actor, p, m, "test_case.coverage_linked", caseID, nil, revision,
			map[string]any{"work_item_id": workID, "source": "task_bound_run"})
	}
	return nil
}

func (r Repository) AuthorizeTaskTestRead(ctx context.Context, actor identity.Actor,
	project identity.Project, workID *uuid.UUID) error {
	gate := work.Repository{Pool: r.Pool}
	if workID != nil {
		return gate.AuthorizeTaskResourceRead(ctx, actor, project, *workID, workdomain.TaskReadTest, idomain.TestRead)
	}
	return gate.AuthorizeUnboundResourceRead(ctx, actor, project, idomain.TestRead)
}

// Resolve the linked WorkItem within the same transaction as the immutable
// test record. The RunItem ID supplied by the caller never chooses a task.
func (r Repository) boundRunItemInTx(ctx context.Context, tx pgx.Tx,
	pid, itemID uuid.UUID) (*uuid.UUID, error) {
	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT run_id FROM qual.test_run_items
  WHERE project_id=$1 AND id=$2`, pid, itemID).Scan(&runID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, testing.ErrNotFound
		}
		return nil, err
	}
	installed, err := r.boundRunColumn(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !installed {
		return nil, nil
	}
	var workID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs
  WHERE project_id=$1 AND id=$2`, pid, runID).Scan(&workID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, testing.ErrNotFound
	}
	return workID, err
}

func (r Repository) boundRunInTx(ctx context.Context, tx pgx.Tx, pid, runID uuid.UUID) (*uuid.UUID, error) {
	installed, err := r.boundRunColumn(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !installed {
		var found bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE project_id=$1 AND id=$2)`, pid, runID).Scan(&found); err != nil {
			return nil, err
		}
		if !found {
			return nil, testing.ErrNotFound
		}
		return nil, nil
	}
	var id *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, pid, runID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, testing.ErrNotFound
	}
	return id, err
}
