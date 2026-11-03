package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qual/internal/evidence/application"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
)

type Repository struct {
	Pool    *pgxpool.Pool
	Objects application.ObjectStore
}
type operation func(pgx.Tx) (any, error)

func (r Repository) idempotent(ctx context.Context, a identity.Actor, p identity.Project, op string, m application.Meta, h [32]byte, fn operation) ([]byte, error) {
	return r.idempotentWithOptions(ctx, a, p, op, m, h, pgx.TxOptions{}, fn)
}
func (r Repository) idempotentWithOptions(ctx context.Context, a identity.Actor, p identity.Project, op string, m application.Meta, h [32]byte, options pgx.TxOptions, fn operation) ([]byte, error) {
	tx, err := r.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		p.ID, a.PrincipalID, op, m.IdempotencyKey, h[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored, body []byte
		var status int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
   FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2 AND operation=$3 AND request_key=$4 FOR UPDATE`,
			p.ID, a.PrincipalID, op, m.IdempotencyKey).Scan(&stored, &body, &status)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(stored, h[:]) {
			return nil, application.ErrIdempotency
		}
		if status != 200 && status != 201 {
			return nil, errors.New("incomplete idempotency record")
		}
		return body, tx.Commit(ctx)
	}
	item, err := fn(tx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=200
  WHERE project_id=$2 AND actor_id=$3 AND operation=$4 AND request_key=$5`,
		body, p.ID, a.PrincipalID, op, m.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	return body, tx.Commit(ctx)
}
func (r Repository) emit(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project, m application.Meta, operation string,
	id uuid.UUID, oldVersion *int64, newVersion int64, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	auditID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO qual.audit_records(id,workspace_id,project_id,actor_id,credential_id,request_id,
 operation,entity_id,old_version,new_version,diff) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		auditID, p.WorkspaceID, p.ID, a.PrincipalID, a.CredentialID, m.RequestID, operation, id, oldVersion, newVersion, raw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records(id,project_id,audit_id,actor_id,operation,entity_id)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, a.PrincipalID, operation, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events(id,project_id,audit_id,event_type,aggregate_id,payload)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, operation, id, raw)
	return err
}
func targetExists(ctx context.Context, tx pgx.Tx, pid uuid.UUID, kind domain.TargetKind, id uuid.UUID) error {
	var query string
	switch kind {
	case domain.WorkItem:
		query = `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`
	case domain.TestCase:
		query = `SELECT EXISTS(SELECT 1 FROM qual.test_cases WHERE project_id=$1 AND id=$2)`
	case domain.TestRun:
		query = `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE project_id=$1 AND id=$2)`
	case domain.TestAttempt:
		query = `SELECT EXISTS(SELECT 1 FROM qual.test_attempts WHERE project_id=$1 AND id=$2)`
	default:
		return domain.ErrValidation
	}
	var exists bool
	if err := tx.QueryRow(ctx, query, pid, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}
