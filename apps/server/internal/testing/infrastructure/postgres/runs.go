package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	identity "qual/internal/identity/application"
	"qual/internal/testing/application"
	"qual/internal/testing/domain"
	workdomain "qual/internal/work/domain"
)

func (r Repository) GetRun(ctx context.Context, pid, rid uuid.UUID) (domain.TestRun, error) {
	var run domain.TestRun
	bound, err := r.RunWork(ctx, pid, rid)
	if err != nil {
		return run, err
	}
	run.WorkItemID = bound
	err = r.Pool.QueryRow(ctx, `SELECT id,project_id,name,source_system,COALESCE(external_run_key,''),
 environment,build_ref,created_at FROM qual.test_runs WHERE project_id=$1 AND id=$2`, pid, rid).
		Scan(&run.ID, &run.ProjectID, &run.Name, &run.SourceSystem, &run.ExternalRunKey, &run.Environment, &run.BuildRef, &run.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TestRun{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TestRun{}, err
	}
	rows, err := r.Pool.Query(ctx, `SELECT i.id,i.test_case_id,c.key,i.test_case_version_id,v.version_no,i.snapshot_sha256,
       (SELECT a.status FROM qual.test_attempts a WHERE a.run_item_id=i.id ORDER BY attempt_no DESC LIMIT 1),
       i.next_attempt_no-1
 FROM qual.test_run_items i
 JOIN qual.test_cases c ON c.id=i.test_case_id
 JOIN qual.test_case_versions v ON v.id=i.test_case_version_id
 WHERE i.project_id=$1 AND i.run_id=$2 ORDER BY c.key`, pid, rid)
	if err != nil {
		return domain.TestRun{}, err
	}
	defer rows.Close()
	run.Items = make([]domain.RunItem, 0)
	for rows.Next() {
		var item domain.RunItem
		var hash []byte
		var latest *string
		if err := rows.Scan(&item.ID, &item.TestCaseID, &item.TestCaseKey, &item.CaseVersionID, &item.CaseVersion,
			&hash, &latest, &item.AttemptCount); err != nil {
			return domain.TestRun{}, err
		}
		item.SnapshotSHA256 = hex.EncodeToString(hash)
		if latest != nil {
			s := domain.ResultStatus(*latest)
			item.LatestStatus = &s
		}
		run.Items = append(run.Items, item)
	}
	return run, rows.Err()
}
func (r Repository) ListRuns(ctx context.Context, pid uuid.UUID) ([]domain.TestRun, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id FROM qual.test_runs WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, pid)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]domain.TestRun, 0, len(ids))
	for _, id := range ids {
		run, err := r.GetRun(ctx, pid, id)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}
