//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPhase5CLIExternalEvidenceAndArchive(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("S3 required for CLI integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "qual")
	builder := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual")
	if out, err := builder.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, out)
	}
	call := func(token string, args ...string) map[string]any {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+token)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v %s", args, err, raw)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("CLI bad JSON %v: %s", err, raw)
		}
		return result
	}
	work := create(t, f, f.agent, "CLI attachment source")
	data := []byte("immutable evidence from external host\n")
	path := filepath.Join(t.TempDir(), "receipt.txt")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	aid := call(f.agent, "evidence", "upload", "-project", f.project.String(),
		"-target-kind", "work_item", "-target", work["id"].(string), "-file", path)["id"].(string)
	listing := call(f.agent, "evidence", "list", "-project", f.project.String(), "-target-kind", "work_item", "-target", work["id"].(string))
	if len(listing["items"].([]any)) != 1 {
		t.Fatalf("CLI evidence list %v", listing)
	}
	outPath := filepath.Join(t.TempDir(), "downloaded.txt")
	download := call(f.viewer, "evidence", "download", "-project", f.project.String(), "-id", aid, "-out", outPath)
	saved, err := os.ReadFile(outPath)
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("CLI download corrupt: %v %v", err, download)
	}
	ref := call(f.agent, "evidence", "ref-create", "-project", f.project.String(), "-target-kind", "work_item",
		"-target", work["id"].(string), "-provider", "git", "-kind", "commit",
		"-external-id", "abc123", "-url", "https://example.com/commit/abc123")
	if ref["provider"] != "git" {
		t.Fatalf("CLI reference failed %v", ref)
	}
	archive := call(f.admin, "archive", "export", "-project", f.project.String())
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	call(f.admin, "archive", "download", "-project", f.project.String(), "-id", archive["id"].(string), "-out", zipPath)
	content, err := os.ReadFile(zipPath)
	if err != nil || len(content) < 100 {
		t.Fatalf("CLI archive is empty: %v", err)
	}
}
