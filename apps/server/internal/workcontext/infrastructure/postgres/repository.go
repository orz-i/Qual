// Package postgres implements bounded, project-scoped WorkItem context projections.
// The projections only read existing Qual facts; they never compute a new result.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	contextapp "qual/internal/workcontext/application"
)

type Repository struct {
	Pool *pgxpool.Pool
}

// Only a genuinely installed Goose21 attribution source may be advertised
// to Task-first resume clients. Checking this never supplies a TestRun grant.
func (r Repository) HasHistoricalAttributions(ctx context.Context) (bool, error) {
	if r.Pool == nil {
		return false, contextapp.ErrInvalidQuery
	}
	var available bool
	err := r.Pool.QueryRow(ctx,
		"SELECT to_regclass('qual.historical_test_run_attributions') IS NOT NULL").Scan(&available)
	return available, err
}

// SQL strings are fixed per section: never interpolate user-supplied columns,
// filters, SQL fragments or untrusted JSON. limit <= 101 is enforced upstream.
func (r Repository) Page(ctx context.Context, project, item uuid.UUID, section contextapp.Section, q contextapp.PageQuery) ([]json.RawMessage, error) {
	if r.Pool == nil || project == uuid.Nil || item == uuid.Nil ||
		q.Limit < 1 || q.Limit > 101 || q.Offset < 0 || q.Offset > 10000 ||
		(section != contextapp.Attempts && (q.BeforeTime != nil || q.BeforeID != uuid.Nil)) ||
		(section == contextapp.Attempts && (q.BeforeTime == nil) != (q.BeforeID == uuid.Nil)) ||
		(section == contextapp.Attempts && q.BeforeTime != nil && q.BeforeTime.IsZero()) {
		return nil, contextapp.ErrInvalidQuery
	}
	if section == contextapp.Attempts {
		return r.attemptPage(ctx, project, item, q)
	}
	var query string
	switch section {
	case contextapp.Dependencies:
		query = `
			SELECT jsonb_build_object(
				'source','work_dependencies',
				'source_key',d.project_id::text||'/'||d.from_item_id::text||'/'||d.to_item_id::text,
				'from_item_id',d.from_item_id,'to_item_id',d.to_item_id,
				'key',w.key,'status',w.status,
				'version',w.version,'created_at',d.created_at)
			FROM qual.work_dependencies d
			JOIN qual.work_items w ON w.project_id=d.project_id AND w.id=d.to_item_id
			WHERE d.project_id=$1 AND d.from_item_id=$2
			ORDER BY d.created_at DESC,d.to_item_id DESC LIMIT $3 OFFSET $4`
	case contextapp.Comments:
		query = `
			SELECT jsonb_build_object(
				'source','work_comments','source_id',c.id,
				'author_id',c.author_id,'excerpt',left(c.body,240),
				'created_at',c.created_at)
			FROM qual.work_comments c
			WHERE c.project_id=$1 AND c.work_item_id=$2
			ORDER BY c.created_at DESC,c.id DESC LIMIT $3 OFFSET $4`
	case contextapp.Reviews:
		query = `
			SELECT jsonb_build_object(
				'source','work_role_entries','source_id',r.id,
				'actor_id',r.actor_id,'outcome',r.outcome,
				'work_version',r.work_version,'self_review',r.self_review,
				'reference_kind',r.reference_kind,'reference_value',r.reference_value,
				'excerpt',left(r.summary,240),'recorded_at',r.recorded_at)
			FROM qual.work_role_entries r
			WHERE r.project_id=$1 AND r.work_item_id=$2 AND r.capacity='reviewer'
			ORDER BY r.recorded_at DESC,r.id DESC LIMIT $3 OFFSET $4`
	case contextapp.Coverage:
		query = `
			SELECT jsonb_build_object(
				'source','test_case_coverage',
				'source_key',cv.project_id::text||'/'||cv.work_item_id::text||'/'||cv.test_case_id::text,
				'work_item_id',cv.work_item_id,'test_case_id',tc.id,'key',tc.key,
				'published_version_id',tc.published_version_id,
				'revision',tc.revision)
			FROM qual.test_case_coverage cv
			JOIN qual.test_cases tc ON tc.project_id=cv.project_id AND tc.id=cv.test_case_id
			WHERE cv.project_id=$1 AND cv.work_item_id=$2
			ORDER BY tc.key ASC,tc.id ASC LIMIT $3 OFFSET $4`
	case contextapp.HistoricalAttributions:
		// Goose21 is append-only; this is an index of provenance for a Task,
		// not an authorization edge to the original legacy Run/Attempts.
		// A separately authorized Task-context read is required before entry.
		query = `
			SELECT jsonb_build_object(
				'source','historical_test_run_attributions',
				'source_id',h.test_run_id,'run_id',h.test_run_id,
				'attribution_kind',h.attribution_kind,
				'role_entry_ids',h.role_entry_ids,
				'acceptance_ids',h.acceptance_ids,
				'recorded_at',h.recorded_at)
			FROM qual.historical_test_run_attributions h
			WHERE h.project_id=$1 AND h.work_item_id=$2
			ORDER BY h.recorded_at DESC,h.test_run_id DESC LIMIT $3 OFFSET $4`
	case contextapp.Artifacts:
		query = `
			SELECT jsonb_build_object(
				'source','artifacts','source_id',a.id,
				'id',a.id,'sha256',encode(a.sha256,'hex'),
				'filename',a.filename,'size_bytes',a.size_bytes,
				'ready_at',a.ready_at)
			FROM qual.artifacts a
			WHERE a.project_id=$1 AND a.target_kind='work_item'
			  AND a.target_id=$2 AND a.state='ready'
			ORDER BY a.ready_at DESC,a.id DESC LIMIT $3 OFFSET $4`
	case contextapp.References:
		query = `
			SELECT jsonb_build_object(
				'source','external_references','source_id',r.id,
				'provider',r.provider,'kind',r.kind,
				'external_id',r.external_id,'url',r.url,
				'created_at',r.created_at)
			FROM qual.external_references r
			WHERE r.project_id=$1 AND r.target_kind='work_item' AND r.target_id=$2
			ORDER BY r.created_at DESC,r.id DESC LIMIT $3 OFFSET $4`
	default:
		return nil, contextapp.ErrInvalidQuery
	}
	rows, err := r.Pool.Query(ctx, query, project, item, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]json.RawMessage, 0, q.Limit)
	for rows.Next() {
		var record json.RawMessage
		if err := rows.Scan(&record); err != nil {
			return nil, err
		}
		if !json.Valid(record) {
			return nil, errors.New("invalid work context projection")
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
