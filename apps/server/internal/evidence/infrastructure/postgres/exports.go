package postgres

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"qual/internal/evidence/application"
	"qual/internal/evidence/domain"
	identity "qual/internal/identity/application"
)

const MaxArchiveBytes = 32 * 1024 * 1024
const MaxRowsPerArchiveTable = 1000

// Bound ZIP entry counts even when a Run has years of source reports.
// Heartbeats are never silently truncated or deleted.
const MaxArchiveFiles = 1024

type archiveFile struct {
	Name string
	Data []byte
	Rows int
}
type snapshotSource struct {
	Name  string
	Query string
}

// Every query has a constant name and stable order. Never interpolate user SQL.
var snapshotSources = []snapshotSource{
	{"project.jsonl", `SELECT to_jsonb(p) FROM qual.projects p WHERE id=$1 ORDER BY id`},
	{"work_items.jsonl", `SELECT to_jsonb(t) FROM qual.work_items t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"bug_details.jsonl", `SELECT to_jsonb(b) FROM qual.bug_details b JOIN qual.work_items w ON w.id=b.work_item_id WHERE w.project_id=$1 ORDER BY b.work_item_id LIMIT 1001`},
	{"work_comments.jsonl", `SELECT to_jsonb(t) FROM qual.work_comments t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"work_role_entries.jsonl", `SELECT to_jsonb(t) FROM qual.work_role_entries t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"work_dependencies.jsonl", `SELECT to_jsonb(t) FROM qual.work_dependencies t WHERE project_id=$1 ORDER BY from_item_id,to_item_id LIMIT 1001`},
	{"test_suites.jsonl", `SELECT to_jsonb(t) FROM qual.test_suites t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"test_cases.jsonl", `SELECT to_jsonb(t) FROM qual.test_cases t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"test_case_versions.jsonl", `SELECT to_jsonb(t) FROM qual.test_case_versions t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"test_case_coverage.jsonl", `SELECT to_jsonb(t) FROM qual.test_case_coverage t WHERE project_id=$1 ORDER BY test_case_id,work_item_id LIMIT 1001`},
	{"test_runs.jsonl", `SELECT to_jsonb(t) FROM qual.test_runs t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"test_run_items.jsonl", `SELECT to_jsonb(t) FROM qual.test_run_items t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"test_attempts.jsonl", `SELECT to_jsonb(t) FROM qual.test_attempts t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"attempt_defects.jsonl", `SELECT to_jsonb(t) FROM qual.attempt_defects t WHERE project_id=$1 ORDER BY attempt_id,bug_work_item_id LIMIT 1001`},
	{"test_report_imports.jsonl", `SELECT to_jsonb(t) FROM qual.test_report_imports t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"artifacts.jsonl", `SELECT to_jsonb(t)-'object_key' FROM qual.artifacts t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"artifact_maintenance_events.jsonl", `SELECT to_jsonb(t) FROM qual.artifact_maintenance_events t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"external_references.jsonl", `SELECT to_jsonb(t) FROM qual.external_references t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"audit_records.jsonl", `SELECT to_jsonb(t) FROM qual.audit_records t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"activity_records.jsonl", `SELECT to_jsonb(t) FROM qual.activity_records t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"outbox_events.jsonl", `SELECT to_jsonb(t) FROM qual.outbox_events t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
}

// Goose13 governance is appended only when its schema actually exists.
// Goose12 archives retain their previous contract without nonexistent-table
// failures; no historical Done is silently rewritten or called independent.
// Schema v2 adds Thread and ThreadLink history; schema v3 additionally adds
// AgentRun and AgentRunReport history. Versions are not interchangeable.
var threadSnapshotSources = []snapshotSource{
	{"external_threads.jsonl", `SELECT to_jsonb(t) FROM qual.external_threads t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"work_thread_links.jsonl", `SELECT to_jsonb(t) FROM qual.work_thread_links t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
}
var agentRunSnapshotSources = []snapshotSource{
	{"agent_runs.jsonl", `SELECT to_jsonb(t) FROM qual.agent_runs t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"agent_run_reports.jsonl", `SELECT to_jsonb(t) FROM qual.agent_run_reports t WHERE project_id=$1 ORDER BY agent_run_id,sequence LIMIT 1001`},
}

var governanceSnapshotSources = []snapshotSource{
	{"project_review_policies.jsonl", `SELECT to_jsonb(t) FROM qual.project_review_policies t WHERE project_id=$1 ORDER BY project_id LIMIT 1001`},
	{"work_review_policy_overrides.jsonl", `SELECT to_jsonb(t) FROM qual.work_review_policy_overrides t WHERE project_id=$1 ORDER BY work_item_id LIMIT 1001`},
	{"review_policy_changes.jsonl", `SELECT to_jsonb(t) FROM qual.review_policy_changes t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
	{"work_acceptance_decisions.jsonl", `SELECT to_jsonb(t) FROM qual.work_acceptance_decisions t WHERE project_id=$1 ORDER BY id LIMIT 1001`},
}

func snapshotJSONL(ctx context.Context, tx pgx.Tx, pid uuid.UUID, source snapshotSource) (archiveFile, error) {
	rows, err := tx.Query(ctx, source.Query, pid)
	if err != nil {
		return archiveFile{}, err
	}
	defer rows.Close()
	var buf bytes.Buffer
	count := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return archiveFile{}, err
		}
		count++
		if count > MaxRowsPerArchiveTable {
			return archiveFile{}, domain.ErrLimit
		}
		if buf.Len()+len(raw)+1 > MaxArchiveBytes {
			return archiveFile{}, domain.ErrLimit
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return archiveFile{}, err
	}
	return archiveFile{Name: source.Name, Data: buf.Bytes(), Rows: count}, nil
}

// Stream external telemetry into individually bounded JSONL files. The first
// thousand retain the legacy path; later chunks are numbered in stable
// project/Run/sequence order within one repeatable-read PostgreSQL snapshot.
// The global 32 MiB envelope is still a hard failure, never silent data loss.
func snapshotRunReports(ctx context.Context, tx pgx.Tx, pid uuid.UUID) ([]archiveFile, error) {
	rows, err := tx.Query(ctx, `SELECT to_jsonb(t) FROM qual.agent_run_reports t
		WHERE project_id=$1 ORDER BY agent_run_id,sequence`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []archiveFile{{Name: "agent_run_reports.jsonl"}}
	totalBytes := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(raw)+1 > MaxArchiveBytes-totalBytes {
			return nil, domain.ErrLimit
		}
		current := &files[len(files)-1]
		if current.Rows == MaxRowsPerArchiveTable {
			if len(files) >= MaxArchiveFiles {
				return nil, domain.ErrLimit
			}
			files = append(files, archiveFile{Name: fmt.Sprintf("agent_run_reports/part-%06d.jsonl", len(files))})
			current = &files[len(files)-1]
		}
		current.Data = append(current.Data, raw...)
		current.Data = append(current.Data, '\n')
		current.Rows++
		totalBytes += len(raw) + 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return files, nil
}

func buildArchive(pid uuid.UUID, snapshot time.Time, files []archiveFile) ([]byte, domain.Manifest, error) {
	return buildArchiveWithSchema(pid, snapshot, files, 1)
}
func buildArchiveWithSchema(pid uuid.UUID, snapshot time.Time, files []archiveFile, schemaVersion int) ([]byte, domain.Manifest, error) {
	if len(files)+1 > MaxArchiveFiles || (schemaVersion != 1 && schemaVersion != 2 && schemaVersion != 3) {
		return nil, domain.Manifest{}, domain.ErrLimit
	}
	manifest := domain.Manifest{ArchiveSchemaVersion: schemaVersion, ProjectID: pid, SnapshotAt: snapshot, Files: make([]domain.FileManifest, 0, len(files))}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	sumRaw := 0
	for _, file := range files {
		sumRaw += len(file.Data)
		if sumRaw > MaxArchiveBytes {
			_ = writer.Close()
			return nil, manifest, domain.ErrLimit
		}
		digest := sha256.Sum256(file.Data)
		manifest.Files = append(manifest.Files, domain.FileManifest{
			Path: file.Name, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(file.Data)), Records: file.Rows,
		})
		entry, err := writer.Create(file.Name)
		if err != nil {
			return nil, manifest, err
		}
		if _, err := entry.Write(file.Data); err != nil {
			return nil, manifest, err
		}
	}
	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, manifest, err
	}
	entry, err := writer.Create("manifest.json")
	if err != nil {
		return nil, manifest, err
	}
	if _, err := entry.Write(rawManifest); err != nil {
		return nil, manifest, err
	}
	if err := writer.Close(); err != nil {
		return nil, manifest, err
	}
	if output.Len() > MaxArchiveBytes {
		return nil, manifest, domain.ErrLimit
	}
	return output.Bytes(), manifest, nil
}
func VerifyArchive(data []byte) (domain.Manifest, error) {
	var manifest domain.Manifest
	if len(data) == 0 || len(data) > MaxArchiveBytes {
		return manifest, domain.ErrLimit
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return manifest, domain.ErrIntegrity
	}
	if len(reader.File) > MaxArchiveFiles {
		return manifest, domain.ErrLimit
	}
	entries := make(map[string]*zip.File)
	for _, f := range reader.File {
		if _, exists := entries[f.Name]; exists {
			return manifest, domain.ErrIntegrity
		}
		entries[f.Name] = f
	}
	meta, ok := entries["manifest.json"]
	if !ok {
		return manifest, domain.ErrIntegrity
	}
	raw, err := zipReadLimit(meta, 2*1024*1024)
	if err != nil {
		return manifest, domain.ErrIntegrity
	}
	if err := json.Unmarshal(raw, &manifest); err != nil ||
		(manifest.ArchiveSchemaVersion != 1 && manifest.ArchiveSchemaVersion != 2 && manifest.ArchiveSchemaVersion != 3) {
		return manifest, domain.ErrIntegrity
	}
	// Schema v1 predates Thread/Link history. Mislabelled archives must
	// not bypass the stricter v2/v3 required-file contract.
	if manifest.ArchiveSchemaVersion == 1 {
		if _, exists := entries["external_threads.jsonl"]; exists {
			return manifest, domain.ErrIntegrity
		}
		if _, exists := entries["work_thread_links.jsonl"]; exists {
			return manifest, domain.ErrIntegrity
		}
	}
	if manifest.ArchiveSchemaVersion >= 2 {
		if _, exists := entries["external_threads.jsonl"]; !exists {
			return manifest, domain.ErrIntegrity
		}
		if _, exists := entries["work_thread_links.jsonl"]; !exists {
			return manifest, domain.ErrIntegrity
		}
	}
	if manifest.ArchiveSchemaVersion == 3 {
		if _, exists := entries["agent_runs.jsonl"]; !exists {
			return manifest, domain.ErrIntegrity
		}
		if _, exists := entries["agent_run_reports.jsonl"]; !exists {
			return manifest, domain.ErrIntegrity
		}
	}
	// v2 is Thread-only. Never silently accept a v2 with Run facts.
	if manifest.ArchiveSchemaVersion <= 2 {
		for name := range entries {
			if name == "agent_runs.jsonl" || name == "agent_run_reports.jsonl" || strings.HasPrefix(name, "agent_run_reports/") {
				return manifest, domain.ErrIntegrity
			}
		}
	}
	if err := verifyRunReportShards(manifest, entries); err != nil {
		return manifest, err
	}
	if len(entries) != len(manifest.Files)+1 {
		return manifest, domain.ErrIntegrity
	}
	// The manifest must cover *every* ZIP member exactly once. A duplicate
	// manifest path could otherwise conceal an omitted, unverified member
	// (including an altered agent_runs.jsonl) while preserving the file count.
	seenManifestPaths := make(map[string]struct{}, len(manifest.Files))
	var totalUncompressed int64
	for _, item := range manifest.Files {
		if item.Path == "manifest.json" {
			return manifest, domain.ErrIntegrity
		}
		if _, duplicate := seenManifestPaths[item.Path]; duplicate {
			return manifest, domain.ErrIntegrity
		}
		seenManifestPaths[item.Path] = struct{}{}
		if item.SizeBytes < 0 || item.SizeBytes > MaxArchiveBytes || totalUncompressed > MaxArchiveBytes-item.SizeBytes {
			return manifest, domain.ErrLimit
		}
		totalUncompressed += item.SizeBytes
		entry, ok := entries[item.Path]
		if !ok {
			return manifest, domain.ErrIntegrity
		}
		if entry.UncompressedSize64 != uint64(item.SizeBytes) {
			return manifest, domain.ErrIntegrity
		}
		content, err := zipReadLimit(entry, item.SizeBytes)
		if err != nil {
			return manifest, domain.ErrIntegrity
		}
		if err := domain.Verify(content, item.SizeBytes, item.SHA256); err != nil {
			return manifest, err
		}
		if (item.Records > 0 || (manifest.ArchiveSchemaVersion == 3 && strings.HasSuffix(item.Path, ".jsonl"))) &&
			bytes.Count(content, []byte("\n")) != item.Records {
			return manifest, domain.ErrIntegrity
		}
	}
	return manifest, nil
}

// v3 sharding is lossless only if every contiguous part is present, and all
// earlier parts are full. These checks complement every ZIP member's SHA.
func verifyRunReportShards(manifest domain.Manifest, entries map[string]*zip.File) error {
	if manifest.ArchiveSchemaVersion != 3 {
		return nil
	}
	const prefix = "agent_run_reports/"
	chunks := make(map[int]int)
	baseRecords := -1
	for _, f := range manifest.Files {
		if f.Path == "agent_run_reports.jsonl" {
			baseRecords = f.Records
			if baseRecords < 0 || baseRecords > MaxRowsPerArchiveTable {
				return domain.ErrIntegrity
			}
			continue
		}
		if !strings.HasPrefix(f.Path, prefix) {
			continue
		}
		var part int
		if _, err := fmt.Sscanf(f.Path, "agent_run_reports/part-%06d.jsonl", &part); err != nil ||
			part < 1 || fmt.Sprintf("agent_run_reports/part-%06d.jsonl", part) != f.Path ||
			f.Records < 1 || f.Records > MaxRowsPerArchiveTable {
			return domain.ErrIntegrity
		}
		chunks[part] = f.Records
	}
	for name := range entries {
		if strings.HasPrefix(name, prefix) && !isRunReportPart(name) {
			return domain.ErrIntegrity
		}
	}
	if baseRecords < 0 || (len(chunks) > 0 && baseRecords != MaxRowsPerArchiveTable) {
		return domain.ErrIntegrity
	}
	for i := 1; i <= len(chunks); i++ {
		records, ok := chunks[i]
		if !ok || (i < len(chunks) && records != MaxRowsPerArchiveTable) {
			return domain.ErrIntegrity
		}
	}
	return nil
}

func isRunReportPart(name string) bool {
	var part int
	_, err := fmt.Sscanf(name, "agent_run_reports/part-%06d.jsonl", &part)
	return err == nil && part > 0 && fmt.Sprintf("agent_run_reports/part-%06d.jsonl", part) == name
}

func zipReadLimit(file *zip.File, limit int64) ([]byte, error) {
	in, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer in.Close()
	data, err := io.ReadAll(io.LimitReader(in, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, domain.ErrLimit
	}
	return data, nil
}
func (r Repository) CreateExport(ctx context.Context, a identity.Actor, p identity.Project, m application.Meta, h [32]byte, objects application.ObjectStore) (domain.Export, error) {
	var storedKey string
	body, err := r.idempotentWithOptions(ctx, a, p, "archive.export", m, h, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) (any, error) {
		var snapshot time.Time
		if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshot); err != nil {
			return nil, err
		}
		var goose13 bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('qual.work_acceptance_decisions') IS NOT NULL`).Scan(&goose13); err != nil {
			return nil, err
		}
		sources := append([]snapshotSource(nil), snapshotSources...)
		if goose13 {
			sources = append(sources, governanceSnapshotSources...)
		}
		var threadSchema, runSchema bool
		if err := tx.QueryRow(ctx, `SELECT
			to_regclass('qual.external_threads') IS NOT NULL AND to_regclass('qual.work_thread_links') IS NOT NULL,
			to_regclass('qual.agent_runs') IS NOT NULL AND to_regclass('qual.agent_run_reports') IS NOT NULL`).Scan(&threadSchema, &runSchema); err != nil {
			return nil, err
		}
		archiveSchemaVersion := 1
		if threadSchema {
			sources = append(sources, threadSnapshotSources...)
			archiveSchemaVersion = 2
		}
		if runSchema {
			if !threadSchema {
				return nil, domain.ErrUnavailable // incomplete migration, never mislabel Run as old v2
			}
			sources = append(sources, agentRunSnapshotSources[0])
			archiveSchemaVersion = 3
		}
		files := make([]archiveFile, 0, len(sources)+20)
		for _, source := range sources {
			file, err := snapshotJSONL(ctx, tx, p.ID, source)
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
		if runSchema {
			reports, err := snapshotRunReports(ctx, tx, p.ID)
			if err != nil {
				return nil, err
			}
			files = append(files, reports...)
		}
		rows, err := tx.Query(ctx, `SELECT id,object_key,size_bytes,encode(sha256,'hex') FROM qual.artifacts
   WHERE project_id=$1 AND state='ready' ORDER BY id LIMIT 101`, p.ID)
		if err != nil {
			return nil, err
		}
		type attachment struct {
			id   uuid.UUID
			key  string
			size int64
			sum  string
		}
		attachments := make([]attachment, 0)
		for rows.Next() {
			var item attachment
			if err := rows.Scan(&item.id, &item.key, &item.size, &item.sum); err != nil {
				rows.Close()
				return nil, err
			}
			attachments = append(attachments, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(attachments) > 100 {
			return nil, domain.ErrLimit
		}
		for _, item := range attachments {
			content, err := objects.Get(ctx, item.key, domain.MaxArtifactSize)
			if err != nil {
				return nil, domain.ErrUnavailable
			}
			if err := domain.Verify(content, item.size, item.sum); err != nil {
				return nil, err
			}
			files = append(files, archiveFile{Name: "artifacts/" + item.id.String(), Data: content})
		}
		zipBytes, manifest, err := buildArchiveWithSchema(p.ID, snapshot, files, archiveSchemaVersion)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(zipBytes)
		exp := domain.Export{ID: uuid.New(), ProjectID: p.ID, SHA256: hex.EncodeToString(digest[:]),
			SizeBytes: int64(len(zipBytes)), SnapshotAt: snapshot, Manifest: manifest}
		key := "projects/" + p.ID.String() + "/exports/" + exp.ID.String() + ".zip"
		if err := objects.Put(ctx, key, "application/zip", zipBytes); err != nil {
			return nil, domain.ErrUnavailable
		}
		storedKey = key
		rawManifest, _ := json.Marshal(manifest)
		err = tx.QueryRow(ctx, `INSERT INTO qual.archive_exports
    (id,project_id,requested_by,object_key,archive_sha256,size_bytes,manifest,snapshot_at)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
			exp.ID, p.ID, a.PrincipalID, key, digest[:], exp.SizeBytes, rawManifest, snapshot).Scan(&exp.CreatedAt)
		if err != nil {
			return nil, err
		}
		err = r.emit(ctx, tx, a, p, m, "archive.exported", exp.ID, nil, 1, map[string]any{
			"sha256": exp.SHA256, "size_bytes": exp.SizeBytes, "snapshot_at": exp.SnapshotAt,
		})
		return exp, err
	})
	if err != nil && storedKey != "" {
		_ = objects.Delete(ctx, storedKey)
	}
	if err != nil {
		return domain.Export{}, err
	}
	var exp domain.Export
	err = json.Unmarshal(body, &exp)
	return exp, err
}
func scanExport(row interface{ Scan(...any) error }) (domain.Export, string, error) {
	var exp domain.Export
	var key string
	var raw []byte
	err := row.Scan(&exp.ID, &exp.ProjectID, &key, &exp.SHA256, &exp.SizeBytes, &raw, &exp.SnapshotAt, &exp.CreatedAt)
	if err != nil {
		return exp, key, err
	}
	var manifest domain.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return exp, key, err
	}
	exp.Manifest = manifest
	return exp, key, nil
}
func (r Repository) GetExport(ctx context.Context, pid, id uuid.UUID) (domain.Export, string, error) {
	exp, key, err := scanExport(r.Pool.QueryRow(ctx, `SELECT id,project_id,object_key,
 encode(archive_sha256,'hex'),size_bytes,manifest,snapshot_at,created_at
 FROM qual.archive_exports WHERE project_id=$1 AND id=$2`, pid, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return exp, "", domain.ErrNotFound
	}
	return exp, key, err
}
func (r Repository) ListExports(ctx context.Context, pid uuid.UUID) ([]domain.Export, error) {
	rows, err := r.Pool.Query(ctx, `SELECT id,project_id,object_key,encode(archive_sha256,'hex'),
 size_bytes,manifest,snapshot_at,created_at FROM qual.archive_exports
 WHERE project_id=$1 ORDER BY created_at DESC,id DESC LIMIT 50`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	exports := make([]domain.Export, 0)
	for rows.Next() {
		exp, _, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		exports = append(exports, exp)
	}
	return exports, rows.Err()
}

var _ = fmt.Sprint
