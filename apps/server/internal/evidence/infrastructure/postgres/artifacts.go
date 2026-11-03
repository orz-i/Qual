package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"qual/internal/evidence/application"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
)

const artifactColumns = `id,project_id,target_kind,target_id,filename,media_type,size_bytes,
 encode(sha256,'hex'),state,created_by,created_at,uploaded_at,ready_at`

func scanArtifact(row interface{ Scan(...any) error }) (domain.Artifact, error) {
	var a domain.Artifact
	err := row.Scan(&a.ID, &a.ProjectID, &a.TargetKind, &a.TargetID, &a.Filename, &a.MediaType, &a.SizeBytes,
		&a.SHA256, &a.State, &a.CreatedBy, &a.CreatedAt, &a.UploadedAt, &a.ReadyAt)
	return a, err
}
func (r Repository) GetArtifact(ctx context.Context, pid, id uuid.UUID) (domain.Artifact, string, error) {
	var key string
	// Get details and object key in one query while keeping key private.
	row := r.Pool.QueryRow(ctx, `SELECT `+artifactColumns+`,object_key FROM qual.artifacts
 WHERE project_id=$1 AND id=$2`, pid, id)
	var a domain.Artifact
	err := row.Scan(&a.ID, &a.ProjectID, &a.TargetKind, &a.TargetID, &a.Filename, &a.MediaType, &a.SizeBytes,
		&a.SHA256, &a.State, &a.CreatedBy, &a.CreatedAt, &a.UploadedAt, &a.ReadyAt, &key)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, "", domain.ErrNotFound
	}
	return a, key, err
}
func (r Repository) ListArtifacts(ctx context.Context, pid uuid.UUID, kind domain.TargetKind, target uuid.UUID) ([]domain.Artifact, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+artifactColumns+` FROM qual.artifacts
 WHERE project_id=$1 AND target_kind=$2 AND target_id=$3 ORDER BY created_at DESC,id DESC LIMIT 100`, pid, kind, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Artifact, 0)
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r Repository) Begin(ctx context.Context, actor identity.Actor, p identity.Project,
	in domain.BeginInput, m application.Meta, h [32]byte) (domain.Artifact, error) {
	raw, err := r.idempotent(ctx, actor, p, "artifact.begin", m, h, func(tx pgx.Tx) (any, error) {
		if err := targetExists(ctx, tx, p.ID, in.TargetKind, in.TargetID); err != nil {
			return nil, err
		}
		sum, err := hex.DecodeString(in.SHA256)
		if err != nil {
			return nil, domain.ErrValidation
		}
		item := domain.Artifact{ID: uuid.New(), ProjectID: p.ID, TargetKind: in.TargetKind, TargetID: in.TargetID,
			Filename: in.Filename, MediaType: in.MediaType, SizeBytes: in.SizeBytes, SHA256: in.SHA256,
			State: "pending", CreatedBy: actor.PrincipalID}
		key := "projects/" + p.ID.String() + "/artifacts/" + item.ID.String()
		err = tx.QueryRow(ctx, `INSERT INTO qual.artifacts
    (id,workspace_id,project_id,target_kind,target_id,object_key,filename,media_type,size_bytes,sha256,created_by)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`,
			item.ID, p.WorkspaceID, p.ID, item.TargetKind, item.TargetID, key, item.Filename, item.MediaType, item.SizeBytes, sum, actor.PrincipalID).
			Scan(&item.CreatedAt)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "artifact.pending", item.ID, nil, 1, map[string]any{
			"target_kind": item.TargetKind, "target_id": item.TargetID, "size_bytes": item.SizeBytes, "sha256": item.SHA256,
		})
		return item, err
	})
	if err != nil {
		return domain.Artifact{}, err
	}
	var item domain.Artifact
	err = json.Unmarshal(raw, &item)
	return item, err
}
func (r Repository) MarkUploaded(ctx context.Context, actor identity.Actor, p identity.Project,
	id uuid.UUID, m application.Meta, h [32]byte, data []byte) (domain.Artifact, error) {
	if r.Objects == nil {
		return domain.Artifact{}, domain.ErrUnavailable
	}
	var uploadedKey string
	raw, err := r.idempotent(ctx, actor, p, "artifact.upload", m, h, func(tx pgx.Tx) (any, error) {
		var key string
		var item domain.Artifact
		err := tx.QueryRow(ctx, `SELECT `+artifactColumns+`,object_key FROM qual.artifacts
   WHERE project_id=$1 AND id=$2 FOR UPDATE`, p.ID, id).Scan(
			&item.ID, &item.ProjectID, &item.TargetKind, &item.TargetID, &item.Filename, &item.MediaType,
			&item.SizeBytes, &item.SHA256, &item.State, &item.CreatedBy, &item.CreatedAt, &item.UploadedAt, &item.ReadyAt, &key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if item.CreatedBy != actor.PrincipalID {
			return nil, identity.ErrForbidden
		}
		if item.State != "pending" {
			return nil, domain.ErrConflict
		}
		if err := domain.Verify(data, item.SizeBytes, item.SHA256); err != nil {
			return nil, err
		}
		if err := r.Objects.Put(ctx, key, item.MediaType, data); err != nil {
			return nil, domain.ErrUnavailable
		}
		uploadedKey = key
		now := time.Now().UTC()
		item.State = "uploaded"
		item.UploadedAt = &now
		_, err = tx.Exec(ctx, `UPDATE qual.artifacts SET state='uploaded',uploaded_at=$1 WHERE project_id=$2 AND id=$3`, now, p.ID, id)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "artifact.uploaded", id, nil, 2, map[string]any{"size_bytes": item.SizeBytes, "sha256": item.SHA256})
		return item, err
	})
	if err != nil && uploadedKey != "" {
		_ = r.Objects.Delete(ctx, uploadedKey)
	}
	if err != nil {
		return domain.Artifact{}, err
	}
	var item domain.Artifact
	err = json.Unmarshal(raw, &item)
	return item, err
}
func (r Repository) MarkReady(ctx context.Context, actor identity.Actor, p identity.Project,
	id uuid.UUID, m application.Meta, h [32]byte, _ []byte) (domain.Artifact, error) {
	if r.Objects == nil {
		return domain.Artifact{}, domain.ErrUnavailable
	}
	raw, err := r.idempotent(ctx, actor, p, "artifact.complete", m, h, func(tx pgx.Tx) (any, error) {
		var item domain.Artifact
		var key string
		err := tx.QueryRow(ctx, `SELECT `+artifactColumns+`,object_key FROM qual.artifacts
  WHERE project_id=$1 AND id=$2 FOR UPDATE`, p.ID, id).Scan(
			&item.ID, &item.ProjectID, &item.TargetKind, &item.TargetID, &item.Filename, &item.MediaType,
			&item.SizeBytes, &item.SHA256, &item.State, &item.CreatedBy, &item.CreatedAt, &item.UploadedAt, &item.ReadyAt, &key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if item.CreatedBy != actor.PrincipalID {
			return nil, identity.ErrForbidden
		}
		if item.State != "uploaded" {
			return nil, domain.ErrConflict
		}
		body, err := r.Objects.Get(ctx, key, domain.MaxArtifactSize)
		if err != nil {
			return nil, domain.ErrUnavailable
		}
		if err := domain.Verify(body, item.SizeBytes, item.SHA256); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		item.State = "ready"
		item.ReadyAt = &now
		_, err = tx.Exec(ctx, `UPDATE qual.artifacts SET state='ready',ready_at=$1
    WHERE project_id=$2 AND id=$3`, now, p.ID, id)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "artifact.ready", id, nil, 3, map[string]any{
			"target_kind": item.TargetKind, "target_id": item.TargetID, "sha256": item.SHA256,
		})
		return item, err
	})
	if err != nil {
		return domain.Artifact{}, err
	}
	var item domain.Artifact
	err = json.Unmarshal(raw, &item)
	return item, err
}

