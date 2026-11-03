package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// ListReviewQueue is a project-scoped projection, never a business mutation.
// Only the most recent Reviewer event decides the remediation lane; both
// failed and needs_work must be included even while a WorkItem is in_progress.
// This avoids N+1 role lookups from the browser and does not expose audit rows.
func (r Repository) ListReviewQueue(ctx context.Context, projectID uuid.UUID, f application.ReviewQueueFilter) (application.ReviewQueuePage, error) {
	rows, err := r.Pool.Query(ctx, `
 SELECT w.id,w.key,w.title,w.kind,w.status,w.priority,w.version,w.assignee_id,w.updated_at,
        rev.id,rev.actor_id,rev.outcome,rev.summary,rev.reference_kind,rev.reference_value,
        rev.work_version,rev.self_review,rev.recorded_at,
        (w.status='in_review' AND rev.id IS NOT NULL AND EXISTS (
          SELECT 1 FROM qual.audit_records a
          WHERE a.project_id=w.project_id AND a.entity_id=w.id
            AND a.operation='work_item.transition'
            AND a.new_version > rev.work_version
            AND a.diff->>'status'='in_review'
        )) AS resubmitted_after_review
 FROM qual.work_items w
 LEFT JOIN LATERAL (
  SELECT e.id,e.actor_id,e.outcome,e.summary,e.reference_kind,e.reference_value,
         e.work_version,e.self_review,e.recorded_at
  FROM qual.work_role_entries e
  WHERE e.project_id=w.project_id AND e.work_item_id=w.id AND e.capacity='reviewer'
  ORDER BY e.recorded_at DESC,e.id DESC
  LIMIT 1
 ) rev ON true
 WHERE w.project_id=$1 AND
  (w.status='in_review' OR (w.status='in_progress' AND rev.outcome IN ('needs_work','failed')))
 ORDER BY CASE
  WHEN rev.outcome IN ('needs_work','failed') THEN 1
  WHEN w.status='in_review' AND rev.outcome='passed' AND rev.self_review=false THEN 2
  ELSE 0 END, w.updated_at DESC,w.id DESC
 LIMIT $2 OFFSET $3
 `, projectID, f.Limit+1, f.Offset)
	if err != nil {
		return application.ReviewQueuePage{}, err
	}
	defer rows.Close()
	page := application.ReviewQueuePage{Items: make([]application.ReviewQueueItem, 0, f.Limit)}
	for rows.Next() {
		var item application.ReviewQueueItem
		var kind, status, priority string
		var assigned, roleID, actorID pgtype.UUID
		var outcome, summary, referenceKind, referenceValue pgtype.Text
		var workVersion pgtype.Int8
		var selfReview pgtype.Bool
		var recordedAt pgtype.Timestamptz
		if err := rows.Scan(&item.WorkItemID, &item.Key, &item.Title, &kind, &status, &priority, &item.Version, &assigned, &item.UpdatedAt,
			&roleID, &actorID, &outcome, &summary, &referenceKind, &referenceValue, &workVersion, &selfReview, &recordedAt,
			&item.ResubmittedAfterReview); err != nil {
			return application.ReviewQueuePage{}, err
		}
		item.Kind = domain.Kind(kind)
		item.Status = domain.Status(status)
		item.Priority = domain.Priority(priority)
		if assigned.Valid {
			id, err := uuid.FromBytes(assigned.Bytes[:])
			if err != nil {
				return application.ReviewQueuePage{}, err
			}
			item.AssigneeID = &id
		}
		if roleID.Valid {
			reviewID, err := uuid.FromBytes(roleID.Bytes[:])
			if err != nil {
				return application.ReviewQueuePage{}, err
			}
			reviewerID, err := uuid.FromBytes(actorID.Bytes[:])
			if err != nil {
				return application.ReviewQueuePage{}, err
			}
			item.LatestReview = &application.ReviewQueueDecision{
				ID: reviewID, ActorID: reviewerID, Outcome: domain.Outcome(outcome.String), Summary: summary.String,
				ReferenceKind: domain.ReferenceKind(referenceKind.String), ReferenceValue: referenceValue.String,
				WorkVersion: workVersion.Int64, SelfReview: selfReview.Bool, RecordedAt: recordedAt.Time,
			}
		}
		item.ReviewLane = application.ClassifyReviewLane(item.Status, item.LatestReview, item.ResubmittedAfterReview)
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return application.ReviewQueuePage{}, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		page.HasMore = true
		next := f.Offset + f.Limit
		page.NextOffset = &next
	}
	return page, nil
}
