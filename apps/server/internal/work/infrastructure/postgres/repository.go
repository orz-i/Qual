// Package postgres implements Work application persistence with PostgreSQL.
// It owns atomic business state, audit/activity/outbox and idempotency writes.
package postgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	identity "qual/internal/identity/application"
	idomain "qual/internal/identity/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

type Repository struct {
	Pool *pgxpool.Pool
}

const itemSelect = `
 SELECT w.id,w.workspace_id,w.project_id,w.key,w.kind,w.title,w.description,
        w.acceptance_criteria,w.status,w.priority,w.assignee_id,w.accountable_owner_id,
        w.created_by,w.version,w.created_at,w.updated_at,
        b.severity,b.reproduction_steps,b.expected_behavior,b.actual_behavior,b.environment
   FROM qual.work_items w
   LEFT JOIN qual.bug_details b ON b.work_item_id=w.id
`

type rowScanner interface{ Scan(...any) error }

func scanItem(row rowScanner) (domain.WorkItem, error) {
	var w domain.WorkItem
	var kind, status, priority string
	var criteria []byte
	var assignee, owner pgtype.UUID
	var severity, repro, expected, actual, environment *string
	err := row.Scan(&w.ID, &w.WorkspaceID, &w.ProjectID, &w.Key, &kind, &w.Title, &w.Description,
		&criteria, &status, &priority, &assignee, &owner, &w.CreatedBy, &w.Version, &w.CreatedAt, &w.UpdatedAt,
		&severity, &repro, &expected, &actual, &environment)
	if err != nil {
		return domain.WorkItem{}, err
	}
	w.Kind = domain.Kind(kind)
	w.Status = domain.Status(status)
	w.Priority = domain.Priority(priority)
	if err := json.Unmarshal(criteria, &w.AcceptanceCriteria); err != nil {
		return domain.WorkItem{}, err
	}
	if assignee.Valid {
		id, err := uuid.FromBytes(assignee.Bytes[:])
		if err != nil {
			return domain.WorkItem{}, err
		}
		w.AssigneeID = &id
	}
	if owner.Valid {
		id, err := uuid.FromBytes(owner.Bytes[:])
		if err != nil {
			return domain.WorkItem{}, err
		}
		w.AccountableOwnerID = &id
	}
	if severity != nil {
		w.BugDetails = &domain.BugDetails{Severity: *severity}
		if repro != nil {
			w.BugDetails.Reproduction = *repro
		}
		if expected != nil {
			w.BugDetails.ExpectedBehavior = *expected
		}
		if actual != nil {
			w.BugDetails.ActualBehavior = *actual
		}
		if environment != nil {
			w.BugDetails.Environment = *environment
		}
	}
	return w, nil
}

