// Package postgres stores external run facts, projections and authorization
// provenance without running any worker or consulting external host processes.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qual/internal/agentrun/application"
	"qual/internal/agentrun/domain"
	identity "qual/internal/identity/application"
	workapp "qual/internal/work/application"
)

type Repository struct{ Pool *pgxpool.Pool }

type scanner interface{ Scan(...any) error }

const runSelect = `SELECT id,project_id,work_item_id,source,external_run_id,host,
    reporter_principal_id,source_status,version,report_sequence,
    last_status_occurred_at,last_seen_at,created_at,updated_at FROM qual.agent_runs`

func scanRun(row scanner) (domain.Run, error) {
	var r domain.Run
	err := row.Scan(&r.ID, &r.ProjectID, &r.WorkItemID, &r.Source, &r.ExternalRunID, &r.Host,
		&r.ReporterPrincipalID, &r.Status, &r.Version, &r.ReportSequence,
		&r.LastStatusOccurredAt, &r.LastSeenAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}
func scanReport(row scanner) (domain.Report, []byte, error) {
	var r domain.Report
	var hash []byte
	var sourceStatus *string
	err := row.Scan(&r.ID, &r.AgentRunID, &r.Sequence, &r.SourceReportID, &hash, &r.Kind,
		&sourceStatus, &r.OccurredAt, &r.ReceivedAt, &r.Applied, &r.RunVersion,
		&r.ReporterPrincipalID, &r.ErrorReason, &r.EvidenceArtifactID, &r.ExternalReferenceID)
	if sourceStatus != nil {
		r.Status = domain.Status(*sourceStatus)
	}
	return r, hash, err
}

const reportSelect = `SELECT id,agent_run_id,sequence,source_report_id,report_hash,kind,
    source_status,occurred_at,received_at,applied,run_version,reporter_principal_id,
    error_reason,evidence_artifact_id,external_reference_id FROM qual.agent_run_reports`

// The existing idempotency ledger is shared with work/test/evidence endpoints.
// INSERT ON CONFLICT is serialized by PostgreSQL; conflicting content is a 409.
func lockRequest(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project,
	op, key string, hash [32]byte) ([]byte, bool, error) {
	var inserted []byte
	err := tx.QueryRow(ctx, `INSERT INTO qual.idempotency_records
        (project_id,actor_id,operation,request_key,request_hash)
        VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING request_hash`,
		p.ID, a.PrincipalID, op, key, hash[:]).Scan(&inserted)
	if err == nil {
		return nil, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var oldHash, body []byte
	var code int
	err = tx.QueryRow(ctx, `SELECT request_hash,response_json,response_status
        FROM qual.idempotency_records
        WHERE project_id=$1 AND actor_id=$2 AND operation=$3 AND request_key=$4 FOR UPDATE`,
		p.ID, a.PrincipalID, op, key).Scan(&oldHash, &body, &code)
	if err != nil {
		return nil, false, err
	}
	if !bytes.Equal(oldHash, hash[:]) || code != 201 {
		return nil, false, application.ErrIdempotency
	}
	return body, true, nil
}
func finishRequest(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project,
	op, key string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE qual.idempotency_records
        SET response_json=$1,response_status=201
        WHERE project_id=$2 AND actor_id=$3 AND operation=$4 AND request_key=$5`,
		body, p.ID, a.PrincipalID, op, key)
	return err
}

// Emit only creation and material status reports, NEVER heartbeat, preserving
// existing transactional audit + activity + outbox invariants.
func emit(ctx context.Context, tx pgx.Tx, a identity.Actor, p identity.Project,
	requestID, operation string, entity uuid.UUID, old *int64, version int64, diff map[string]any) error {
	auditID := uuid.New()
	raw, err := json.Marshal(diff)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.audit_records
      (id,workspace_id,project_id,actor_id,credential_id,request_id,operation,entity_id,old_version,new_version,diff)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		auditID, p.WorkspaceID, p.ID, a.PrincipalID, a.CredentialID, requestID, operation, entity, old, version, raw)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.activity_records
      (id,project_id,audit_id,actor_id,operation,entity_id)
      VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, a.PrincipalID, operation, entity)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.outbox_events
      (id,project_id,audit_id,event_type,aggregate_id,payload)
      VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), p.ID, auditID, operation, entity, raw)
	return err
}

func (r Repository) Register(ctx context.Context, a identity.Actor, p identity.Project,
	in domain.Registration, meta workapp.Meta, hash [32]byte, now time.Time) (domain.Run, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	body, replay, err := lockRequest(ctx, tx, a, p, "agent_run.register", meta.IdempotencyKey, hash)
	if err != nil {
		return domain.Run{}, err
	}
	if replay {
		var saved domain.Run
		if err := json.Unmarshal(body, &saved); err != nil {
			return saved, err
		}
		return saved, tx.Commit(ctx)
	}
	// The natural source key spans all reporters and request idempotency keys.
	// Lock it before checking for an existing run, so simultaneous first
	// registrations cannot surface a PostgreSQL unique violation as HTTP 500.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`,
		p.ID.String()+":"+in.Source+":"+in.ExternalRunID); err != nil {
		return domain.Run{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items
        WHERE id=$1 AND project_id=$2)`, in.WorkItemID, p.ID).Scan(&exists); err != nil {
		return domain.Run{}, err
	}
	if !exists {
		return domain.Run{}, domain.ErrNotFound
	}
	saved, err := scanRun(tx.QueryRow(ctx, runSelect+` WHERE project_id=$1 AND source=$2 AND external_run_id=$3 FOR UPDATE`,
		p.ID, in.Source, in.ExternalRunID))
	if err == nil {
		// Cross-actor reuse of an external ID cannot assume the caller's identity.
		if saved.WorkItemID != in.WorkItemID || saved.ReporterPrincipalID != a.PrincipalID || saved.Host != in.Host {
			return domain.Run{}, domain.ErrConflict
		}
		if err := finishRequest(ctx, tx, a, p, "agent_run.register", meta.IdempotencyKey, saved); err != nil {
			return domain.Run{}, err
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, err
	}
	run := domain.Run{ID: uuid.New(), ProjectID: p.ID, WorkItemID: in.WorkItemID,
		Source: in.Source, ExternalRunID: in.ExternalRunID, Host: in.Host,
		ReporterPrincipalID: a.PrincipalID, Status: domain.Queued, Version: 1,
		LastSeenAt: &now, CreatedAt: now, UpdatedAt: now}
	_, err = tx.Exec(ctx, `INSERT INTO qual.agent_runs
      (id,workspace_id,project_id,work_item_id,source,external_run_id,host,
       reporter_principal_id,registration_credential_id,source_status,version,report_sequence,
       last_seen_at,created_at,updated_at)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,0,$11,$12,$12)`,
		run.ID, p.WorkspaceID, p.ID, in.WorkItemID, in.Source, in.ExternalRunID, in.Host,
		a.PrincipalID, a.CredentialID, run.Status, now, now)
	if err != nil {
		return domain.Run{}, err
	}
	if err := emit(ctx, tx, a, p, meta.RequestID, "agent_run.registered", run.ID, nil, 1,
		map[string]any{"work_item_id": in.WorkItemID, "source": in.Source}); err != nil {
		return domain.Run{}, err
	}
	if err := finishRequest(ctx, tx, a, p, "agent_run.register", meta.IdempotencyKey, run); err != nil {
		return domain.Run{}, err
	}
	return run, tx.Commit(ctx)
}

func (r Repository) Report(ctx context.Context, a identity.Actor, p identity.Project, runID uuid.UUID,
	in domain.ReportInput, expected int64, meta workapp.Meta, hash [32]byte, now time.Time, sampleInterval time.Duration) (domain.Receipt, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return domain.Receipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	body, replay, err := lockRequest(ctx, tx, a, p, "agent_run.report", meta.IdempotencyKey, hash)
	if err != nil {
		return domain.Receipt{}, err
	}
	if replay {
		var saved domain.Receipt
		if err := json.Unmarshal(body, &saved); err != nil {
			return saved, err
		}
		return saved, tx.Commit(ctx)
	}
	run, err := scanRun(tx.QueryRow(ctx, runSelect+` WHERE project_id=$1 AND id=$2 FOR UPDATE`, p.ID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Receipt{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Receipt{}, err
	}
	if run.ReporterPrincipalID != a.PrincipalID {
		return domain.Receipt{}, domain.ErrNotFound
	}

	reportHash := hashReport(in)
	previous, oldHash, err := scanReport(tx.QueryRow(ctx, reportSelect+
		` WHERE project_id=$1 AND agent_run_id=$2 AND source_report_id=$3`,
		p.ID, runID, in.SourceReportID))
	if err == nil {
		if !bytes.Equal(oldHash, reportHash[:]) {
			return domain.Receipt{}, domain.ErrConflict
		}
		result := domain.Receipt{Run: run, Report: previous}
		if err := finishRequest(ctx, tx, a, p, "agent_run.report", meta.IdempotencyKey, result); err != nil {
			return domain.Receipt{}, err
		}
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Receipt{}, err
	}
	if expected != run.Version {
		return domain.Receipt{}, domain.ErrConflict
	}

	if in.EvidenceArtifactID != nil {
		var found bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.artifacts WHERE id=$1 AND project_id=$2 AND state='ready')`,
			*in.EvidenceArtifactID, p.ID).Scan(&found)
		if err != nil {
			return domain.Receipt{}, err
		}
		if !found {
			return domain.Receipt{}, domain.ErrNotFound
		}
	}
	if in.ExternalReferenceID != nil {
		var found bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.external_references WHERE id=$1 AND project_id=$2)`,
			*in.ExternalReferenceID, p.ID).Scan(&found)
		if err != nil {
			return domain.Receipt{}, err
		}
		if !found {
			return domain.Receipt{}, domain.ErrNotFound
		}
	}
	oldVersion, oldSequence := run.Version, run.ReportSequence
	applied := false
	if in.Kind == domain.Heartbeat {
		var previousHeartbeat time.Time
		err = tx.QueryRow(ctx, `SELECT received_at FROM qual.agent_run_reports
            WHERE project_id=$1 AND agent_run_id=$2 AND kind='heartbeat'
            ORDER BY sequence DESC LIMIT 1`, p.ID, runID).Scan(&previousHeartbeat)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.Receipt{}, err
		}
		if err == nil && now.Sub(previousHeartbeat) < sampleInterval {
			return domain.Receipt{}, domain.ErrRateLimited
		}
	} else if run.LastStatusOccurredAt == nil || in.OccurredAt.After(*run.LastStatusOccurredAt) {
		if in.Status != run.Status {
			if !domain.CanProject(run.Status, in.Status) {
				return domain.Receipt{}, domain.ErrConflict
			}
			run.Status = in.Status
			run.Version++
			applied = true
		}
		// Re-observing the same status with a different source report ID is
		// stored as a fact, but does not manufacture a new status transition.
		occurred := in.OccurredAt.UTC()
		run.LastStatusOccurredAt = &occurred
	}
	run.ReportSequence++
	run.UpdatedAt = now
	run.LastSeenAt = &now
	report := domain.Report{ID: uuid.New(), AgentRunID: runID, Sequence: run.ReportSequence,
		SourceReportID: in.SourceReportID, Kind: in.Kind, Status: in.Status,
		OccurredAt: in.OccurredAt.UTC(), ReceivedAt: now, Applied: applied,
		RunVersion: run.Version, ReporterPrincipalID: a.PrincipalID, ErrorReason: in.ErrorReason,
		EvidenceArtifactID: in.EvidenceArtifactID, ExternalReferenceID: in.ExternalReferenceID}
	tag, err := tx.Exec(ctx, `UPDATE qual.agent_runs SET source_status=$1,version=$2,
        report_sequence=$3,last_status_occurred_at=$4,last_seen_at=$5,updated_at=$5
        WHERE project_id=$6 AND id=$7 AND version=$8 AND report_sequence=$9`,
		run.Status, run.Version, run.ReportSequence, run.LastStatusOccurredAt, now,
		p.ID, runID, oldVersion, oldSequence)
	if err != nil {
		return domain.Receipt{}, err
	}
	if tag.RowsAffected() != 1 {
		return domain.Receipt{}, domain.ErrConflict
	}
	var status any
	if in.Kind == domain.StatusChange {
		status = in.Status
	}
	_, err = tx.Exec(ctx, `INSERT INTO qual.agent_run_reports
      (id,workspace_id,project_id,agent_run_id,sequence,source_report_id,report_hash,
       kind,source_status,occurred_at,received_at,applied,run_version,
       reporter_principal_id,credential_id,error_reason,evidence_artifact_id,external_reference_id)
      VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		report.ID, p.WorkspaceID, p.ID, runID, report.Sequence, in.SourceReportID, reportHash[:],
		in.Kind, status, report.OccurredAt, now, applied, run.Version,
		a.PrincipalID, a.CredentialID, in.ErrorReason, in.EvidenceArtifactID, in.ExternalReferenceID)
	if err != nil {
		return domain.Receipt{}, err
	}
	if in.Kind == domain.StatusChange && (applied || in.ErrorReason != "" || in.EvidenceArtifactID != nil || in.ExternalReferenceID != nil) {
		if err := emit(ctx, tx, a, p, meta.RequestID, "agent_run.status_reported", run.ID, &oldVersion, run.Version,
			map[string]any{"status": in.Status, "applied": applied,
				"source_report_id": in.SourceReportID, "sequence": run.ReportSequence,
				"evidence_attached": in.EvidenceArtifactID != nil || in.ExternalReferenceID != nil}); err != nil {
			return domain.Receipt{}, err
		}
	}
	result := domain.Receipt{Run: run, Report: report}
	if err := finishRequest(ctx, tx, a, p, "agent_run.report", meta.IdempotencyKey, result); err != nil {
		return domain.Receipt{}, err
	}
	return result, tx.Commit(ctx)
}
func hashReport(input domain.ReportInput) [32]byte {
	raw, _ := json.Marshal(input)
	// Identity and version are deliberately not part of the external source
	// report fingerprint; duplicate source IDs cannot gain new content.
	return sha256Sum(raw)
}
func sha256Sum(data []byte) [32]byte { return sha256.Sum256(data) }

func (r Repository) Get(ctx context.Context, pid, id uuid.UUID) (domain.Run, error) {
	run, err := scanRun(r.Pool.QueryRow(ctx, runSelect+` WHERE project_id=$1 AND id=$2`, pid, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrNotFound
	}
	return run, err
}
func (r Repository) ListForWorkItem(ctx context.Context, pid, wid uuid.UUID, limit, offset int) ([]domain.Run, error) {
	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`, pid, wid).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, err := r.Pool.Query(ctx, runSelect+
		` WHERE project_id=$1 AND work_item_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`,
		pid, wid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]domain.Run, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, run)
	}
	return list, rows.Err()
}
func (r Repository) ListReports(ctx context.Context, pid, runID uuid.UUID, before int64, limit int) ([]domain.Report, error) {
	if _, err := r.Get(ctx, pid, runID); err != nil {
		return nil, err
	}
	rows, err := r.Pool.Query(ctx, reportSelect+
		` WHERE project_id=$1 AND agent_run_id=$2 AND ($3::bigint=0 OR sequence<$3)
        ORDER BY sequence DESC LIMIT $4`, pid, runID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Report, 0)
	for rows.Next() {
		report, _, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, report)
	}
	return items, rows.Err()
}
