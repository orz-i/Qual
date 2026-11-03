package postgres

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExternalAgentRunArchiveCompatibility(t *testing.T) {
	project := uuid.New()
	old, original, err := buildArchive(project, time.Now(), []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{\"key\":\"QUAL-1\"}\n"), Rows: 1},
	})
	if err != nil || original.ArchiveSchemaVersion != 1 {
		t.Fatalf("legacy archive: %v %+v", err, original)
	}
	if verified, err := VerifyArchive(old); err != nil || verified.ArchiveSchemaVersion != 1 {
		t.Fatalf("existing schema-v1 archive must remain verifiable: %v", err)
	}
	v3files := []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{\"key\":\"QUAL-32\"}\n"), Rows: 1},
		{Name: "external_threads.jsonl", Data: []byte("{\"id\":\"thread\"}\n"), Rows: 1},
		{Name: "work_thread_links.jsonl", Data: []byte("{\"id\":\"link\"}\n"), Rows: 1},
		{Name: "agent_runs.jsonl", Data: []byte("{\"source_status\":\"succeeded\"}\n"), Rows: 1},
		{Name: "agent_run_reports.jsonl", Data: []byte("{\"source_report_id\":\"real-external\"}\n"), Rows: 1},
	}
	newer, manifest, err := buildArchiveWithSchema(project, time.Now(), v3files, 3)
	if err != nil || manifest.ArchiveSchemaVersion != 3 || len(manifest.Files) != 5 {
		t.Fatalf("new archive schema: %v %+v", err, manifest)
	}
	if verified, err := VerifyArchive(newer); err != nil || verified.ArchiveSchemaVersion != 3 {
		t.Fatalf("new archive verification: %v", err)
	}
	// A v2 Run-only archive is ambiguous with QUAL-29's v2 Thread contract.
	mislabelled, _, err := buildArchiveWithSchema(project, time.Now(), v3files, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArchive(mislabelled); err == nil {
		t.Fatal("mislabelled v2 Run archive was accepted")
	}
	for _, missing := range []int{1, 2, 3, 4} {
		incomplete := append([]archiveFile(nil), v3files[:missing]...)
		incomplete = append(incomplete, v3files[missing+1:]...)
		corrupt, _, err := buildArchiveWithSchema(project, time.Now(), incomplete, 3)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyArchive(corrupt); err == nil {
			t.Fatalf("v3 missing file %d accepted", missing)
		}
	}
	if bytes.Equal(old, newer) {
		t.Fatal("schema upgrade lost AgentRun reports")
	}
	foundRuns, foundReports := false, false
	for _, src := range agentRunSnapshotSources {
		if src.Name == "agent_runs.jsonl" {
			foundRuns = true
		}
		if src.Name == "agent_run_reports.jsonl" {
			foundReports = true
		}
	}
	if !foundRuns || !foundReports {
		t.Fatal("both immutable run source tables must be exported")
	}
}
