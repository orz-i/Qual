package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	identity "qual/internal/identity/application"
	"qual/internal/testing/application"
	"qual/internal/testing/domain"
)

func scanCase(row interface{ Scan(...any) error }) (domain.TestCase, error) {
	var c domain.TestCase
	var draft []byte
	var published *int
	err := row.Scan(&c.ID, &c.ProjectID, &c.Key, &c.SuiteID, &draft, &c.Revision, &published, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(draft, &c.Draft); err != nil {
		return c, err
	}
	if published != nil {
		c.PublishedVersion = *published
	}
	return c, nil
}

const caseSelect = `SELECT c.id,c.project_id,c.key,c.suite_id,c.draft,c.revision,
 (SELECT v.version_no FROM qual.test_case_versions v WHERE v.id=c.published_version_id),c.updated_at
 FROM qual.test_cases c`

func (r Repository) GetCase(ctx context.Context, pid, cid uuid.UUID) (domain.TestCase, error) {
	c, err := scanCase(r.Pool.QueryRow(ctx, caseSelect+` WHERE c.project_id=$1 AND c.id=$2`, pid, cid))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	return c, err
}
func (r Repository) ListCases(ctx context.Context, pid uuid.UUID) ([]domain.TestCase, error) {
	rows, err := r.Pool.Query(ctx, caseSelect+` WHERE c.project_id=$1 ORDER BY c.key LIMIT 500`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TestCase, 0)
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r Repository) CreateSuite(ctx context.Context, actor identity.Actor, p identity.Project, name string, m application.Meta, h [32]byte) (domain.Suite, error) {
	if len(strings.TrimSpace(name)) < 1 || len(name) > 160 {
		return domain.Suite{}, domain.ErrValidation
	}
	body, err := r.idempotent(ctx, actor, p, "suite.create", m, h, func(tx pgx.Tx) (any, error) {
		var s domain.Suite
		s.ID = uuid.New()
		s.ProjectID = p.ID
		s.Name = strings.TrimSpace(name)
		err := tx.QueryRow(ctx, `INSERT INTO qual.test_suites(id,workspace_id,project_id,name,created_by)
    VALUES($1,$2,$3,$4,$5) RETURNING created_at`, s.ID, p.WorkspaceID, p.ID, s.Name, actor.PrincipalID).Scan(&s.CreatedAt)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "test_suite.created", s.ID, nil, 1, map[string]any{"name": s.Name})
		return s, err
	})
	if err != nil {
		return domain.Suite{}, err
	}
	var s domain.Suite
	err = json.Unmarshal(body, &s)
	return s, err
}
func (r Repository) ListSuites(ctx context.Context, pid uuid.UUID) ([]domain.Suite, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,project_id,name,created_at FROM qual.test_suites WHERE project_id=$1 ORDER BY name LIMIT 500`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Suite, 0)
	for rows.Next() {
		var s domain.Suite
		if err := rows.Scan(&s.ID, &s.ProjectID, &s.Name, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func checkSuite(ctx context.Context, tx pgx.Tx, pid uuid.UUID, suite *uuid.UUID) error {
	if suite == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.test_suites WHERE project_id=$1 AND id=$2)`, pid, *suite).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}
