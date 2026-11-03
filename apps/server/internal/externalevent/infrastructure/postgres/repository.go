// Package postgres appends externally reported Event facts and read-only timelines.
// All queries are project-filtered; no raw payload/secret enters PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qual/internal/externalevent/domain"
	identity "qual/internal/identity/application"
	work "qual/internal/work/application"
)

type Repository struct{ Pool *pgxpool.Pool }

const selectEvent = `SELECT id,project_id,work_item_id,thread_id,source,external_event_id,
event_type,occurred_at,received_at,payload_sha256,summary,reporter_principal_id FROM qual.external_events`

func scanEvent(row interface{ Scan(...any) error }) (domain.Event, error) {
	var e domain.Event
	var digest []byte
	err := row.Scan(&e.ID, &e.ProjectID, &e.WorkItemID, &e.ThreadID,
		&e.Source, &e.ExternalEventID, &e.EventType, &e.OccurredAt, &e.ReceivedAt,
		&digest, &e.Summary, &e.ReporterPrincipalID)
	if err != nil {
		return e, err
	}
	e.PayloadSHA256 = hex.EncodeToString(digest)
	return e, nil
}

func lockRequest(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project, meta work.Meta, hash [32]byte) ([]byte, bool, error) {
	const op = "external_event.record"
	var inserted []byte
	err := tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records
       (project_id,actor_id,operation,request_key,request_hash)
       VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		p.ID, a.PrincipalID, op, meta.IdempotencyKey, hash[:]).Scan(&inserted)
	if err == nil {
		return nil, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var old, body []byte
	var status int
	if err := tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
       FROM qual.idempotency_records WHERE project_id=$1 AND actor_id=$2
       AND operation=$3 AND request_key=$4 FOR UPDATE`, p.ID, a.PrincipalID, op, meta.IdempotencyKey).Scan(&old, &body, &status); err != nil {
		return nil, false, err
	}
	if !bytes.Equal(hash[:], old) || status != 201 {
		return nil, false, domain.ErrIdempotency
	}
	return body, true, nil
}

func finishRequest(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project, meta work.Meta, e domain.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records SET response_json=$1,response_status=201
       WHERE project_id=$2 AND actor_id=$3 AND operation='external_event.record' AND request_key=$4`,
		body, p.ID, a.PrincipalID, meta.IdempotencyKey)
	return err
}

