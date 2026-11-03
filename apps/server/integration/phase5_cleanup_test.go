//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

func TestStaleUploadedS3CleanupRecordsJournal(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("real S3 required")
	}
	work := create(t, f, f.admin, "uploaded-but-incomplete")
	data := []byte("uploaded but never finalized")
	sum := sha256.Sum256(data)
	base := evidenceURL(f)
	status, artifact := testRequest(t, "POST", base+"/artifacts", f.agent, "stale-upload-start", "",
		map[string]any{"target_kind": "work_item", "target_id": work["id"], "filename": "incomplete.txt",
			"media_type": "text/plain", "size_bytes": len(data), "sha256": hex.EncodeToString(sum[:])})
	if status != 201 {
		t.Fatalf("artifact begin: %d %v", status, artifact)
	}
	id := artifact["id"].(string)
	status, _ = rawHTTP(t, "PUT", base+"/artifacts/"+id+"/content", f.agent, "stale-upload-content", data, "application/octet-stream")
	if status != 200 {
		t.Fatalf("artifact upload: %d", status)
	}
	_, err := f.pool.Exec(context.Background(), `UPDATE qual.artifacts SET uploaded_at=now()-interval '3 hours' WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	if err := f.pool.QueryRow(context.Background(), `SELECT object_key FROM qual.artifacts WHERE id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	count, err := (evidencepg.Repository{Pool: f.pool, Objects: f.objects}).CleanupStale(context.Background(), f.objects, time.Hour)
	if err != nil || count < 1 {
		t.Fatalf("cleanup result=%d, err=%v", count, err)
	}
	var state, action string
	err = f.pool.QueryRow(context.Background(), `SELECT a.state,m.action FROM qual.artifacts a
 JOIN qual.artifact_maintenance_events m ON m.artifact_id=a.id
 WHERE a.id=$1`, uuid.MustParse(id)).Scan(&state, &action)
	if err != nil || state != "failed" || action != "expired_uploaded_cleanup" {
		t.Fatalf("state=%s action=%s err=%v", state, action, err)
	}
	if _, err := f.objects.Get(context.Background(), key, 1024); err == nil {
		t.Fatal("orphaned uploaded S3 object still present")
	}
	if status, _ := testRequest(t, "POST", base+"/artifacts/"+id+"/complete", f.agent, "stale-upload-complete-after-cleanup", "", nil); status != 409 {
		t.Fatalf("finalizing cleaned attachment should conflict, got %d", status)
	}
}
