//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

// Security acceptance: two independent synthetic Runs, together exceeding the
// 1000-row legacy ceiling, must retain every sequence and both Run identities.
// Only a disposable qual32_run_* DB may contain these generated reports.
func TestQual4344AuditMultiRunArchiveCompleteness(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual32_run_") {
		t.Skip("requires disposable qual32_run_* PostgreSQL database")
	}
	f := setup(t)
	if f.objects == nil {
		t.Skip("requires real private S3 fixture")
	}
	ctx := context.Background()
	work := create(t, f, f.agent, "audit-qual4344-multi-run")
	itemID := work["id"].(string)
	status, link, _ := request(t, "POST", threadBase(f, itemID), f.agent, "qual4344-multirun-link", "", threadInput("example/audit/pull/212", 1))
	if status != 201 {
		t.Fatalf("create thread status %d: %+v", status, link)
	}
	threadID := link["thread"].(map[string]any)["id"].(string)
	var workspaceID, reporterID uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT workspace_id FROM qual.projects WHERE id=$1", f.project).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT principal_id FROM qual.credentials WHERE id=$1", f.agentCredential).Scan(&reporterID); err != nil {
		t.Fatal(err)
	}
	wanted := map[string]int{}
	for i, size := range []int{605, 620} {
		runID := uuid.New()
		wanted[runID.String()] = size
		_, err := f.pool.Exec(ctx, `INSERT INTO qual.agent_runs
           (id,workspace_id,project_id,work_item_id,source,external_run_id,host,
            reporter_principal_id,registration_credential_id,source_status)
           VALUES($1,$2,$3,$4,'synthetic-archive-security',$5,'isolated-fixture',$6,$7,'running')`,
			runID, workspaceID, f.project, itemID, fmt.Sprintf("audit-multi-run-%d", i), reporterID, f.agentCredential)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.pool.Exec(ctx, `INSERT INTO qual.agent_run_reports
          (id,workspace_id,project_id,agent_run_id,sequence,source_report_id,report_hash,
           kind,source_status,occurred_at,received_at,applied,run_version,
           reporter_principal_id,credential_id)
          SELECT gen_random_uuid(),$1,$2,$3,g.i,concat('audit-hb-',g.i),
            decode(repeat('cd',32),'hex'),'heartbeat',NULL,now(),now(),false,1,$4,$5
          FROM generate_series(1,$6::int) AS g(i)`,
			workspaceID, f.project, runID, reporterID, f.agentCredential, size)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := evidenceURL(f)
	status, result, _ := request(t, "POST", base+"/exports", f.admin, "qual4344-multirun-export", "", nil)
	if status != 201 {
		t.Fatalf("export status %d: %+v", status, result)
	}
	status, raw := rawHTTP(t, "GET", base+"/exports/"+result["id"].(string)+"/content", f.admin, "", nil, "")
	if status != 200 {
		t.Fatalf("download status %d", status)
	}
	sum := sha256.Sum256(raw)
	if result["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatal("export SHA mismatch")
	}
	manifest, err := evidencepg.VerifyArchive(raw)
	if err != nil || manifest.ArchiveSchemaVersion != 3 {
		t.Fatalf("archive invalid: %v, v=%d", err, manifest.ArchiveSchemaVersion)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	seenRuns := map[string]bool{}
	seenSequences := map[string]map[int]bool{}
	baseCount, partCount := 0, 0
	seenThread, seenLink := false, false
	for _, member := range reader.File {
		if member.Name != "agent_runs.jsonl" && member.Name != "external_threads.jsonl" && member.Name != "work_thread_links.jsonl" &&
			member.Name != "agent_run_reports.jsonl" && !strings.HasPrefix(member.Name, "agent_run_reports/") {
			continue
		}
		in, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(in, evidencepg.MaxArchiveBytes+1))
		_ = in.Close()
		if err != nil {
			t.Fatal(err)
		}
		switch member.Name {
		case "external_threads.jsonl":
			seenThread = bytes.Contains(data, []byte(threadID))
		case "work_thread_links.jsonl":
			seenLink = bytes.Contains(data, []byte(threadID))
		case "agent_runs.jsonl":
			for runID := range wanted {
				seenRuns[runID] = bytes.Contains(data, []byte(runID))
			}
		default:
			lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
			if member.Name == "agent_run_reports.jsonl" {
				baseCount = len(lines)
			} else {
				partCount += len(lines)
			}
			for _, line := range lines {
				if len(line) == 0 {
					continue
				}
				var report struct {
					RunID    string `json:"agent_run_id"`
					Sequence int    `json:"sequence"`
				}
				if err := json.Unmarshal(line, &report); err != nil {
					t.Fatal(err)
				}
				if _, exists := wanted[report.RunID]; !exists {
					t.Fatalf("unknown run id in archive: %q", report.RunID)
				}
				if seenSequences[report.RunID] == nil {
					seenSequences[report.RunID] = map[int]bool{}
				}
				if seenSequences[report.RunID][report.Sequence] {
					t.Fatalf("duplicate sequence %d for %s", report.Sequence, report.RunID)
				}
				seenSequences[report.RunID][report.Sequence] = true
			}
		}
	}
	if !seenThread || !seenLink {
		t.Fatalf("thread/link lost: thread=%v link=%v", seenThread, seenLink)
	}
	if baseCount != 1000 || partCount != 225 {
		t.Fatalf("shard counts %d + %d != 1225", baseCount, partCount)
	}
	for runID, count := range wanted {
		if !seenRuns[runID] || len(seenSequences[runID]) != count {
			t.Fatalf("lost run %s: in runs=%v, reports=%d, wanted=%d", runID, seenRuns[runID], len(seenSequences[runID]), count)
		}
		for seq := 1; seq <= count; seq++ {
			if !seenSequences[runID][seq] {
				t.Fatalf("missing %s sequence %d", runID, seq)
			}
		}
	}
	t.Logf("verified archive v3: 2 Run IDs, 1225 synthetic reports, 1000+225 shards, thread/link, S3 and SHA-256")
}
