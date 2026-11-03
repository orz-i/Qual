package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "qual/internal/identity/application"
	"qual/internal/testing/application"
)

type Repository struct{ Pool *pgxpool.Pool }
type mutation func(pgx.Tx) (any, error)

// idempotent serializes the scoped command and the business transaction,
// audit, activity and outbox. A retry receives the same response, not a
// second TestAttempt or TestCaseVersion.
func (r Repository) idempotent(ctx context.Context, actor identity.Actor, p identity.Project,
	op string, m application.Meta, hash [32]byte, fn mutation) ([]byte, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
    VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		p.ID, actor.PrincipalID, op, m.IdempotencyKey, hash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var stored, body []byte
		var status int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
      FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2
        AND operation=$3 AND request_key=$4 FOR UPDATE`,
			p.ID, actor.PrincipalID, op, m.IdempotencyKey).Scan(&stored, &body, &status)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(stored, hash[:]) {
			return nil, application.ErrIdempotency
		}
		if status != 200 && status != 201 {
			return nil, errors.New("incomplete idempotency record")
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return body, nil
	}
	result, err := fn(tx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE qual.idempotency_records
     SET response_json=$1,response_status=200 WHERE project_id=$2 AND actor_id=$3
       AND operation=$4 AND request_key=$5`, body, p.ID, actor.PrincipalID, op, m.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, errors.New("failed to persist idempotent response")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return body, nil
}
func (r Repository) emit(ctx context.Context, tx pgx.Tx, actor identity.Actor, p identity.Project, m application.Meta,
	op string, id uuid.UUID, oldVersion *int64, newVersion int64, detail map[string]any) error {
	auditID := uuid.New()
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.audit_records(id,workspace_id,project_id,actor_id,
 credential_id,request_id,operation,entity_id,old_version,new_version,diff)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, auditID, p.WorkspaceID, p.ID,
		actor.PrincipalID, actor.CredentialID, m.RequestID, op, id, oldVersion, newVersion, raw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records(id,project_id,audit_id,actor_id,operation,entity_id)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, actor.PrincipalID, op, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events(id,project_id,audit_id,event_type,aggregate_id,payload)
 VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, op, id, raw)
	return err
}