func (r Repository) CreateCase(ctx context.Context, actor identity.Actor, p identity.Project, in application.CaseInput, m application.Meta, h [32]byte) (domain.TestCase, error) {
	body, err := r.idempotent(ctx, actor, p, "case.create", m, h, func(tx pgx.Tx) (any, error) {
		if err := checkSuite(ctx, tx, p.ID, in.SuiteID); err != nil {
			return nil, err
		}
		var number int64
		if err := tx.QueryRow(ctx, `UPDATE qual.projects SET next_test_case_number=next_test_case_number+1
   WHERE id=$1 AND workspace_id=$2 RETURNING next_test_case_number-1`, p.ID, p.WorkspaceID).Scan(&number); err != nil {
			return nil, err
		}
		data, _ := json.Marshal(in.Specification)
		c := domain.TestCase{ID: uuid.New(), ProjectID: p.ID, Key: fmt.Sprintf("%s-TC-%d", p.Key, number),
			SuiteID: in.SuiteID, Draft: in.Specification, Revision: 1}
		if err := tx.QueryRow(ctx, `INSERT INTO qual.test_cases(id,workspace_id,project_id,number,key,suite_id,draft,created_by)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING updated_at`, c.ID, p.WorkspaceID, p.ID, number, c.Key, c.SuiteID, data, actor.PrincipalID).Scan(&c.UpdatedAt); err != nil {
			return nil, err
		}
		err := r.emit(ctx, tx, actor, p, m, "test_case.created", c.ID, nil, c.Revision, map[string]any{"key": c.Key})
		return c, err
	})
	if err != nil {
		return domain.TestCase{}, err
	}
	var c domain.TestCase
	err = json.Unmarshal(body, &c)
	return c, err
}
func (r Repository) UpdateCase(ctx context.Context, actor identity.Actor, p identity.Project, cid uuid.UUID, in application.EditCaseInput, m application.Meta, h [32]byte) (domain.TestCase, error) {
	body, err := r.idempotent(ctx, actor, p, "case.update", m, h, func(tx pgx.Tx) (any, error) {
		c, err := scanCase(tx.QueryRow(ctx, caseSelect+` WHERE c.project_id=$1 AND c.id=$2 FOR UPDATE OF c`, p.ID, cid))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if c.Revision != in.ExpectedRevision {
			return nil, domain.ErrConflict
		}
		if err := checkSuite(ctx, tx, p.ID, in.SuiteID); err != nil {
			return nil, err
		}
		old := c.Revision
		c.Draft = in.Specification
		c.SuiteID = in.SuiteID
		c.Revision++
		data, _ := json.Marshal(c.Draft)
		if err := tx.QueryRow(ctx, `UPDATE qual.test_cases SET draft=$1,suite_id=$2,revision=$3,updated_at=now()
    WHERE project_id=$4 AND id=$5 AND revision=$6 RETURNING updated_at`, data, c.SuiteID, c.Revision, p.ID, cid, old).Scan(&c.UpdatedAt); err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "test_case.updated", cid, &old, c.Revision, map[string]any{"revision": c.Revision})
		return c, err
	})
	if err != nil {
		return domain.TestCase{}, err
	}
	var c domain.TestCase
	err = json.Unmarshal(body, &c)
	return c, err
}
func scanVersion(row interface{ Scan(...any) error }) (domain.TestCaseVersion, error) {
	var v domain.TestCaseVersion
	var raw, hash []byte
	err := row.Scan(&v.ID, &v.TestCaseID, &v.Version, &v.SourceRevision, &raw, &hash, &v.PublishedAt)
	if err != nil {
		return v, err
	}
	v.SHA256 = hex.EncodeToString(hash)
	err = json.Unmarshal(raw, &v.Specification)
	return v, err
}
func (r Repository) CaseVersions(ctx context.Context, pid, cid uuid.UUID) ([]domain.TestCaseVersion, error) {
	rows, err := r.Pool.Query(ctx, `SELECT v.id,v.test_case_id,v.version_no,v.source_revision,v.specification,v.sha256,v.published_at
 FROM qual.test_case_versions v WHERE v.project_id=$1 AND v.test_case_id=$2 ORDER BY v.version_no DESC LIMIT 100`, pid, cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.TestCaseVersion, 0)
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r Repository) PublishCase(ctx context.Context, actor identity.Actor, p identity.Project, cid uuid.UUID, in application.PublishInput, m application.Meta, h [32]byte) (domain.TestCaseVersion, error) {
	body, err := r.idempotent(ctx, actor, p, "case.publish", m, h, func(tx pgx.Tx) (any, error) {
		c, err := scanCase(tx.QueryRow(ctx, caseSelect+` WHERE c.project_id=$1 AND c.id=$2 FOR UPDATE OF c`, p.ID, cid))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if c.Revision != in.ExpectedRevision {
			return nil, domain.ErrConflict
		}
		if err := c.Draft.Validate(); err != nil {
			return nil, err
		}
		if c.PublishedVersion > 0 {
			var sourceRevision int64
			if err := tx.QueryRow(ctx, `SELECT source_revision FROM qual.test_case_versions WHERE id=
    (SELECT published_version_id FROM qual.test_cases WHERE id=$1)`, cid).Scan(&sourceRevision); err != nil {
				return nil, err
			}
			if sourceRevision >= c.Revision {
				return nil, domain.ErrAlreadyPublished
			}
		}
		spec := c.Draft.Normalize()
		specJSON, _ := json.Marshal(spec)
		checksum := sha256.Sum256(specJSON)
		v := domain.TestCaseVersion{ID: uuid.New(), TestCaseID: cid, Version: c.PublishedVersion + 1,
			SourceRevision: c.Revision, Specification: spec, SHA256: hex.EncodeToString(checksum[:])}
		if err := tx.QueryRow(ctx, `INSERT INTO qual.test_case_versions
   (id,project_id,test_case_id,version_no,source_revision,specification,sha256,published_by)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING published_at`,
			v.ID, p.ID, cid, v.Version, v.SourceRevision, specJSON, checksum[:], actor.PrincipalID).Scan(&v.PublishedAt); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE qual.test_cases SET published_version_id=$1 WHERE id=$2 AND project_id=$3`, v.ID, cid, p.ID); err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, actor, p, m, "test_case.published", cid, nil, int64(v.Version), map[string]any{"case_version_id": v.ID, "checksum": v.SHA256})
		return v, err
	})
	if err != nil {
		return domain.TestCaseVersion{}, err
	}
	var v domain.TestCaseVersion
	err = json.Unmarshal(body, &v)
	return v, err
}
func (r Repository) LinkCoverage(ctx context.Context, actor identity.Actor, p identity.Project, cid, wid uuid.UUID, m application.Meta, h [32]byte) (domain.TestCase, error) {
	body, err := r.idempotent(ctx, actor, p, "case.coverage", m, h, func(tx pgx.Tx) (any, error) {
		c, err := scanCase(tx.QueryRow(ctx, caseSelect+` WHERE c.project_id=$1 AND c.id=$2`, p.ID, cid))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qual.work_items WHERE project_id=$1 AND id=$2)`, p.ID, wid).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			return nil, domain.ErrNotFound
		}
		tag, err := tx.Exec(ctx, `INSERT INTO qual.test_case_coverage(project_id,test_case_id,work_item_id)
    VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, p.ID, cid, wid)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, domain.ErrDuplicate
		}
		err = r.emit(ctx, tx, actor, p, m, "test_case.coverage_linked", cid, nil, c.Revision, map[string]any{"work_item_id": wid})
		return c, err
	})
	if err != nil {
		return domain.TestCase{}, err
	}
	var c domain.TestCase
	err = json.Unmarshal(body, &c)
	return c, err
}