func (r Repository) CreateRun(ctx context.Context, actor identity.Actor, p identity.Project, in domain.RunInput, m application.Meta, h [32]byte) (domain.TestRun, error) {
	body, err := r.idempotent(ctx, actor, p, "run.create", m, h, func(tx pgx.Tx) (any, error) {
		if err := r.taskRunGuard(ctx, tx, actor, p, in.WorkItemID, workdomain.TaskReportTest); err != nil {
			return nil, err
		}
		boundSchema, err := r.boundRunColumn(ctx, tx)
		if err != nil {
			return nil, err
		}
		if in.WorkItemID != nil && !boundSchema {
			return nil, domain.ErrRunBindingUnavailable
		}
		if in.ExternalRunKey != "" {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_runs
    WHERE project_id=$1 AND source_principal_id=$2 AND source_system=$3 AND external_run_key=$4)`,
				p.ID, actor.PrincipalID, in.SourceSystem, in.ExternalRunKey).Scan(&exists); err != nil {
				return nil, err
			}
			if exists {
				return nil, domain.ErrDuplicate
			}
		}
		run := domain.TestRun{ID: uuid.New(), ProjectID: p.ID, Name: strings.TrimSpace(in.Name),
			SourceSystem: in.SourceSystem, ExternalRunKey: in.ExternalRunKey, Environment: in.Environment, BuildRef: in.BuildRef,
			WorkItemID: in.WorkItemID, Items: make([]domain.RunItem, 0)}
		if boundSchema {
			err = tx.QueryRow(ctx, `INSERT INTO qual.test_runs(id,project_id,name,source_principal_id,source_system,external_run_key,environment,build_ref,work_item_id)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
				run.ID, p.ID, run.Name, actor.PrincipalID, run.SourceSystem, nilIfEmpty(run.ExternalRunKey), run.Environment, run.BuildRef, in.WorkItemID).Scan(&run.CreatedAt)
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO qual.test_runs(id,project_id,name,source_principal_id,source_system,external_run_key,environment,build_ref)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
				run.ID, p.ID, run.Name, actor.PrincipalID, run.SourceSystem, nilIfEmpty(run.ExternalRunKey), run.Environment, run.BuildRef).Scan(&run.CreatedAt)
		}
		if err != nil {
			return nil, err
		}
		seen := make(map[uuid.UUID]bool)
		for _, vid := range in.CaseVersionIDs {
			var cID uuid.UUID
			var key string
			var version int
			var checksum []byte
			err := tx.QueryRow(ctx, `SELECT v.test_case_id,c.key,v.version_no,v.sha256
      FROM qual.test_case_versions v JOIN qual.test_cases c ON c.id=v.test_case_id
      WHERE v.project_id=$1 AND v.id=$2`, p.ID, vid).Scan(&cID, &key, &version, &checksum)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, domain.ErrNotPublished
			}
			if err != nil {
				return nil, err
			}
			if seen[cID] {
				return nil, domain.ErrValidation
			}
			if in.WorkItemID != nil {
				if err := r.validateRunCoverage(ctx, tx, actor, p, m, *in.WorkItemID, cID); err != nil {
					return nil, err
				}
			}
			seen[cID] = true
			item := domain.RunItem{ID: uuid.New(), TestCaseID: cID, TestCaseKey: key, CaseVersionID: vid, CaseVersion: version,
				SnapshotSHA256: hex.EncodeToString(checksum), AttemptCount: 0}
			_, err = tx.Exec(ctx, `INSERT INTO qual.test_run_items(id,project_id,run_id,test_case_id,test_case_version_id,snapshot_sha256)
      VALUES($1,$2,$3,$4,$5,$6)`, item.ID, p.ID, run.ID, cID, vid, checksum)
			if err != nil {
				return nil, err
			}
			run.Items = append(run.Items, item)
		}
		err = r.emit(ctx, tx, actor, p, m, "test_run.created", run.ID, nil, 1, map[string]any{"case_versions": len(run.Items), "source": in.SourceSystem})
		return run, err
	})
	if err != nil {
		return domain.TestRun{}, err
	}
	var run domain.TestRun
	err = json.Unmarshal(body, &run)
	return run, err
}
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func scanAttempt(row interface{ Scan(...any) error }) (domain.Attempt, error) {
	var a domain.Attempt
	err := row.Scan(&a.ID, &a.RunItemID, &a.AttemptNo, &a.Status, &a.Note, &a.ObservedAt, &a.IngestedAt,
		&a.ReportedBy, &a.SourceKind, &a.ExternalAttemptKey)
	return a, err
}
func (r Repository) Attempts(ctx context.Context, pid, itemID uuid.UUID) ([]domain.Attempt, error) {
	var exists bool
	err := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_run_items WHERE project_id=$1 AND id=$2)`, pid, itemID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, err := r.Pool.Query(ctx, `SELECT id,run_item_id,attempt_no,status,note,observed_at,ingested_at,
 reported_by,source_kind,COALESCE(external_attempt_key,'')
 FROM qual.test_attempts WHERE project_id=$1 AND run_item_id=$2 ORDER BY attempt_no DESC LIMIT 500`, pid, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Attempt, 0)
	for rows.Next() {
		a, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r Repository) insertAttempt(ctx context.Context, tx pgx.Tx, actor identity.Actor, p identity.Project,
	itemID uuid.UUID, in domain.AttemptInput) (domain.Attempt, error) {
	var attemptNo int
	err := tx.QueryRow(ctx, `UPDATE qual.test_run_items SET next_attempt_no=next_attempt_no+1
   WHERE id=$1 AND project_id=$2 RETURNING next_attempt_no-1`, itemID, p.ID).Scan(&attemptNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Attempt{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Attempt{}, err
	}
	if in.ExternalAttemptKey != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_attempts
    WHERE run_item_id=$1 AND external_attempt_key=$2)`, itemID, in.ExternalAttemptKey).Scan(&exists); err != nil {
			return domain.Attempt{}, err
		}
		if exists {
			return domain.Attempt{}, domain.ErrDuplicate
		}
	}
	a := domain.Attempt{ID: uuid.New(), RunItemID: itemID, AttemptNo: attemptNo, Status: in.Status, Note: in.Note,
		ReportedBy: actor.PrincipalID, SourceKind: in.SourceKind, ExternalAttemptKey: in.ExternalAttemptKey}
	if in.ObservedAt != nil {
		a.ObservedAt = in.ObservedAt.UTC()
	} else {
		a.ObservedAt = time.Now().UTC()
	}
	err = tx.QueryRow(ctx, `INSERT INTO qual.test_attempts(id,project_id,run_item_id,attempt_no,status,note,
   observed_at,reported_by,source_kind,external_attempt_key)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING ingested_at`,
		a.ID, p.ID, itemID, a.AttemptNo, a.Status, a.Note, a.ObservedAt, a.ReportedBy, a.SourceKind, nilIfEmpty(in.ExternalAttemptKey)).Scan(&a.IngestedAt)
	return a, err
}
func (r Repository) RecordAttempt(ctx context.Context, actor identity.Actor, p identity.Project,
	itemID uuid.UUID, in domain.AttemptInput, m application.Meta, h [32]byte) (domain.Attempt, error) {
	body, err := r.idempotent(ctx, actor, p, "attempt.record", m, h, func(tx pgx.Tx) (any, error) {
		workID, err := r.boundRunItemInTx(ctx, tx, p.ID, itemID)
		if err != nil {
			return nil, err
		}
		if err := r.taskRunGuard(ctx, tx, actor, p, workID, workdomain.TaskReportTest); err != nil {
			return nil, err
		}
		a, err := r.insertAttempt(ctx, tx, actor, p, itemID, in)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "test_attempt.recorded", a.ID, nil, int64(a.AttemptNo),
			map[string]any{"run_item_id": a.RunItemID, "status": a.Status, "source": a.SourceKind, "attempt_no": a.AttemptNo})
		return a, err
	})
	if err != nil {
		return domain.Attempt{}, err
	}
	var a domain.Attempt
	err = json.Unmarshal(body, &a)
	return a, err
}
