package postgres

import (
	"context"

	"github.com/google/uuid"
	"qual/internal/testing/domain"
)

// AttemptDefects does not infer relationships from a latest result: it reads
// only existing, immutable Attempt → Bug links in this exact project.
func (r Repository) AttemptDefects(ctx context.Context, projectID, attemptID uuid.UUID) ([]domain.AttemptDefect, error) {
	var exists bool
	err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_attempts
  WHERE project_id=$1 AND id=$2)`, projectID, attemptID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, err := r.Pool.Query(ctx, `SELECT d.attempt_id,d.bug_work_item_id,w.key,w.title,w.status,d.created_at,d.linked_by
 FROM qual.attempt_defects d
 JOIN qual.work_items w ON w.id=d.bug_work_item_id AND w.project_id=d.project_id
 WHERE d.project_id=$1 AND d.attempt_id=$2
 ORDER BY d.created_at DESC,d.bug_work_item_id
 `, projectID, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.AttemptDefect, 0)
	for rows.Next() {
		var v domain.AttemptDefect
		if scanErr := rows.Scan(&v.AttemptID, &v.BugID, &v.BugKey, &v.BugTitle, &v.BugStatus, &v.LinkedAt, &v.LinkedBy); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
