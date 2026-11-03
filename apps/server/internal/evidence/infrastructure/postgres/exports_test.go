package postgres

import (
	"bytes"
	"github.com/google/uuid"
	"qual/internal/evidence/domain"
	"testing"
	"time"
)

func TestArchiveManifestHashesAndCorruption(t *testing.T) {
	original := []archiveFile{{Name: "work_items.jsonl", Data: []byte("{\"id\":1}\n"), Rows: 1},
		{Name: "test_attempts.jsonl", Data: nil, Rows: 0},
		{Name: "artifacts/" + uuid.NewString(), Data: []byte("evidence bytes")}}
	zipData, manifest, err := buildArchive(uuid.New(), time.Now(), original)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 3 {
		t.Fatal("manifest missing files")
	}
	verified, err := VerifyArchive(zipData)
	if err != nil || verified.ArchiveSchemaVersion != 1 {
		t.Fatalf("verification failed: %v", err)
	}
	if _, err := VerifyArchive([]byte("not a zip")); err == nil {
		t.Fatal("invalid zip accepted")
	}
	altered := append([]byte(nil), zipData...)
	altered[len(altered)-25] ^= 0xff
	if bytes.Equal(altered, zipData) {
		t.Fatal("tamper setup failed")
	}
	// Corrupt ZIP may be detected either by zip checksum or by manifest hash.
	if _, err := VerifyArchive(altered); err == nil {
		t.Fatal("corrupted archive accepted")
	}
	_ = domain.Manifest{}
}
