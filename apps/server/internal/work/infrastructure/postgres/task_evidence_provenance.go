package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	identity "qual/internal/identity/application"
	"qual/internal/work/domain"
)

// Goose20 is the first schema supporting an immutable TestRun -> WorkItem
// binding. On Goose18/19, keep the old compatibility path, and never infer
// bound task authority merely from shared TestCase coverage.
func taskRunBindingAvailable(ctx context.Context, tx pgx.Tx) (bool, error) {
	var available bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
 WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&available)
	return available, err
}
func taskBoundRun(ctx context.Context, tx pgx.Tx, pid, runID uuid.UUID) (*uuid.UUID, bool, error) {
	enabled, err := taskRunBindingAvailable(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	if !enabled {
		return nil, false, nil
	}
	var bound *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`, pid, runID).Scan(&bound)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, identity.ErrNotFound
	}
	return bound, true, err
}

func (r Repository) validateTaskRoleReference(ctx context.Context, tx pgx.Tx, project identity.Project,
	item domain.WorkItem, kind domain.ReferenceKind, value string) error {
	if kind != domain.TestRunReference && kind != domain.ArtifactReference {
		return nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return domain.ErrValidation
	}
	enabled, err := taskRunBindingAvailable(ctx, tx)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	} // Goose19 accepts prior behavior; pilot requires Goose20.
	if kind == domain.TestRunReference {
		bound, _, err := taskBoundRun(ctx, tx, project.ID, id)
		if err != nil {
			return err
		}
		if bound == nil || *bound != item.ID {
			return identity.ErrForbidden
		}
		return nil
	}
	var targetKind string
	var targetID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT target_kind,target_id FROM qual.artifacts
 WHERE project_id=$1 AND id=$2 AND state='ready'`, project.ID, id).Scan(&targetKind, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	if targetKind == "work_item" && targetID == item.ID {
		return nil
	}
	if targetKind == "test_run" {
		bound, _, err := taskBoundRun(ctx, tx, project.ID, targetID)
		if err != nil {
			return err
		}
		if bound != nil && *bound == item.ID {
			return nil
		}
	}
	if targetKind == "test_attempt" {
		var runID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT i.run_id FROM qual.test_attempts a
  JOIN qual.test_run_items i ON i.project_id=a.project_id AND i.id=a.run_item_id
  WHERE a.project_id=$1 AND a.id=$2`, project.ID, targetID).Scan(&runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrNotFound
		}
		if err != nil {
			return err
		}
		bound, _, err := taskBoundRun(ctx, tx, project.ID, runID)
		if err != nil {
			return err
		}
		if bound != nil && *bound == item.ID {
			return nil
		}
	}
	return identity.ErrForbidden
}
