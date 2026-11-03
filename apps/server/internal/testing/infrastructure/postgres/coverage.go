package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"qual/internal/testing/application"
	"qual/internal/testing/domain"
)

func (r Repository) Coverage(ctx context.Context, pid, cid uuid.UUID) ([]application.CoveredWorkItem, error) {
	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_cases WHERE project_id=$1 AND id=$2)`, pid, cid).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, err := r.Pool.Query(ctx, `SELECT w.id,w.key,w.title,w.status FROM qual.test_case_coverage l
 JOIN qual.work_items w ON w.id=l.work_item_id
 WHERE l.project_id=$1 AND l.test_case_id=$2 ORDER BY w.key LIMIT 100`, pid, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]application.CoveredWorkItem, 0)
	for rows.Next() {
		var v application.CoveredWorkItem
		if err := rows.Scan(&v.ID, &v.Key, &v.Title, &v.Status); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Repository) CoveredCases(ctx context.Context, pid, wid uuid.UUID) ([]application.CoveringTestCase, error) {
	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`, pid, wid).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, err := r.Pool.Query(ctx, `SELECT c.id,c.key,c.draft,
 COALESCE(v.version_no,0) FROM qual.test_case_coverage l
 JOIN qual.test_cases c ON c.id=l.test_case_id
 LEFT JOIN qual.test_case_versions v ON v.id=c.published_version_id
 WHERE l.project_id=$1 AND l.work_item_id=$2 ORDER BY c.key LIMIT 100`, pid, wid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]application.CoveringTestCase, 0)
	for rows.Next() {
		var v application.CoveringTestCase
		var draft []byte
		if err := rows.Scan(&v.ID, &v.Key, &draft, &v.PublishedVersion); err != nil {
			return nil, err
		}
		var spec struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(draft, &spec); err != nil {
			return nil, err
		}
		v.Title = spec.Title
		out = append(out, v)
	}
	return out, rows.Err()
}