// CleanupStale explicitly removes abandoned pending OR uploaded bytes.
// The row is locked before deleting S3, preventing concurrent Complete/Upload
// from transitioning the same object to ready while a cleanup runs.
func (r Repository) CleanupStale(ctx context.Context, store application.ObjectStore, age time.Duration) (int, error) {
	if store == nil || age < time.Hour {
		return 0, domain.ErrValidation
	}
	total := 0
	for total < 100 {
		tx, err := r.Pool.Begin(ctx)
		if err != nil {
			return total, err
		}
		var id, projectID uuid.UUID
		var key, state string
		err = tx.QueryRow(ctx, `SELECT id,project_id,object_key,state
    FROM qual.artifacts
    WHERE state IN ('pending','uploaded')
      AND COALESCE(uploaded_at,created_at)<now()-($1::double precision*interval '1 second')
    ORDER BY COALESCE(uploaded_at,created_at),id
    FOR UPDATE SKIP LOCKED LIMIT 1`, age.Seconds()).Scan(&id, &projectID, &key, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return total, nil
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return total, err
		}
		if err := store.Delete(ctx, key); err != nil {
			_ = tx.Rollback(ctx)
			return total, domain.ErrUnavailable
		}
		_, err = tx.Exec(ctx, `UPDATE qual.artifacts SET state='failed',failure_reason=$1
    WHERE id=$2 AND state=$3`, "expired_"+state, id, state)
		if err != nil {
			_ = tx.Rollback(ctx)
			return total, err
		}
		action := "expired_" + state + "_cleanup"
		_, err = tx.Exec(ctx, `INSERT INTO qual.artifact_maintenance_events
    (id,project_id,artifact_id,action,detail) VALUES($1,$2,$3,$4,'stale S3 object cleaned')`,
			uuid.New(), projectID, id, action)
		if err != nil {
			_ = tx.Rollback(ctx)
			return total, err
		}
		if err := tx.Commit(ctx); err != nil {
			return total, err
		}
		total++
	}
	return total, nil
}
