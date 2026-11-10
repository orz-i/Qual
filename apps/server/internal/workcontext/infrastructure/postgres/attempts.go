package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	identity "qual/internal/identity/application"
	workdomain "qual/internal/work/domain"
	contextapp "qual/internal/workcontext/application"
)

// Hard per-request scan budget protects the default service from a sparse
// project-wide history. A budget exhaustion is an explicit 503, NEVER an
// apparently complete or silently truncated historical result.
const (
	narrowRunItemCeiling = 32
	candidateBatchSize   = 512
	maxCandidateScans    = 16384
)

// Narrow path: a small number of qualifying RunItems can be read without
// visiting unrelated project Attempts. This reads at most 33 run-item keys
// before choosing a query plan, regardless of project history size.
const narrowCountSQL = `
 SELECT count(*) FROM (
  SELECT 1 FROM qual.test_case_coverage cv
  JOIN qual.test_run_items ri ON ri.project_id=cv.project_id AND ri.test_case_id=cv.test_case_id
  WHERE cv.project_id=$1 AND cv.work_item_id=$2 LIMIT $3
 ) t`

// In task_scoped mode a shared Case coverage edge is NOT enough to read
// an Attempt. Only frozen Goose20 TestRuns explicitly owned by this WorkItem
// contribute to its private context, with keyset pagination.
const scopedAttemptSQL = `
 SELECT jsonb_build_object(
  'source','test_attempts','source_id',a.id,
  'run_id',r.id,'run_item_id',ri.id,
  'test_case_id',ri.test_case_id,'test_case_version_id',ri.test_case_version_id,
  'attempt_no',a.attempt_no,'status',a.status,'source_kind',a.source_kind,
  'observed_at',a.observed_at,'ingested_at',a.ingested_at,'excerpt',left(a.note,240))
 FROM qual.test_runs r
 JOIN qual.test_run_items ri ON ri.project_id=r.project_id AND ri.run_id=r.id
 JOIN qual.test_attempts a ON a.project_id=ri.project_id AND a.run_item_id=ri.id
 WHERE r.project_id=$1 AND r.work_item_id=$2
   AND ($4::timestamptz IS NULL OR (a.ingested_at,a.id)<($4::timestamptz,$5::uuid))
 ORDER BY a.ingested_at DESC,a.id DESC LIMIT $3`

// With <=32 RunItems, the per-RunItem index gives bounded local scans, and
// the global sort includes every qualifying source before LIMIT is applied.
const narrowAttemptSQL = `
 SELECT jsonb_build_object(
  'source','test_attempts','source_id',a.id,
  'run_id',r.id,'run_item_id',ri.id,
  'test_case_id',ri.test_case_id,'test_case_version_id',ri.test_case_version_id,
  'attempt_no',a.attempt_no,'status',a.status,'source_kind',a.source_kind,
  'observed_at',a.observed_at,'ingested_at',a.ingested_at,'excerpt',left(a.note,240))
 FROM qual.test_case_coverage cv
 JOIN qual.test_run_items ri ON ri.project_id=cv.project_id AND ri.test_case_id=cv.test_case_id
 JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id
 JOIN LATERAL (
  SELECT a.id,a.attempt_no,a.status,a.source_kind,a.observed_at,a.ingested_at,a.note
  FROM qual.test_attempts a
  WHERE a.project_id=ri.project_id AND a.run_item_id=ri.id
   AND ($4::timestamptz IS NULL OR (a.ingested_at,a.id)<($4::timestamptz,$5::uuid))
  ORDER BY a.ingested_at DESC,a.id DESC LIMIT $3
 ) a ON true
 WHERE cv.project_id=$1 AND cv.work_item_id=$2
 ORDER BY a.ingested_at DESC,a.id DESC LIMIT $3`