func (r Repository) Get(ctx context.Context, projectID, itemID uuid.UUID) (domain.WorkItem, error) {
	item, err := scanItem(r.Pool.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2`, projectID, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkItem{}, application.ErrNotFound
	}
	return item, err
}

type cursorData struct {
	ProjectID uuid.UUID     `json:"project_id"`
	Status    domain.Status `json:"status"`
	Kind      domain.Kind   `json:"kind"`
	CreatedAt time.Time     `json:"created_at"`
	ID        uuid.UUID     `json:"id"`
}

func parseCursor(value string, projectID uuid.UUID, f application.Filter) (*cursorData, error) {
	if value == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(b) > 1024 {
		return nil, domain.ErrValidation
	}
	var c cursorData
	if err := json.Unmarshal(b, &c); err != nil || c.ProjectID != projectID || c.Kind != f.Kind || c.Status != f.Status || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return nil, domain.ErrValidation
	}
	return &c, nil
}
func makeCursor(c cursorData) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (r Repository) List(ctx context.Context, projectID uuid.UUID, filter application.Filter) (application.Page, error) {
	cursor, err := parseCursor(filter.Cursor, projectID, filter)
	if err != nil {
		return application.Page{}, err
	}
	var date any
	var afterID any
	if cursor != nil {
		date = cursor.CreatedAt
		afterID = cursor.ID
	}
	rows, err := r.Pool.Query(ctx, itemSelect+`
	 WHERE w.project_id=$1
	   AND ($2::text='' OR w.status=$2::text)
	   AND ($3::text='' OR w.kind=$3::text)
	   AND ($4::timestamptz IS NULL OR (w.created_at,w.id)<($4::timestamptz,$5::uuid))
	 ORDER BY w.created_at DESC,w.id DESC
	 LIMIT $6
	`, projectID, string(filter.Status), string(filter.Kind), date, afterID, filter.Limit+1)
	if err != nil {
		return application.Page{}, err
	}
	defer rows.Close()
	page := application.Page{Items: make([]domain.WorkItem, 0, filter.Limit)}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return application.Page{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return application.Page{}, err
	}
	if len(page.Items) > filter.Limit {
		page.Items = page.Items[:filter.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = makeCursor(cursorData{ProjectID: projectID, Status: filter.Status, Kind: filter.Kind, CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (r Repository) Create(ctx context.Context, actor identity.Actor, project identity.Project, draft domain.Draft, meta application.Meta, hash [32]byte) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, "create", meta, hash, func(tx pgx.Tx) (domain.WorkItem, error) {
		var num int64
		err := tx.QueryRow(ctx, `UPDATE qual.projects SET next_work_item_number=next_work_item_number+1
			WHERE id=$1 AND workspace_id=$2 RETURNING next_work_item_number-1`, project.ID, actor.WorkspaceID).Scan(&num)
		if err != nil {
			return domain.WorkItem{}, err
		}
		var owner *uuid.UUID
		if actor.Type == idomain.Human {
			id := actor.PrincipalID
			owner = &id
		}
		w, err := domain.New(uuid.New(), project.WorkspaceID, project.ID, actor.PrincipalID,
			fmt.Sprintf("%s-%d", project.Key, num), draft, owner, time.Now())
		if err != nil {
			return domain.WorkItem{}, err
		}
		criteria, err := json.Marshal(w.AcceptanceCriteria)
		if err != nil {
			return domain.WorkItem{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO qual.work_items
			(id,workspace_id,project_id,number,key,kind,title,description,acceptance_criteria,
			status,priority,accountable_owner_id,created_by,version,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			w.ID, w.WorkspaceID, w.ProjectID, num, w.Key, w.Kind, w.Title, w.Description, criteria,
			w.Status, w.Priority, w.AccountableOwnerID, w.CreatedBy, w.Version, w.CreatedAt, w.UpdatedAt)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if w.Kind == domain.Bug {
			b := w.BugDetails
			_, err = tx.Exec(ctx, `INSERT INTO qual.bug_details
				(work_item_id,severity,reproduction_steps,expected_behavior,actual_behavior,environment)
				VALUES($1,$2,$3,$4,$5,$6)`, w.ID, b.Severity, b.Reproduction, b.ExpectedBehavior, b.ActualBehavior, b.Environment)
			if err != nil {
				return domain.WorkItem{}, err
			}
		}
		err = r.emit(ctx, tx, actor, project, meta.RequestID, "work_item.created", w.ID, nil, w.Version,
			map[string]any{"kind": w.Kind, "status": w.Status, "version": w.Version})
		return w, err
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var item domain.WorkItem
	err = json.Unmarshal(data, &item)
	return item, err
}

func (r Repository) Mutate(ctx context.Context, actor identity.Actor, project identity.Project, itemID uuid.UUID, mutation application.Mutation) (domain.WorkItem, error) {
	data, err := r.idempotent(ctx, actor, project, mutation.Operation, mutation.Meta, mutation.RequestHash, func(tx pgx.Tx) (domain.WorkItem, error) {
		w, err := scanItem(tx.QueryRow(ctx, itemSelect+` WHERE w.project_id=$1 AND w.id=$2 FOR UPDATE OF w`, project.ID, itemID))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkItem{}, application.ErrNotFound
		}
		if err != nil {
			return domain.WorkItem{}, err
		}
		oldVersion, oldStatus := w.Version, w.Status
		if mutation.LegacyDoneAttempt {
			// The legacy transition endpoint cannot bypass the acceptance
			// gate once Goose13 is installed. Goose12 pilot history retains
			// its explicitly documented legacy behavior until rollout.
			enabled, err := acceptanceSchemaEnabled(ctx, tx)
			if err != nil {
				return domain.WorkItem{}, err
			}
			if enabled {
				return domain.WorkItem{}, domain.ErrDoneGate
			}
		}
		if err := mutation.Apply(&w); err != nil {
			return domain.WorkItem{}, err
		}
		criteria, err := json.Marshal(w.AcceptanceCriteria)
		if err != nil {
			return domain.WorkItem{}, err
		}
		tag, err := tx.Exec(ctx, `UPDATE qual.work_items SET title=$1,description=$2,acceptance_criteria=$3,
			priority=$4,status=$5,assignee_id=$6,version=$7,updated_at=$8
			WHERE id=$9 AND project_id=$10 AND version=$11`,
			w.Title, w.Description, criteria, w.Priority, w.Status, w.AssigneeID, w.Version, w.UpdatedAt, w.ID, w.ProjectID, oldVersion)
		if err != nil {
			return domain.WorkItem{}, err
		}
		if tag.RowsAffected() != 1 {
			return domain.WorkItem{}, domain.ErrConflict
		}
		err = r.emit(ctx, tx, actor, project, mutation.Meta.RequestID, "work_item."+mutation.Operation, w.ID, &oldVersion, w.Version,
			map[string]any{"old_status": oldStatus, "status": w.Status, "version": w.Version})
		return w, err
	})
	if err != nil {
		return domain.WorkItem{}, err
	}
	var item domain.WorkItem
	err = json.Unmarshal(data, &item)
	return item, err
}

type idempotentWork func(pgx.Tx) (domain.WorkItem, error)

// idempotent always opens a transaction. A conflicting INSERT blocks until
// the first writer commits, then returns its stored response or a 409 conflict.
func (r Repository) idempotent(ctx context.Context, actor identity.Actor, project identity.Project,
	op string, meta application.Meta, requestHash [32]byte, perform idempotentWork) ([]byte, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inserted []byte
	err = tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records(project_id,actor_id,operation,request_key,request_hash)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		project.ID, actor.PrincipalID, op, meta.IdempotencyKey, requestHash[:]).Scan(&inserted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, body []byte
		var status int
		err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
			FROM qual.idempotency_records
			WHERE project_id=$1 AND actor_id=$2 AND operation=$3 AND request_key=$4 FOR UPDATE`,
			project.ID, actor.PrincipalID, op, meta.IdempotencyKey).Scan(&storedHash, &body, &status)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(storedHash, requestHash[:]) {
			return nil, application.ErrIdempotency
		}
		if status == 0 {
			return nil, errors.New("incomplete idempotency record")
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return body, nil
	}
	work, err := perform(tx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(work)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records
		SET response_json=$1,response_status=200
		WHERE project_id=$2 AND actor_id=$3 AND operation=$4 AND request_key=$5`,
		body, project.ID, actor.PrincipalID, op, meta.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return body, nil
}

// emit is called only in the same PostgreSQL transaction as work state writes.
// No external process is invoked. The outbox is dispatched in a later phase.
func (r Repository) emit(ctx context.Context, tx pgx.Tx, actor identity.Actor, project identity.Project,
	requestID, operation string, entityID uuid.UUID, oldVersion *int64, newVersion int64, diff map[string]any) error {
	auditID := uuid.New()
	detail, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.audit_records
		(id,workspace_id,project_id,actor_id,credential_id,request_id,operation,entity_id,old_version,new_version,diff)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		auditID, project.WorkspaceID, project.ID, actor.PrincipalID, actor.CredentialID,
		requestID, operation, entityID, oldVersion, newVersion, detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records
		(id,project_id,audit_id,actor_id,operation,entity_id) VALUES($1,$2,$3,$4,$5,$6)`,
		uuid.New(), project.ID, auditID, actor.PrincipalID, operation, entityID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events
		(id,project_id,audit_id,event_type,aggregate_id,payload) VALUES($1,$2,$3,$4,$5,$6)`,
		uuid.New(), project.ID, auditID, operation, entityID, detail)
	return err
}
