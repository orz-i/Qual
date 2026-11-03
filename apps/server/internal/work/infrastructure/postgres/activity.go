package postgres

import (
	"context"

	"github.com/google/uuid"
	"qual/internal/work/application"
)

func (r Repository) Activity(ctx context.Context, projectID uuid.UUID, limit int) ([]application.ActivityRecord, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,actor_id,operation,entity_id,occurred_at
		FROM qual.activity_records WHERE project_id=$1
		ORDER BY occurred_at DESC,id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]application.ActivityRecord, 0)
	for rows.Next() {
		var v application.ActivityRecord
		if err := rows.Scan(&v.ID, &v.ActorID, &v.Operation, &v.EntityID, &v.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (r Repository) Audit(ctx context.Context, projectID uuid.UUID, limit int) ([]application.AuditRecord, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,actor_id,credential_id,request_id,operation,entity_id,
		old_version,new_version,occurred_at FROM qual.audit_records
		WHERE project_id=$1 ORDER BY occurred_at DESC,id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]application.AuditRecord, 0)
	for rows.Next() {
		var v application.AuditRecord
		if err := rows.Scan(&v.ID, &v.ActorID, &v.CredentialID, &v.RequestID, &v.Operation, &v.EntityID, &v.OldVersion, &v.NewVersion, &v.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
