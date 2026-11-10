package postgres

import (
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

func updateTouched(ctx context.Context, tx pgx.Tx, w domain.WorkItem, oldVersion int64) error {
	tag, err := tx.Exec(ctx, `UPDATE qual.work_items SET version=$1,updated_at=$2
		WHERE id=$3 AND project_id=$4 AND version=$5`,
		w.Version, w.UpdatedAt, w.ID, w.ProjectID, oldVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (r Repository) AddComment(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID uuid.UUID, expected int64, body string, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "comment", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		mode, err := r.lockTaskMode(ctx, tx, project)
		if err != nil {
			return domain.WorkItem{}, err
		}
		w, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		if err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, w, domain.TaskComment, idomain.WorkWrite, "", false); err != nil {
			return domain.WorkItem{}, err
		}
		if err := domain.ValidateComment(body); err != nil {
			return domain.WorkItem{}, err
		}
		oldVersion := w.Version
		if err := w.Touch(expected, time.Now()); err != nil {
			return domain.WorkItem{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO qual.work_comments
			(id,workspace_id,project_id,work_item_id,author_id,body)
			VALUES($1,$2,$3,$4,$5,$6)`,
			uuid.New(), project.WorkspaceID, project.ID, w.ID, actor.PrincipalID, body)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if err := updateTouched(ctx, tx, w, oldVersion); err != nil {
			return domain.WorkItem{}, err
		}
		err = r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.commented", w.ID, &oldVersion, w.Version,
			map[string]any{"action": "comment_added", "version": w.Version})
		return w, err
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var result domain.WorkItem
	err = json.Unmarshal(data, &result)
	return result, err
}

func (r Repository) LinkDependency(ctx context.Context, actor identity.Actor, project identity.Project,
	fromID, toID uuid.UUID, expected int64, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "link_dependency", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		mode, err := r.lockTaskMode(ctx, tx, project)
		if err != nil {
			return domain.WorkItem{}, err
		}
		// Serialize dependency graph mutations within the project to prevent
		// two concurrent reverse links from both passing the cycle check.
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::uuid::text,0))`, project.ID)
		if err != nil {
			return domain.WorkItem{}, err
		}
		w, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, fromID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		if err := r.authorizeTaskWrite(ctx, tx, actor, project, mode, w, domain.TaskUpdate, idomain.WorkWrite, "", false); err != nil {
			return domain.WorkItem{}, err
		}
		oldVersion := w.Version
		if err := w.Touch(expected, time.Now()); err != nil {
			return domain.WorkItem{}, err
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`, project.ID, toID).Scan(&exists)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if !exists {
			return domain.WorkItem{}, application.ErrNotFound
		}
		var cycles bool
		err = tx.QueryRow(ctx, `
			WITH RECURSIVE reach(id) AS (
				SELECT to_item_id FROM qual.work_dependencies
				  WHERE project_id=$1 AND from_item_id=$2
				UNION
				SELECT d.to_item_id FROM qual.work_dependencies d
				JOIN reach r ON r.id=d.from_item_id
				WHERE d.project_id=$1
			)
			SELECT EXISTS(SELECT 1 FROM reach WHERE id=$3)
		`, project.ID, toID, fromID).Scan(&cycles)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if cycles {
			return domain.WorkItem{}, domain.ErrDependency
		}
		tag, err := tx.Exec(ctx, `INSERT INTO qual.work_dependencies(project_id,from_item_id,to_item_id)
			VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, project.ID, fromID, toID)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if tag.RowsAffected() != 1 {
			return domain.WorkItem{}, domain.ErrDependency
		}
		if err := updateTouched(ctx, tx, w, oldVersion); err != nil {
			return domain.WorkItem{}, err
		}
		err = r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.dependency_linked", w.ID, &oldVersion, w.Version,
			map[string]any{"to_item_id": toID, "version": w.Version})
		return w, err
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var result domain.WorkItem
	err = json.Unmarshal(data, &result)
	return result, err
}

func (r Repository) Comments(ctx context.Context, projectID, itemID uuid.UUID) ([]domain.Comment, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,work_item_id,author_id,body,created_at
		FROM qual.work_comments WHERE project_id=$1 AND work_item_id=$2
		ORDER BY created_at DESC,id DESC LIMIT 100`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Comment, 0)
	for rows.Next() {
		var item domain.Comment
		if err := rows.Scan(&item.ID, &item.WorkItemID, &item.AuthorID, &item.Body, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r Repository) Dependencies(ctx context.Context, projectID, itemID uuid.UUID) ([]domain.Dependency, error) {
	rows, err := r.Pool.Query(ctx, `SELECT from_item_id,to_item_id,created_at
		FROM qual.work_dependencies WHERE project_id=$1 AND from_item_id=$2
		ORDER BY created_at DESC,to_item_id LIMIT 100`, projectID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Dependency, 0)
	for rows.Next() {
		var item domain.Dependency
		if err := rows.Scan(&item.FromItemID, &item.ToItemID, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