func emit(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project, requestID string, e domain.Event) error {
	// No summary/payload bytes in the audit, Activity or Outbox.
	diff, _ := json.Marshal(map[string]any{
		"event_id": e.ID, "source": e.Source, "external_event_id": e.ExternalEventID,
		"event_type": e.EventType, "work_item_id": e.WorkItemID, "thread_id": e.ThreadID,
		"payload_sha256": e.PayloadSHA256,
	})
	audit := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO qual.audit_records
        (id,workspace_id,project_id,actor_id,credential_id,request_id,operation,entity_id,old_version,new_version,diff)
        VALUES($1,$2,$3,$4,$5,$6,'external_event.recorded',$7,NULL,1,$8)`,
		audit, p.WorkspaceID, p.ID, a.PrincipalID, a.CredentialID, requestID, e.ID, diff); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.activity_records
        (id,project_id,audit_id,actor_id,operation,entity_id)
        VALUES($1,$2,$3,$4,'external_event.recorded',$5)`,
		uuid.New(), p.ID, audit, a.PrincipalID, e.ID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO qual.outbox_events
        (id,project_id,audit_id,event_type,aggregate_id,payload)
        VALUES($1,$2,$3,'external_event.recorded',$4,$5)`,
		uuid.New(), p.ID, audit, e.ID, diff)
	return err
}

func (r Repository) Record(ctx context.Context, a identity.Actor, p identity.Project, in domain.Input, meta work.Meta, hash [32]byte, now time.Time) (domain.Event, error) {
	var zero domain.Event
	if r.Pool == nil {
		return zero, errors.New("missing Event store")
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	body, replay, err := lockRequest(ctx, tx, a, p, meta, hash)
	if err != nil {
		return zero, err
	}
	if replay {
		var saved domain.Event
		if err := json.Unmarshal(body, &saved); err != nil {
			return zero, err
		}
		return saved, tx.Commit(ctx)
	}
	// Serialize first registrations across HTTP clients and Idempotency-Keys.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`,
		p.ID.String()+":"+in.Source+":"+in.ExternalEventID); err != nil {
		return zero, err
	}
	// Read a persisted natural identity before checking current link activity:
	// delayed retries of an immutable event must remain safely idempotent even
	// after the WorkItem-Thread association is explicitly unlinked.
	saved, err := scanEvent(tx.QueryRow(ctx, selectEvent+` WHERE project_id=$1 AND source=$2 AND external_event_id=$3`,
		p.ID, in.Source, in.ExternalEventID))
	if err == nil {
		if saved.WorkItemID != in.WorkItemID || saved.ThreadID != in.ThreadID ||
			saved.EventType != in.EventType || saved.PayloadSHA256 != in.PayloadSHA256 ||
			saved.Summary != in.Summary || !saved.OccurredAt.Equal(in.OccurredAt) ||
			saved.ReporterPrincipalID != a.PrincipalID {
			return zero, domain.ErrConflict
		}
		if err := finishRequest(ctx, tx, a, p, meta, saved); err != nil {
			return zero, err
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	// Only NEW events require an active, project-scoped WorkItem-Thread
	// association with a matching provider. Existing events remain readable.
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
        SELECT 1 FROM qual.work_thread_links l
        JOIN qual.external_threads t ON t.project_id=l.project_id AND t.id=l.thread_id
        JOIN qual.work_items w ON w.project_id=l.project_id AND w.id=l.work_item_id
        WHERE l.project_id=$1 AND l.work_item_id=$2 AND l.thread_id=$3
          AND l.removed_at IS NULL AND t.provider=$4
          FOR SHARE OF l
        )`, p.ID, in.WorkItemID, in.ThreadID, in.Source).Scan(&valid)
	if err != nil {
		return zero, err
	}
	if !valid {
		return zero, domain.ErrNotFound
	}
	digest, _ := hex.DecodeString(in.PayloadSHA256)
	saved = domain.Event{ID: uuid.New(), ProjectID: p.ID, WorkItemID: in.WorkItemID,
		ThreadID: in.ThreadID, Source: in.Source, ExternalEventID: in.ExternalEventID,
		EventType: in.EventType, OccurredAt: in.OccurredAt.UTC(), ReceivedAt: now.UTC(),
		PayloadSHA256: in.PayloadSHA256, Summary: in.Summary, ReporterPrincipalID: a.PrincipalID}
	if _, err := tx.Exec(ctx, `INSERT INTO qual.external_events
      (id,workspace_id,project_id,work_item_id,thread_id,source,external_event_id,event_type,
       occurred_at,received_at,payload_sha256,summary,reporter_principal_id,registration_credential_id)
       VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		saved.ID, p.WorkspaceID, p.ID, in.WorkItemID, in.ThreadID, in.Source, in.ExternalEventID,
		in.EventType, in.OccurredAt, saved.ReceivedAt, digest, in.Summary, a.PrincipalID, a.CredentialID); err != nil {
		return zero, err
	}
	if err := emit(ctx, tx, a, p, meta.RequestID, saved); err != nil {
		return zero, err
	}
	if err := finishRequest(ctx, tx, a, p, meta, saved); err != nil {
		return zero, err
	}
	return saved, tx.Commit(ctx)
}

func (r Repository) List(ctx context.Context, pid, target uuid.UUID, byThread bool, limit int, cursor uuid.UUID) (domain.Page, error) {
	var result = domain.Page{Items: []domain.Event{}}
	if r.Pool == nil {
		return result, errors.New("missing Event store")
	}
	col, table := "work_item_id", "qual.work_items"
	if byThread {
		col, table = "thread_id", "qual.external_threads"
	}
	var exists bool
	// Only hardcoded column/table choices; never user-provided SQL.
	err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE project_id=$1 AND id=$2)`, pid, target).Scan(&exists)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, domain.ErrNotFound
	}
	var before *time.Time
	if cursor != uuid.Nil {
		var at time.Time
		err = r.Pool.QueryRow(ctx, `SELECT received_at FROM qual.external_events
           WHERE project_id=$1 AND `+col+`=$2 AND id=$3`, pid, target, cursor).Scan(&at)
		if errors.Is(err, pgx.ErrNoRows) {
			return result, domain.ErrNotFound
		}
		if err != nil {
			return result, err
		}
		before = &at
	}
	rows, err := r.Pool.Query(ctx, selectEvent+` WHERE project_id=$1 AND `+col+`=$2
          AND ($3::timestamptz IS NULL OR (received_at,id)<($3::timestamptz,$4::uuid))
          ORDER BY received_at DESC,id DESC LIMIT $5`, pid, target, before, cursor, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanEvent(rows)
		if err != nil {
			return result, err
		}
		if len(result.Items) == limit {
			result.HasMore = true
			break
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if result.HasMore && len(result.Items) > 0 {
		result.NextCursor = result.Items[len(result.Items)-1].ID.String()
	}
	return result, nil
}
