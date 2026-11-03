package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
)

func TestArchiveV2IncludesThreadHistoryAndV1RemainsReadable(t *testing.T) {
	pid := uuid.New()
	files := []archiveFile{
		{Name: "work_items.jsonl", Data: []byte("{\"id\":1}\n"), Rows: 1},
		{Name: "external_threads.jsonl", Data: []byte("{\"id\":\"thread\"}\n"), Rows: 1},
		{Name: "work_thread_links.jsonl", Data: []byte("{\"removed_at\":\"historical\"}\n"), Rows: 1},
	}
	data, manifest, err := buildArchiveWithSchema(pid, time.Now(), files, 2)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ArchiveSchemaVersion != 2 {
		t.Fatalf("thread archive version=%d", manifest.ArchiveSchemaVersion)
	}
	verified, err := VerifyArchive(data)
	if err != nil || verified.ProjectID != pid || verified.ArchiveSchemaVersion != 2 {
		t.Fatalf("v2 archive integrity failed: %+v %v", verified, err)
	}
	missing, _, err := buildArchiveWithSchema(pid, time.Now(), files[:2], 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArchive(missing); err != domain.ErrIntegrity {
		t.Fatalf("v2 archive without link history accepted: %v", err)
	}
	legacy, _, err := buildArchive(pid, time.Now(), files[:1])
	if err != nil {
		t.Fatal(err)
	}
	v1, err := VerifyArchive(legacy)
	if err != nil || v1.ArchiveSchemaVersion != 1 {
		t.Fatalf("v1 verification regressed: %v %+v", err, v1)
	}
}