// Wide path: MATERIALIZED forces exactly one globally sorted project Attempt
// batch BEFORE any coverage join. Each row is checked for authorized WorkItem
// coverage; an unrelated Attempt can advance the internal scan cursor but is
// never returned. We keep fetching until we have limit+1 matching rows or
// exhaust the underlying history. Limit-before-coverage without continuation
// would silently lose valid rows, and is intentionally not used.
const candidateAttemptSQL = `
 WITH candidate AS MATERIALIZED (
  SELECT a.id,a.project_id,a.run_item_id,a.attempt_no,a.status,a.source_kind,
         a.observed_at,a.ingested_at,a.note
  FROM qual.test_attempts a
  WHERE a.project_id=$1
   AND ($4::timestamptz IS NULL OR (a.ingested_at,a.id)<($4::timestamptz,$5::uuid))
  ORDER BY a.ingested_at DESC,a.id DESC LIMIT $3
 )
 SELECT a.ingested_at,a.id,
  CASE WHEN cv.work_item_id IS NULL THEN NULL
   ELSE jsonb_build_object(
    'source','test_attempts','source_id',a.id,
    'run_id',r.id,'run_item_id',ri.id,
    'test_case_id',ri.test_case_id,'test_case_version_id',ri.test_case_version_id,
    'attempt_no',a.attempt_no,'status',a.status,'source_kind',a.source_kind,
    'observed_at',a.observed_at,'ingested_at',a.ingested_at,'excerpt',left(a.note,240))
  END
 FROM candidate a
 JOIN qual.test_run_items ri ON ri.project_id=a.project_id AND ri.id=a.run_item_id
 JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id
 LEFT JOIN qual.test_case_coverage cv ON cv.project_id=a.project_id
  AND cv.work_item_id=$2 AND cv.test_case_id=ri.test_case_id
 ORDER BY a.ingested_at DESC,a.id DESC`

func (r Repository) attemptPage(ctx context.Context, project, item uuid.UUID, q contextapp.PageQuery) ([]json.RawMessage, error) {
	mode, err := r.TaskMode(ctx, project)
	if err != nil {
		return nil, err
	}
	if mode == workdomain.TaskScopedAuthorization {
		var ready bool
		if err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
			WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id')`).Scan(&ready); err != nil {
			return nil, err
		}
		if !ready {
			return nil, identity.ErrForbidden
		}
		rows, err := r.Pool.Query(ctx, scopedAttemptSQL, project, item, q.Limit, q.BeforeTime, q.BeforeID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanFacts(rows, q.Limit)
	}
	var matchedRunItems int
	if err := r.Pool.QueryRow(ctx, narrowCountSQL, project, item, narrowRunItemCeiling+1).Scan(&matchedRunItems); err != nil {
		return nil, err
	}
	if matchedRunItems <= narrowRunItemCeiling {
		rows, err := r.Pool.Query(ctx, narrowAttemptSQL, project, item, q.Limit, q.BeforeTime, q.BeforeID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanFacts(rows, q.Limit)
	}
	// Do not reuse global scan progress as the external cursor: only the last
	// *returned authorized Attempt* is a stable page boundary for the client.
	beforeTime, beforeID := q.BeforeTime, q.BeforeID
	facts := make([]json.RawMessage, 0, q.Limit)
	scanned := 0
	for scanned < maxCandidateScans {
		budget := maxCandidateScans - scanned
		batch := candidateBatchSize
		if budget < batch {
			batch = budget
		}
		rows, err := r.Pool.Query(ctx, candidateAttemptSQL, project, item, batch, beforeTime, beforeID)
		if err != nil {
			return nil, err
		}
		observed := 0
		foundEnough := false
		for rows.Next() {
			observed++
			var ingested time.Time
			var id uuid.UUID
			var fact json.RawMessage
			if err := rows.Scan(&ingested, &id, &fact); err != nil {
				rows.Close()
				return nil, err
			}
			beforeTime, beforeID = &ingested, id
			if len(fact) > 0 {
				if !json.Valid(fact) {
					rows.Close()
					return nil, errors.New("invalid context Attempt projection")
				}
				facts = append(facts, fact)
				if len(facts) >= q.Limit {
					foundEnough = true
					break
				}
			}
		}
		queryErr := rows.Err()
		rows.Close()
		if queryErr != nil {
			return nil, queryErr
		}
		if foundEnough {
			return facts, nil
		}
		scanned += observed
		if observed < batch {
			return facts, nil
		}
	}
	// Critical: returning facts here would make an incomplete page look
	// complete. No Done/Passed or completeness can be inferred from this error.
	return nil, contextapp.ErrScanBudget
}

// scanFacts is shared by narrow pages only; the source query already limits
// the returned count, so it does not need to retain historical row bodies.
func scanFacts(rows pgx.Rows, limit int) ([]json.RawMessage, error) {
	facts := make([]json.RawMessage, 0, limit)
	for rows.Next() {
		var fact json.RawMessage
		if err := rows.Scan(&fact); err != nil {
			return nil, err
		}
		if !json.Valid(fact) {
			return nil, errors.New("invalid context Attempt projection")
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}
