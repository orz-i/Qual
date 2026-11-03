//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

// QUAL-43/44/46: a real HTTP/S3 Project ZIP holds >1000 external reports and
// both Thread and Run facts. Synthetic fixture facts never claim real execution.
func TestAgentRunArchiveV3RealS3MoreThan1000Heartbeats(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_") {
		t.Skip("requires disposable qual32_run_* database with Goose14 and Goose16")
	}
	f := setup(t)
	if f.objects == nil {
		t.Skip("requires private S3 test environment")
	}
	ctx := context.Background()
	work := create(t, f, f.agent, "qual43-sharded-archive-work")
	itemID := work["id"].(string)

	input := threadInput("example/project/pull/91", 1)
	st, linked, _ := request(t, "POST", threadBase(f, itemID), f.agent, "qual44-real-thread-link", "", input)
	if st != 201 {
		t.Fatalf("linked Thread HTTP %d: %v", st, linked)
	}
	threadID := linked["thread"].(map[string]any)["id"].(string)

	var workspaceID, reporterID uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT principal_id FROM qual.credentials WHERE id=$1", f.agentCredential).Scan(&reporterID); err != nil {
		t.Fatal(err)
	}
	runID := uuid.New()
	_, err := f.pool.Exec(ctx, `INSERT INTO qual.agent_runs
	 (id,workspace_id,project_id,work_item_id,source,external_run_id,host,
	  reporter_principal_id,registration_credential_id,source_status)
	 VALUES($1,$2,$3,$4,'synthetic-archive-regression','sample-1205','isolated-fixture',$5,$6,'running')`,
		runID, workspaceID, f.project, work["id"], reporterID, f.agentCredential)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic, immutable source reports exercise archive capacity without
	// bypassing production Heartbeat sampling or claiming a real worker ran.
	_, err = f.pool.Exec(ctx, `INSERT INTO qual.agent_run_reports
	 (id,workspace_id,project_id,agent_run_id,sequence,source_report_id,report_hash,
	  kind,source_status,occurred_at,received_at,applied,run_version,
	  reporter_principal_id,credential_id)
	 SELECT gen_random_uuid(),$1,$2,$3,g.i,concat('synthetic-heartbeat-',g.i),
	  decode(repeat('ab',32),'hex'),'heartbeat',NULL,now(),now(),false,1,$4,$5
	 FROM generate_series(1,1205) AS g(i)`, workspaceID, f.project, runID, reporterID, f.agentCredential)
	if err != nil {
		t.Fatal(err)
	}
	base := evidenceURL(f)
	st, result, _ := request(t, "POST", base+"/exports", f.admin, "qual43-archive-1205-real-s3", "", nil)
	if st != 201 {
		t.Fatalf("ZIP HTTP %d, expected 201: %v", st, result)
	}
	st, raw := rawHTTP(t, "GET", base+"/exports/"+result["id"].(string)+"/content", f.admin, "", nil, "")
	if st != 200 {
		t.Fatalf("download HTTP%d: %s", st, raw)
	}
	sum := sha256.Sum256(raw)
	if result["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatal("SHA-256 disagrees with downloaded ZIP")
	}
	manifest, err := evidencepg.VerifyArchive(raw)
	if err != nil || manifest.ArchiveSchemaVersion != 3 {
		t.Fatalf("expected verified v3 ZIP, got %+v %v", manifest, err)
	}
	if result["manifest"].(map[string]any)["archive_schema_version"] != float64(3) {
		t.Fatalf("export REST response fails v3 contract: %v", result["manifest"])
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var baseReportRows, chunkRows, totalRows int
	var seenThread, seenLink, seenRun bool
	for _, zf := range zr.File {
		if zf.Name != "external_threads.jsonl" && zf.Name != "work_thread_links.jsonl" &&
			zf.Name != "agent_runs.jsonl" && zf.Name != "agent_run_reports.jsonl" &&
			!strings.HasPrefix(zf.Name, "agent_run_reports/") {
			continue
		}
		in, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(in, evidencepg.MaxArchiveBytes+1))
		_ = in.Close()
		if err != nil {
			t.Fatal(err)
		}
		switch zf.Name {
		case "external_threads.jsonl":
			seenThread = bytes.Contains(data, []byte(threadID))
		case "work_thread_links.jsonl":
			seenLink = bytes.Contains(data, []byte(threadID))
		case "agent_runs.jsonl":
			seenRun = bytes.Contains(data, []byte(runID.String()))
		case "agent_run_reports.jsonl":
			baseReportRows = bytes.Count(data, []byte("\n"))
			totalRows += baseReportRows
		default:
			chunkRows += bytes.Count(data, []byte("\n"))
			totalRows += bytes.Count(data, []byte("\n"))
		}
	}
	if !seenThread || !seenLink || !seenRun ||
		baseReportRows != 1000 || chunkRows != 205 || totalRows != 1205 {
		t.Fatalf("lost archive facts: thread=%t link=%t run=%t base=%d chunks=%d total=%d",
			seenThread, seenLink, seenRun, baseReportRows, chunkRows, totalRows)
	}
	t.Logf("verified v3 S3 ZIP %d bytes, 1205 synthetic heartbeats in 1000+205 shards, Thread+Run intact", len(raw))
}
