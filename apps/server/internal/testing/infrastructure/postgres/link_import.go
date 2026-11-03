package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	identity "qual/internal/identity/application"
	"qual/internal/testing/application"
	"qual/internal/testing/domain"
	workdomain "qual/internal/work/domain"
)

func (r Repository) LinkDefect(ctx context.Context, actor identity.Actor, p identity.Project,
	attemptID uuid.UUID, in domain.BugLinkInput, m application.Meta, h [32]byte) (domain.BugLink, error) {
	body, err := r.idempotent(ctx, actor, p, "attempt.defect", m, h, func(tx pgx.Tx) (any, error) {
		var status domain.ResultStatus
		err := tx.QueryRow(ctx, `SELECT status FROM qual.test_attempts WHERE project_id=$1 AND id=$2`, p.ID, attemptID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if status != domain.Failed {
			return nil, domain.ErrNotFailed
		}
		link := domain.BugLink{AttemptID: attemptID}
		if in.BugID != nil {
			err := tx.QueryRow(ctx, `SELECT id,key FROM qual.work_items
    WHERE project_id=$1 AND id=$2 AND kind='bug'`, p.ID, *in.BugID).Scan(&link.BugID, &link.BugKey)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			if err != nil {
				return nil, err
			}
		} else {
			severity := in.Severity
			if severity == "" {
				severity = "major"
			}
			var number int64
			if err := tx.QueryRow(ctx, `UPDATE qual.projects SET next_work_item_number=next_work_item_number+1
       WHERE id=$1 AND workspace_id=$2 RETURNING next_work_item_number-1`, p.ID, p.WorkspaceID).Scan(&number); err != nil {
				return nil, err
			}
			var owner *uuid.UUID
			if actor.Type == "human" {
				id := actor.PrincipalID
				owner = &id
			}
			w, err := workdomain.New(uuid.New(), p.WorkspaceID, p.ID, actor.PrincipalID,
				fmt.Sprintf("%s-%d", p.Key, number),
				workdomain.Draft{Kind: workdomain.Bug, Title: in.Title, Priority: workdomain.High,
					BugDetails: &workdomain.BugDetails{Severity: severity, Reproduction: in.ReproductionSteps}},
				owner, time.Now())
			if err != nil {
				return nil, err
			}
			criteria, _ := json.Marshal(w.AcceptanceCriteria)
			_, err = tx.Exec(ctx, `INSERT INTO qual.work_items(id,workspace_id,project_id,number,key,kind,title,description,
        acceptance_criteria,status,priority,accountable_owner_id,created_by,version,created_at,updated_at)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
				w.ID, w.WorkspaceID, w.ProjectID, number, w.Key, w.Kind, w.Title, w.Description,
				criteria, w.Status, w.Priority, w.AccountableOwnerID, w.CreatedBy, w.Version, w.CreatedAt, w.UpdatedAt)
			if err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO qual.bug_details(work_item_id,severity,reproduction_steps)
     VALUES($1,$2,$3)`, w.ID, severity, in.ReproductionSteps)
			if err != nil {
				return nil, err
			}
			if err := r.emit(ctx, tx, actor, p, m, "work_item.created", w.ID, nil, 1,
				map[string]any{"kind": "bug", "source_attempt_id": attemptID}); err != nil {
				return nil, err
			}
			link.BugID = w.ID
			link.BugKey = w.Key
			link.Created = true
		}
		tag, err := tx.Exec(ctx, `INSERT INTO qual.attempt_defects(project_id,attempt_id,bug_work_item_id,linked_by)
      VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, p.ID, attemptID, link.BugID, actor.PrincipalID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, domain.ErrDuplicate
		}
		err = r.emit(ctx, tx, actor, p, m, "test_attempt.defect_linked", attemptID, nil, 1,
			map[string]any{"bug_id": link.BugID, "bug_key": link.BugKey, "created": link.Created})
		return link, err
	})
	if err != nil {
		return domain.BugLink{}, err
	}
	var link domain.BugLink
	err = json.Unmarshal(body, &link)
	return link, err
}

func (r Repository) ImportJUnit(ctx context.Context, actor identity.Actor, p identity.Project,
	rid uuid.UUID, in application.JUnitInput, cases []domain.ReportCase, m application.Meta, h [32]byte) (domain.ImportSummary, error) {
	body, err := r.idempotent(ctx, actor, p, "junit.import", m, h, func(tx pgx.Tx) (any, error) {
		if filepath.Base(in.Filename) != in.Filename || strings.ContainsAny(in.Filename, "\\/") ||
			strings.TrimSpace(in.Filename) == "" || !strings.HasSuffix(strings.ToLower(in.Filename), ".xml") {
			return nil, domain.ErrValidation
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_runs WHERE project_id=$1 AND id=$2)`, p.ID, rid).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, domain.ErrNotFound
		}
		hash := sha256.Sum256(in.Raw)
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_report_imports WHERE run_id=$1 AND report_sha256=$2)`, rid, hash[:]).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, domain.ErrDuplicate
		}
		rows, err := tx.Query(ctx, `SELECT i.id,c.key FROM qual.test_run_items i
    JOIN qual.test_cases c ON c.id=i.test_case_id
    WHERE i.project_id=$1 AND i.run_id=$2`, p.ID, rid)
		if err != nil {
			return nil, err
		}
		items := make(map[string]uuid.UUID)
		for rows.Next() {
			var id uuid.UUID
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				rows.Close()
				return nil, err
			}
			items[key] = id
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, c := range cases {
			if _, ok := items[c.CaseKey]; !ok {
				return nil, domain.ErrNotFound
			}
		}
		summary := domain.ImportSummary{ID: uuid.New(), RunID: rid, Filename: in.Filename,
			SHA256: hex.EncodeToString(hash[:]), ImportedCount: len(cases)}
		// XML is only a reported result. ObservedAt is the ingestion clock unless
		// an authoritative timestamp was explicitly provided by the reporter.
		for _, c := range cases {
			a, err := r.insertAttempt(ctx, tx, actor, p, items[c.CaseKey], domain.AttemptInput{
				Status: c.Status, Note: c.Note, SourceKind: "junit",
			})
			if err != nil {
				return nil, err
			}
			switch a.Status {
			case domain.Passed:
				summary.Passed++
			case domain.Failed:
				summary.Failed++
			case domain.Skipped:
				summary.Skipped++
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO qual.test_report_imports
    (id,project_id,run_id,reported_by,filename,report_sha256,raw_xml,case_count)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			summary.ID, p.ID, rid, actor.PrincipalID, in.Filename, hash[:], in.Raw, len(cases))
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "test_report.imported", summary.ID, nil, 1,
			map[string]any{"run_id": rid, "sha256": summary.SHA256, "imported_count": summary.ImportedCount,
				"passed": summary.Passed, "failed": summary.Failed, "skipped": summary.Skipped})
		return summary, err
	})
	if err != nil {
		return domain.ImportSummary{}, err
	}
	var summary domain.ImportSummary
	err = json.Unmarshal(body, &summary)
	return summary, err
}
