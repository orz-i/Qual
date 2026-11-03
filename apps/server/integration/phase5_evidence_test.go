//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

func evidenceURL(f *fixture) string {
	return fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
}
func rawHTTP(t *testing.T, method, url, token, idem string, data []byte, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 36*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}
func testUploadArtifact(t *testing.T, f *fixture, workID string, data []byte) (string, string) {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	base := evidenceURL(f)
	status, item := testRequest(t, "POST", base+"/artifacts", f.agent, "phase5-artifact-begin-"+uuid.NewString(), "",
		map[string]any{"target_kind": "work_item", "target_id": workID, "filename": "evidence.txt",
			"media_type": "text/plain", "size_bytes": len(data), "sha256": digest})
	if status != 201 {
		t.Fatalf("begin: %d %#v", status, item)
	}
	id := item["id"].(string)
	url := base + "/artifacts/" + id
	if status, _ := rawHTTP(t, "PUT", url+"/content", f.viewer, "viewer-no-upload", data, "text/plain"); status != 403 {
		t.Fatalf("viewer wrote attachment: %d", status)
	}
	if status, _ := rawHTTP(t, "PUT", url+"/content", f.agent, "phase5-bad-hash", []byte(strings.Repeat("x", len(data))), "text/plain"); status != 422 {
		t.Fatalf("wrong hash accepted: %d", status)
	}
	status, raw := rawHTTP(t, "PUT", url+"/content", f.agent, "phase5-artifact-bytes-"+uuid.NewString(), data, "text/plain")
	if status != 200 {
		t.Fatalf("put: %d %s", status, raw)
	}
	status, ready := testRequest(t, "POST", url+"/complete", f.agent, "phase5-artifact-complete-"+uuid.NewString(), "", nil)
	if status != 200 || ready["state"] != "ready" {
		t.Fatalf("complete: %d %v", status, ready)
	}
	return id, digest
}
func TestArtifactsRealS3IntegrityAndProjectIsolation(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("QUAL_S3_ENDPOINT needed for real object storage test")
	}
	work := create(t, f, f.admin, "artifact-root")
	data := []byte("Evidence reported by an external actor: no test or agent executed by Qual.\n")
	aid, digest := testUploadArtifact(t, f, work["id"].(string), data)
	base := evidenceURL(f)
	url := base + "/artifacts/" + aid
	status, body := rawHTTP(t, "GET", url+"/content", f.viewer, "", nil, "")
	if status != 200 || !bytes.Equal(body, data) {
		t.Fatalf("download auth %d %q", status, body)
	}
	if status, _ := rawHTTP(t, "GET", f.server.URL+"/api/v1/projects/"+f.otherProject.String()+"/artifacts/"+aid+"/content", f.agent, "", nil, ""); status != 404 {
		t.Fatalf("cross project leak %d", status)
	}
	if status, _ := rawHTTP(t, "GET", url+"/content", "", "", nil, ""); status != 401 {
		t.Fatalf("anonymous download %d", status)
	}
	status, items := testRequest(t, "GET", base+"/artifacts?target_kind=work_item&target_id="+work["id"].(string), f.agent, "", "", nil)
	if status != 200 || len(items["items"].([]any)) != 1 {
		t.Fatalf("artifact list %d %v", status, items)
	}
	if items["items"].([]any)[0].(map[string]any)["sha256"] != digest {
		t.Fatalf("wrong SHA in API %v", items)
	}
	// Corrupt S3 object behind metadata; Qual checks on every download.
	var key string
	if err := f.pool.QueryRow(context.Background(), `SELECT object_key FROM qual.artifacts WHERE id=$1`, aid).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Put(context.Background(), key, "text/plain", []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if status, _ := rawHTTP(t, "GET", url+"/content", f.viewer, "", nil, ""); status != 422 {
		t.Fatalf("tampered object accepted: %d", status)
	}
	if err := f.objects.Put(context.Background(), key, "text/plain", data); err != nil {
		t.Fatal(err)
	}
	if status, raw := rawHTTP(t, "GET", url+"/content", f.viewer, "", nil, ""); status != 200 || !bytes.Equal(raw, data) {
		t.Fatalf("restore after tamper failed %d", status)
	}
	if status, _ := testRequest(t, "POST", base+"/artifacts", f.reporter, "integration-cannot-write-work-artifact", "",
		map[string]any{"target_kind": "work_item", "target_id": work["id"], "filename": "ci.txt", "media_type": "text/plain", "size_bytes": 1, "sha256": digest}); status != 403 {
		t.Fatalf("integration work item artifact accepted: %d", status)
	}
	var audits int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND entity_id=$2`, f.project, aid).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("artifact audit count = %d: %v", audits, err)
	}
}
func TestExternalReferencesAndVerifiableProjectZIP(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("QUAL_S3_ENDPOINT needed")
	}
	work := create(t, f, f.agent, "archive-data-work")
	data := []byte("Report artifact: external result, not a command.\n")
	_, _ = testUploadArtifact(t, f, work["id"].(string), data)
	base := evidenceURL(f)
	refReq := map[string]any{"target_kind": "work_item", "target_id": work["id"],
		"provider": "github", "kind": "pull_request", "external_id": "sample-pr-12", "url": "https://github.com/example/repository/pull/12"}
	status, ref := testRequest(t, "POST", base+"/external-references", f.agent, "phase5-link-external-ref", "", refReq)
	if status != 201 {
		t.Fatalf("reference create %d %v", status, ref)
	}
	status, replayed := testRequest(t, "POST", base+"/external-references", f.agent, "phase5-link-external-ref", "", refReq)
	if status != 201 || replayed["id"] != ref["id"] {
		t.Fatalf("reference idempotency %d %v", status, replayed)
	}
	if status, _ := testRequest(t, "POST", base+"/external-references", f.agent, "phase5-ref-http-reject", "",
		map[string]any{"target_kind": "work_item", "target_id": work["id"], "provider": "other",
			"kind": "issue", "external_id": "1", "url": "http://example.com/issue/1"}); status != 422 {
		t.Fatalf("http link accepted: %d", status)
	}
	status, list := testRequest(t, "GET", base+"/external-references?target_kind=work_item&target_id="+work["id"].(string), f.viewer, "", "", nil)
	if status != 200 || len(list["items"].([]any)) != 1 {
		t.Fatalf("references list %d %v", status, list)
	}
	if status, _ := testRequest(t, "POST", base+"/exports", f.agent, "phase5-agent-export-denied", "", nil); status != 403 {
		t.Fatalf("Agent export allowed %d", status)
	}
	status, exp := testRequest(t, "POST", base+"/exports", f.admin, "phase5-admin-export-project", "", nil)
	if status != 201 {
		t.Fatalf("export %d %#v", status, exp)
	}
	id := exp["id"].(string)
	status, replay := testRequest(t, "POST", base+"/exports", f.admin, "phase5-admin-export-project", "", nil)
	if status != 201 || replay["id"] != id {
		t.Fatalf("export replay %d %v", status, replay)
	}
	if status, _ := testRequest(t, "GET", base+"/exports", f.agent, "", "", nil); status != 403 {
		t.Fatalf("Agent listed private exports: %d", status)
	}
	status, body := rawHTTP(t, "GET", base+"/exports/"+id+"/content", f.admin, "", nil, "")
	if status != 200 {
		t.Fatalf("zip GET %d %s", status, body)
	}
	digest := sha256.Sum256(body)
	if exp["sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatalf("archive sha mismatch: %v", exp)
	}
	manifest, err := evidencepg.VerifyArchive(body)
	if err != nil {
		t.Fatal(err)
	}
	if (manifest.ArchiveSchemaVersion < 1 || manifest.ArchiveSchemaVersion > 3) || manifest.ProjectID != f.project {
		t.Fatalf("archive header incorrect: %+v", manifest)
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	foundWork, foundObject, foundRef := false, false, false
	for _, file := range reader.File {
		in, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(in)
		_ = in.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "work_items.jsonl" && bytes.Contains(content, []byte(work["id"].(string))) {
			foundWork = true
		}
		if strings.HasPrefix(file.Name, "artifacts/") && bytes.Equal(content, data) {
			foundObject = true
		}
		if file.Name == "external_references.jsonl" && bytes.Contains(content, []byte("sample-pr-12")) {
			foundRef = true
		}
	}
	if !foundWork || !foundObject || !foundRef {
		t.Fatalf("archive missing project facts: work=%v,artifact=%v,ref=%v", foundWork, foundObject, foundRef)
	}
	var key string
	if err := f.pool.QueryRow(context.Background(), `SELECT object_key FROM qual.archive_exports WHERE id=$1`, id).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if err := f.objects.Put(context.Background(), key, "application/zip", []byte("wrong")); err != nil {
		t.Fatal(err)
	}
	if status, _ := rawHTTP(t, "GET", base+"/exports/"+id+"/content", f.admin, "", nil, ""); status != 422 {
		t.Fatalf("tampered zip accepted %d", status)
	}
}
func TestExpiredPendingArtifactsCleanup(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("real S3 required")
	}
	work := create(t, f, f.admin, "stale-attachment")
	data := []byte("stale content")
	sum := sha256.Sum256(data)
	status, item := testRequest(t, "POST", evidenceURL(f)+"/artifacts", f.agent, "stale-pending-artifact", "", map[string]any{
		"target_kind": "work_item", "target_id": work["id"], "filename": "stale.txt", "media_type": "text/plain",
		"size_bytes": len(data), "sha256": hex.EncodeToString(sum[:])})
	if status != 201 {
		t.Fatal(status, item)
	}
	id := item["id"].(string)
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.artifacts SET created_at=now()-interval '3 hours' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	repository := evidencepg.Repository{Pool: f.pool, Objects: f.objects}
	n, err := repository.CleanupStale(context.Background(), f.objects, time.Hour)
	if err != nil || n < 1 {
		t.Fatalf("cleanup: %d %v", n, err)
	}
	status, after := testRequest(t, "GET", evidenceURL(f)+"/artifacts/"+id, f.agent, "", "", nil)
	if status != 200 || after["state"] != "failed" {
		t.Fatalf("stale artifact not marked failed: %d %v", status, after)
	}
	var maintenance int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.artifact_maintenance_events
      WHERE project_id=$1 AND artifact_id=$2`, f.project, id).Scan(&maintenance); err != nil || maintenance != 1 {
		t.Fatalf("missing maintenance journal: count=%d err=%v", maintenance, err)
	}
	if _, err := f.pool.Exec(context.Background(), `DELETE FROM qual.artifact_maintenance_events WHERE artifact_id=$1`, id); err == nil {
		t.Fatal("maintenance journal mutable")
	}
}
