package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

func (r Repository) RecordRole(ctx context.Context, actor identity.Actor, project identity.Project, itemID uuid.UUID,
	input domain.RoleEntryInput, meta application.Meta, hash [32]byte) (domain.RoleEntry, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records
 (project_id,actor_id,operation,request_key,request_hash)
 VALUES($1,$2,'role_entry.record',$3,$4) ON CONFLICT DO NOTHING RETURNING request_hash`,
		project.ID, actor.PrincipalID, meta.IdempotencyKey, hash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.RoleEntry{}, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var oldHash, body []byte
		var status int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
    FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2 AND operation='role_entry.record'
      AND request_key=$3 FOR UPDATE`,
			project.ID, actor.PrincipalID, meta.IdempotencyKey).Scan(&oldHash, &body, &status)
		if err != nil {
			return domain.RoleEntry{}, err
		}
		if !bytes.Equal(oldHash, hash[:]) {
			return domain.RoleEntry{}, application.ErrIdempotency
		}
		if status != 201 {
			return domain.RoleEntry{}, application.ErrIdempotency
		}
		var saved domain.RoleEntry
		if err := json.Unmarshal(body, &saved); err != nil {
			return domain.RoleEntry{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.RoleEntry{}, err
		}
		return saved, nil
	}
	mode, err := r.lockTaskMode(ctx, tx, project)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	item, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RoleEntry{}, application.ErrNotFound
	}
	if err != nil {
		return domain.RoleEntry{}, err
	}

	action := domain.TaskAction("")
	permission := idomain.WorkWrite
	switch input.Capacity {
	case domain.Executor:
		action = domain.TaskRecordExecutor
	case domain.Tester:
		action, permission = domain.TaskRecordTester, idomain.TestWrite
	case domain.Reviewer:
		action, permission = domain.TaskRecordReview, idomain.WorkReview
	case domain.Auditor:
		permission = idomain.AuditRead // legacy has a separate work:write gate
	}
	if err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, item, action, permission, "", false); err != nil {
		return domain.RoleEntry{}, err
	}
	// A task-scoped RoleEntry may cite only evidence truly bound to this
	// WorkItem. The historical test_case_coverage relation alone is NOT a
	// grant: one TestCase may be shared by unrelated tasks.
	if mode == domain.TaskScopedAuthorization {
		if err := r.validateTaskRoleReference(ctx, tx, project, item,
			input.ReferenceKind, input.ReferenceValue); err != nil {
			return domain.RoleEntry{}, err
		}
	}
	// Sources backed by Qual must exist within the same project. Git commit
	// references are external and only syntax-checked, not attested by Qual.
	if input.ReferenceKind == domain.TestRunReference || input.ReferenceKind == domain.ArtifactReference {
		query := `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE id=$1 AND project_id=$2)`
		if input.ReferenceKind == domain.ArtifactReference {
			query = `SELECT EXISTS(SELECT 1 FROM qual.artifacts WHERE id=$1 AND project_id=$2 AND state='ready')`
		}
		var found bool
		if err := tx.QueryRow(ctx, query, input.ReferenceValue, project.ID).Scan(&found); err != nil {
			return domain.RoleEntry{}, err
		}
		if !found {
			return domain.RoleEntry{}, application.ErrNotFound
		}
	}
	selfReview := false
	if input.Capacity == domain.Reviewer {
		selfReview = item.CreatedBy == actor.PrincipalID || (item.AssigneeID != nil && *item.AssigneeID == actor.PrincipalID)
		if !selfReview {
			// Includes immutable Claim/Reassign and task-bound test facts.
			// A transferred prior executor may not become an independent
			// reviewer by simply clearing their current assignee relation.
			var err error
			selfReview, err = wasTaskExecutor(ctx, tx, item, actor.PrincipalID)
			if err != nil {
				return domain.RoleEntry{}, err
			}
		}
	}
	now := time.Now().UTC()
	oldVersion := item.Version
	if err := item.Touch(input.ExpectedVersion, now); err != nil {
		return domain.RoleEntry{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE qual.work_items SET version=$1,updated_at=$2 WHERE id=$3 AND project_id=$4 AND version=$5`,
		item.Version, item.UpdatedAt, item.ID, project.ID, oldVersion)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.RoleEntry{}, domain.ErrConflict
	}
	entry := domain.RoleEntry{
		ID: uuid.New(), ProjectID: project.ID, WorkItemID: item.ID, ActorID: actor.PrincipalID,
		Capacity: input.Capacity, Outcome: input.Outcome, Summary: input.Summary,
		ReferenceKind: input.ReferenceKind, ReferenceValue: input.ReferenceValue,
		WorkVersion: item.Version, WorkStatus: item.Status, SelfReview: selfReview, RecordedAt: now,
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.work_role_entries
  (id,workspace_id,project_id,work_item_id,actor_id,credential_id,capacity,outcome,summary,
   reference_kind,reference_value,work_version,work_status,self_review,request_id,recorded_at)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		entry.ID, project.WorkspaceID, project.ID, item.ID, actor.PrincipalID, actor.CredentialID,
		entry.Capacity, entry.Outcome, entry.Summary, entry.ReferenceKind, entry.ReferenceValue,
		entry.WorkVersion, entry.WorkStatus, selfReview, meta.RequestID, now)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	err = r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.role_recorded", item.ID, &oldVersion, item.Version, map[string]any{
		"entry_id": entry.ID, "capacity": entry.Capacity, "outcome": entry.Outcome,
		"self_review": selfReview, "reference_kind": entry.ReferenceKind, "reference_value": entry.ReferenceValue,
		"work_version": item.Version,
	})
	if err != nil {
		return domain.RoleEntry{}, err
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=201
 WHERE project_id=$2 AND actor_id=$3 AND operation='role_entry.record' AND request_key=$4`,
		body, project.ID, actor.PrincipalID, meta.IdempotencyKey)
	if err != nil {
		return domain.RoleEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RoleEntry{}, err
	}
	return entry, nil
}

func (r Repository) ListRoles(ctx context.Context, projectID, itemID uuid.UUID) ([]domain.RoleEntry, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,project_id,work_item_id,actor_id,capacity,outcome,summary,
  reference_kind,reference_value,work_version,work_status,self_review,recorded_at
  FROM qual.work_role_entries
  WHERE project_id=$1 AND work_item_id=$2
  ORDER BY recorded_at DESC,id DESC LIMIT 100`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.RoleEntry, 0)
	for rows.Next() {
		var e domain.RoleEntry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.WorkItemID, &e.ActorID, &e.Capacity, &e.Outcome,
			&e.Summary, &e.ReferenceKind, &e.ReferenceValue, &e.WorkVersion, &e.WorkStatus, &e.SelfReview, &e.RecordedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}
