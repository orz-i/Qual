package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"qual/internal/evidence/application"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
)

func lockThreadWorkItem(ctx context.Context, tx pgx.Tx, projectID, itemID uuid.UUID, expected int64) (int64, error) {
	var version int64
	err := tx.QueryRow(ctx, `SELECT version FROM qual.work_items WHERE project_id=$1 AND id=$2 FOR UPDATE`,
		projectID, itemID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if version != expected {
		return 0, domain.ErrConflict
	}
	return version, nil
}

func advanceThreadWorkVersion(ctx context.Context, tx pgx.Tx, projectID, itemID uuid.UUID, oldVersion int64) (int64, error) {
	var next int64
	err := tx.QueryRow(ctx, `UPDATE qual.work_items SET version=version+1,updated_at=now()
 WHERE project_id=$1 AND id=$2 AND version=$3 RETURNING version`, projectID, itemID, oldVersion).Scan(&next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrConflict
	}
	return next, err
}

func legacyThreadSource(ctx context.Context, tx pgx.Tx, projectID, itemID uuid.UUID, in domain.ThreadLinkInput) (string, error) {
	sourceURL := in.URL
	if in.SourceReferenceID == nil {
		return sourceURL, nil
	}
	var provider, kind, externalID, legacyURL, targetKind string
	var targetID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT provider,kind,external_id,url,target_kind,target_id
 FROM qual.external_references WHERE project_id=$1 AND id=$2`, projectID, *in.SourceReferenceID).
		Scan(&provider, &kind, &externalID, &legacyURL, &targetKind, &targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if targetKind != "work_item" || targetID != itemID || provider != in.Provider ||
		kind != in.ThreadKind || externalID != in.ExternalThreadID ||
		(sourceURL != "" && sourceURL != legacyURL) || !domain.ValidThreadURL(legacyURL) {
		return "", domain.ErrConflict
	}
	return legacyURL, nil
}

func threadByIdentity(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, in domain.ThreadLinkInput) (domain.Thread, error) {
	var t domain.Thread
	err := tx.QueryRow(ctx, `SELECT id,project_id,provider,thread_kind,external_thread_id,
 COALESCE(url,''),created_by,created_at FROM qual.external_threads
 WHERE project_id=$1 AND provider=$2 AND thread_kind=$3 AND external_thread_id=$4`,
		projectID, in.Provider, in.ThreadKind, in.ExternalThreadID).
		Scan(&t.ID, &t.ProjectID, &t.Provider, &t.ThreadKind, &t.ExternalThreadID, &t.URL, &t.CreatedBy, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	return t, err
}
func threadByID(ctx context.Context, tx pgx.Tx, projectID, threadID uuid.UUID) (domain.Thread, error) {
	var t domain.Thread
	err := tx.QueryRow(ctx, `SELECT id,project_id,provider,thread_kind,external_thread_id,
 COALESCE(url,''),created_by,created_at FROM qual.external_threads WHERE project_id=$1 AND id=$2`,
		projectID, threadID).
		Scan(&t.ID, &t.ProjectID, &t.Provider, &t.ThreadKind, &t.ExternalThreadID, &t.URL, &t.CreatedBy, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, domain.ErrNotFound
	}
	return t, err
}
func threadWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return domain.ErrConflict
		case "23503":
			return domain.ErrNotFound
		}
	}
	return err
}

func (r Repository) LinkThread(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID uuid.UUID, in domain.ThreadLinkInput, meta application.Meta, hash [32]byte) (domain.ThreadMutation, error) {
	body, err := r.idempotent(ctx, actor, project, "work_thread.link", meta, hash, func(tx pgx.Tx) (any, error) {
		prior, err := lockThreadWorkItem(ctx, tx, project.ID, itemID, in.ExpectedVersion)
		if err != nil {
			return nil, err
		}
		// The optional old reference is checked under the same project before any new writes.
		sourceURL, err := legacyThreadSource(ctx, tx, project.ID, itemID, in)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO qual.external_threads
 (id,workspace_id,project_id,provider,thread_kind,external_thread_id,url,created_by)
 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8)
 ON CONFLICT(project_id,provider,thread_kind,external_thread_id) DO NOTHING`,
			uuid.New(), project.WorkspaceID, project.ID, in.Provider, in.ThreadKind, in.ExternalThreadID, sourceURL, actor.PrincipalID)
		if err != nil {
			return nil, threadWriteError(err)
		}
		thread, err := threadByIdentity(ctx, tx, project.ID, in)
		if err != nil {
			return nil, err
		}
		if sourceURL != "" && thread.URL != sourceURL {
			// Same project+source identity must never be silently rebound to another URL.
			return nil, domain.ErrConflict
		}
		link := domain.WorkThreadLink{
			ID: uuid.New(), ProjectID: project.ID, ThreadID: thread.ID,
			WorkItemID: itemID, SourceReferenceID: in.SourceReferenceID, CreatedBy: actor.PrincipalID,
		}
		err = tx.QueryRow(ctx, `INSERT INTO qual.work_thread_links
 (id,workspace_id,project_id,thread_id,work_item_id,source_reference_id,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
			link.ID, project.WorkspaceID, project.ID, thread.ID, itemID, link.SourceReferenceID, actor.PrincipalID).Scan(&link.CreatedAt)
		if err != nil {
			return nil, threadWriteError(err)
		}
		next, err := advanceThreadWorkVersion(ctx, tx, project.ID, itemID, prior)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, project, meta, "work_thread.linked", itemID, &prior, next,
			map[string]any{"thread_id": thread.ID, "link_id": link.ID, "source_reference_id": link.SourceReferenceID})
		return domain.ThreadMutation{WorkThreadEntry: domain.WorkThreadEntry{Thread: thread, Link: link}, WorkItemVersion: next}, err
	})
	if err != nil {
		return domain.ThreadMutation{}, err
	}
	var out domain.ThreadMutation
	err = json.Unmarshal(body, &out)
	return out, err
}

func (r Repository) UnlinkThread(ctx context.Context, actor identity.Actor, project identity.Project,
	itemID, threadID uuid.UUID, expected int64, meta application.Meta, hash [32]byte) (domain.ThreadMutation, error) {
	body, err := r.idempotent(ctx, actor, project, "work_thread.unlink", meta, hash, func(tx pgx.Tx) (any, error) {
		prior, err := lockThreadWorkItem(ctx, tx, project.ID, itemID, expected)
		if err != nil {
			return nil, err
		}
		thread, err := threadByID(ctx, tx, project.ID, threadID)
		if err != nil {
			return nil, err
		}
		var link domain.WorkThreadLink
		err = tx.QueryRow(ctx, `SELECT id,project_id,thread_id,work_item_id,source_reference_id,created_by,created_at
 FROM qual.work_thread_links
 WHERE project_id=$1 AND work_item_id=$2 AND thread_id=$3 AND removed_at IS NULL
 FOR UPDATE`, project.ID, itemID, threadID).
			Scan(&link.ID, &link.ProjectID, &link.ThreadID, &link.WorkItemID, &link.SourceReferenceID, &link.CreatedBy, &link.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		removedBy := actor.PrincipalID
		link.RemovedBy = &removedBy
		err = tx.QueryRow(ctx, `UPDATE qual.work_thread_links
 SET removed_by=$2,removed_at=now() WHERE id=$1 AND removed_at IS NULL RETURNING removed_at`,
			link.ID, removedBy).Scan(&link.RemovedAt)
		if err != nil {
			return nil, err
		}
		next, err := advanceThreadWorkVersion(ctx, tx, project.ID, itemID, prior)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, project, meta, "work_thread.unlinked", itemID, &prior, next,
			map[string]any{"thread_id": threadID, "link_id": link.ID})
		return domain.ThreadMutation{WorkThreadEntry: domain.WorkThreadEntry{Thread: thread, Link: link}, WorkItemVersion: next}, err
	})
	if err != nil {
		return domain.ThreadMutation{}, err
	}
	var out domain.ThreadMutation
	err = json.Unmarshal(body, &out)
	return out, err
}

func (r Repository) ListWorkThreads(ctx context.Context, projectID, itemID uuid.UUID, limit int, cursor uuid.UUID) (domain.WorkThreadPage, error) {
	out := domain.WorkThreadPage{Items: []domain.WorkThreadEntry{}}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := targetExists(ctx, tx, projectID, domain.WorkItem, itemID); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.project_id,t.provider,t.thread_kind,t.external_thread_id,
 COALESCE(t.url,''),t.created_by,t.created_at,
 l.id,l.project_id,l.thread_id,l.work_item_id,l.source_reference_id,l.created_by,l.created_at
 FROM qual.work_thread_links l JOIN qual.external_threads t
 ON t.project_id=l.project_id AND t.id=l.thread_id
 WHERE l.project_id=$1 AND l.work_item_id=$2 AND l.removed_at IS NULL AND l.id>$3
 ORDER BY l.id LIMIT $4`, projectID, itemID, cursor, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var entry domain.WorkThreadEntry
		err := rows.Scan(&entry.Thread.ID, &entry.Thread.ProjectID, &entry.Thread.Provider,
			&entry.Thread.ThreadKind, &entry.Thread.ExternalThreadID, &entry.Thread.URL,
			&entry.Thread.CreatedBy, &entry.Thread.CreatedAt,
			&entry.Link.ID, &entry.Link.ProjectID, &entry.Link.ThreadID, &entry.Link.WorkItemID,
			&entry.Link.SourceReferenceID, &entry.Link.CreatedBy, &entry.Link.CreatedAt)
		if err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Items) == limit {
			last := out.Items[len(out.Items)-1].Link.ID
			out.NextCursor = &last
			break
		}
		out.Items = append(out.Items, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (r Repository) ListThreadWorkItems(ctx context.Context, projectID, threadID uuid.UUID, limit int, cursor uuid.UUID) (domain.ThreadWorkItemPage, error) {
	out := domain.ThreadWorkItemPage{Items: []domain.ThreadWorkItemEntry{}}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := threadByID(ctx, tx, projectID, threadID); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT l.id,l.project_id,l.thread_id,l.work_item_id,l.source_reference_id,l.created_by,l.created_at,
 w.id,w.key,w.status,w.version
 FROM qual.work_thread_links l JOIN qual.work_items w
 ON w.project_id=l.project_id AND w.id=l.work_item_id
 WHERE l.project_id=$1 AND l.thread_id=$2 AND l.removed_at IS NULL AND l.id>$3
 ORDER BY l.id LIMIT $4`, projectID, threadID, cursor, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var entry domain.ThreadWorkItemEntry
		err := rows.Scan(&entry.Link.ID, &entry.Link.ProjectID, &entry.Link.ThreadID,
			&entry.Link.WorkItemID, &entry.Link.SourceReferenceID, &entry.Link.CreatedBy, &entry.Link.CreatedAt,
			&entry.WorkItem.ID, &entry.WorkItem.Key, &entry.WorkItem.Status, &entry.WorkItem.Version)
		if err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Items) == limit {
			last := out.Items[len(out.Items)-1].Link.ID
			out.NextCursor = &last
			break
		}
		out.Items = append(out.Items, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
