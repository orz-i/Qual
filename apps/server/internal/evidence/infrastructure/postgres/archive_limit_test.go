package postgres

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
)

func TestArchiveVerifyRejectsCompressedOversize(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 17*1024*1024)
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	files := []string{"part-a.jsonl", "part-b.jsonl"}
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	meta := domain.Manifest{ArchiveSchemaVersion: 1, ProjectID: uuid.New(), SnapshotAt: time.Now(),
		Files: []domain.FileManifest{
			{Path: files[0], SizeBytes: int64(len(data)), SHA256: checksum},
			{Path: files[1], SizeBytes: int64(len(data)), SHA256: checksum},
		}}
	raw, _ := json.Marshal(meta)
	entry, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > 100000 {
		t.Fatalf("test ZIP should be heavily compressed, got %d", buf.Len())
	}
	if _, err := VerifyArchive(buf.Bytes()); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("ZIP bomb size limit not enforced: %v", err)
	}
}
