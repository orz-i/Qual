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

func (r Repository) AddReference(ctx context.Context, a identity.Actor, p identity.Project, in domain.ReferenceInput, m application.Meta, h [32]byte) (domain.Reference, error) {
	body, err := r.idempotent(ctx, a, p, "external_reference.create", m, h, func(tx pgx.Tx) (any, error) {
		if err := r.scopedReferenceWrite(ctx, tx, a, p, in.TargetKind, in.TargetID); err != nil {
			return nil, err
		}
		if err := targetExists(ctx, tx, p.ID, in.TargetKind, in.TargetID); err != nil {
			return nil, err
		}
		ref := domain.Reference{ID: uuid.New(), ProjectID: p.ID, ReferenceInput: in, CreatedBy: a.PrincipalID}
		err := tx.QueryRow(ctx, `INSERT INTO qual.external_references
   (id,project_id,target_kind,target_id,provider,kind,external_id,url,created_by)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
			ref.ID, p.ID, in.TargetKind, in.TargetID, in.Provider, in.Kind, in.ExternalID, in.URL, a.PrincipalID).Scan(&ref.CreatedAt)
		if err != nil {
			var pgerr *pgconn.PgError
			if errors.As(err, &pgerr) && pgerr.Code == "23505" {
				return nil, domain.ErrConflict
			}
			return nil, err
		}
		err = r.emit(ctx, tx, a, p, m, "external_reference.created", ref.ID, nil, 1, map[string]any{
			"target_kind": in.TargetKind, "target_id": in.TargetID, "provider": in.Provider, "kind": in.Kind, "external_id": in.ExternalID,
		})
		return ref, err
	})
	if err != nil {
		return domain.Reference{}, err
	}
	var ref domain.Reference
	err = json.Unmarshal(body, &ref)
	return ref, err
}
func (r Repository) ListReferences(ctx context.Context, pid uuid.UUID, kind domain.TargetKind, target uuid.UUID) ([]domain.Reference, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := targetExists(ctx, tx, pid, kind, target); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,project_id,target_kind,target_id,provider,kind,external_id,url,created_by,created_at
 FROM qual.external_references WHERE project_id=$1 AND target_kind=$2 AND target_id=$3
 ORDER BY created_at DESC,id DESC LIMIT 100`, pid, kind, target)
	if err != nil {
		return nil, err
	}
	out := []domain.Reference{}
	for rows.Next() {
		var ref domain.Reference
		if err := rows.Scan(&ref.ID, &ref.ProjectID, &ref.TargetKind, &ref.TargetID, &ref.Provider, &ref.Kind,
			&ref.ExternalID, &ref.URL, &ref.CreatedBy, &ref.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
