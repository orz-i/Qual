package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
)

func TestAgentRunArchiveV3ReportPartsAreLosslessAndContiguous(t *testing.T) {
	pid := uuid.New()
	base := []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "external_threads.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "work_thread_links.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "agent_runs.jsonl", Data: []byte("{}\n"), Rows: 1},
		{Name: "agent_run_reports.jsonl", Data: []byte(strings.Repeat("{}\n", 1000)), Rows: 1000},
	}
	extra := archiveFile{Name: "agent_run_reports/part-000001.jsonl", Data: []byte("{}\n"), Rows: 1}
	check := func(label string, files []archiveFile, wantErr bool) {
		t.Helper()
		data, _, err := buildArchiveWithSchema(pid, time.Now(), files, 3)
		if err != nil {
			t.Fatalf("%s archive build: %v", label, err)
		}
		_, err = VerifyArchive(data)
		if wantErr && err != domain.ErrIntegrity {
			t.Errorf("%s expected integrity failure, got %v", label, err)
		}
		if !wantErr && err != nil {
			t.Errorf("%s expected valid archive, got %v", label, err)
		}
	}
	check("1000 no shard", base, false)
	check("1001 records", append(append([]archiveFile(nil), base...), extra), false)
	notFull := append([]archiveFile(nil), base...)
	notFull[4].Rows = 999
	check("partial first with shard", append(notFull, extra), true)
	missing := extra
	missing.Name = "agent_run_reports/part-000002.jsonl"
	check("missing first shard", append(append([]archiveFile(nil), base...), missing), true)
	empty := extra
	empty.Data, empty.Rows = nil, 0
	check("empty trailing shard", append(append([]archiveFile(nil), base...), empty), true)
	invalid := extra
	invalid.Name = "agent_run_reports/../part-000001.jsonl"
	check("untrusted path", append(append([]archiveFile(nil), base...), invalid), true)
}
