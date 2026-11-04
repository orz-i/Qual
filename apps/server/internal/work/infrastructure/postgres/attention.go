package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// Each source provides its top N scoped facts, ordered by urgency/time.
// One source fact per WorkItem survives source-specific de-duplication;
// globally, the highest-priority fact wins for each WorkItem.
// Query memory and API results are bounded to offset + limit + 1 <= 501.
const attentionSQL = `WITH review_base AS (
 SELECT w.id AS work_id,w.key,w.title,w.status,w.priority,w.assignee_id,
        w.updated_at,rev.id AS review_id,rev.outcome,rev.self_review,rev.recorded_at,
        (w.status='in_review' AND rev.id IS NOT NULL AND EXISTS(
           SELECT 1 FROM qual.audit_records a
           WHERE a.project_id=w.project_id AND a.entity_id=w.id
             AND a.operation='work_item.transition' AND a.new_version>rev.work_version
             AND a.diff->>'status'='in_review'
        )) AS resubmitted
 FROM qual.work_items w
 LEFT JOIN LATERAL (
   SELECT id,outcome,self_review,recorded_at,work_version
   FROM qual.work_role_entries e
   WHERE e.project_id=w.project_id AND e.work_item_id=w.id AND e.capacity='reviewer'
   ORDER BY e.recorded_at DESC,e.id DESC LIMIT 1
 ) rev ON true
 WHERE w.project_id=$1
   AND (w.status='in_review' OR (w.status='in_progress' AND rev.outcome IN ('needs_work','failed')))
), review_candidates AS (
 SELECT work_id,key,title,status,priority,assignee_id,
   CASE WHEN resubmitted THEN 'review_awaiting'
        WHEN outcome IN ('needs_work','failed') THEN 'review_needs_work'
        WHEN outcome='passed' AND status='in_review' AND self_review=false THEN 'review_ready'
        ELSE 'review_awaiting' END::text AS reason,
   'review'::text AS source_kind,COALESCE(review_id,work_id) AS source_id,
   COALESCE(outcome::text,'work_status') AS source_label,
   CASE WHEN resubmitted THEN updated_at ELSE COALESCE(recorded_at,updated_at) END AS observed_at,
   NULL::timestamptz AS source_at,
   false AS hint_only
 FROM review_base
), review_ranked AS (
 SELECT *,CASE reason WHEN 'review_needs_work' THEN 0 WHEN 'review_awaiting' THEN 1 ELSE 2 END AS urgency
 FROM review_candidates
 ORDER BY urgency,observed_at DESC,work_id DESC LIMIT $4
), run_candidates AS (
 SELECT w.id AS work_id,w.key,w.title,w.status,w.priority,w.assignee_id,
   CASE WHEN r.source_status='blocked' THEN 'run_blocked'
        WHEN r.source_status='running' AND r.last_seen_at IS NULL THEN 'run_unknown'
        ELSE 'run_stale' END::text AS reason,
   'agent_run'::text AS source_kind,r.id AS source_id,r.source AS source_label,
   COALESCE(r.last_seen_at,r.updated_at) AS observed_at,
   r.last_status_occurred_at AS source_at,true AS hint_only,
   CASE WHEN r.source_status='blocked' THEN 3
        WHEN r.last_seen_at IS NULL THEN 5 ELSE 4 END AS urgency
 FROM qual.agent_runs r JOIN qual.work_items w
   ON w.project_id=r.project_id AND w.id=r.work_item_id
 WHERE r.project_id=$1 AND w.status IN ('todo','in_progress','in_review')
   AND (r.source_status='blocked' OR (r.source_status='running' AND
        (r.last_seen_at IS NULL OR r.last_seen_at<$5)))
), run_best AS (
 SELECT DISTINCT ON (work_id) *
 FROM run_candidates ORDER BY work_id,urgency,observed_at DESC,source_id DESC
), run_ranked AS (
 SELECT * FROM run_best
 ORDER BY urgency,observed_at DESC,work_id DESC LIMIT $4
), event_candidates AS (
 SELECT DISTINCT ON (w.id) w.id AS work_id,w.key,w.title,w.status,w.priority,w.assignee_id,
   'event_needs_input'::text AS reason,'external_event'::text AS source_kind,
   e.id AS source_id,e.source AS source_label,e.received_at AS observed_at,
   e.occurred_at AS source_at,true AS hint_only,6 AS urgency
 FROM qual.external_events e JOIN qual.work_items w
   ON w.project_id=e.project_id AND w.id=e.work_item_id
 WHERE e.project_id=$1 AND e.event_type IN ('needs_input','attention.required')
   AND e.received_at>=$6 AND e.received_at>w.updated_at
   AND w.status IN ('todo','in_progress','in_review')
 ORDER BY w.id,e.received_at DESC,e.id DESC
), event_ranked AS (
 SELECT * FROM event_candidates
 ORDER BY urgency,observed_at DESC,work_id DESC LIMIT $4
), candidates AS (
 SELECT * FROM review_ranked
 UNION ALL SELECT * FROM run_ranked
 UNION ALL SELECT * FROM event_ranked
), dedup AS (
 SELECT *,row_number() OVER (PARTITION BY work_id
   ORDER BY urgency,observed_at DESC,source_id DESC) AS rn
 FROM candidates
)
SELECT work_id,key,title,status,priority,assignee_id,reason,
       source_kind,source_id,source_label,observed_at,source_at,hint_only
FROM dedup WHERE rn=1
ORDER BY urgency,observed_at DESC,work_id DESC,source_id DESC
LIMIT ($2::int+1) OFFSET $3::int`

func (r Repository) ListAttention(ctx context.Context, pid uuid.UUID, f application.AttentionFilter) (application.AttentionPage, error) {
	page := application.AttentionPage{Items: make([]application.AttentionItem, 0, f.Limit)}
	rows, err := r.Pool.Query(ctx, attentionSQL, pid, f.Limit, f.Offset, f.Offset+f.Limit+1,
		f.Now.Add(-f.StaleAfter), f.Now.Add(-7*24*time.Hour))
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var x application.AttentionItem
		var status, priority string
		var assignee pgtype.UUID
		var sourceAt pgtype.Timestamptz
		if err := rows.Scan(&x.WorkItemID, &x.Key, &x.Title, &status, &priority, &assignee,
			&x.Reason, &x.SourceKind, &x.SourceID, &x.SourceLabel, &x.ObservedAt, &sourceAt, &x.HintOnly); err != nil {
			return page, err
		}
		x.Status, x.Priority = domain.Status(status), domain.Priority(priority)
		if assignee.Valid {
			id, err := uuid.FromBytes(assignee.Bytes[:])
			if err != nil {
				return page, err
			}
			x.AssigneeID = &id
		}
		if sourceAt.Valid {
			t := sourceAt.Time
			x.SourceAt = &t
		}
		page.Items = append(page.Items, x)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		page.HasMore = true
		next := f.Offset + f.Limit
		if next <= 450 {
			page.NextOffset = &next
		} else {
			page.PagingLimited = true
		}
	}
	return page, nil
}
