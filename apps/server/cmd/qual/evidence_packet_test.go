package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func fixtureGitCommit(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("disposable Git fixture command failed: %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "receipt.txt"), []byte("verified commit for test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "receipt.txt")
	run("-c", "user.name=Qual test", "-c", "user.email=qual-test@example.invalid",
		"commit", "-q", "-m", "create read-only fixture commit")
	return dir, run("rev-parse", "HEAD")
}

func TestPacketInputAndReadOnlyGitValidation(t *testing.T) {
	dir, sha := fixtureGitCommit(t)
	in := workEvidencePacketInput{
		ProjectID: uuid.NewString(), WorkItemID: uuid.NewString(),
		TestRunID: uuid.NewString(), GitRepo: dir, GitCommit: sha,
		RequestKey: "qual-packet-fixture-key",
	}
	if _, _, _, err := validatePacketInput(in); err != nil {
		t.Fatal(err)
	}
	if err := verifyLocalGitCommit(context.Background(), dir, sha); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name   string
		mutate func(*workEvidencePacketInput)
	}{
		{"bad UUID", func(x *workEvidencePacketInput) { x.WorkItemID = "NOT-A-UUID" }},
		{"invalid SHA", func(x *workEvidencePacketInput) { x.GitCommit = "deadbeef" }},
		{"uppercase SHA", func(x *workEvidencePacketInput) { x.GitCommit = strings.ToUpper(sha) }},
		{"missing repo", func(x *workEvidencePacketInput) { x.GitRepo = "" }},
		{"unstable key", func(x *workEvidencePacketInput) { x.RequestKey = "" }},
	} {
		t.Run(v.name, func(t *testing.T) {
			bad := in
			v.mutate(&bad)
			if _, _, _, err := validatePacketInput(bad); err == nil {
				t.Fatal("invalid packet accepted")
			}
		})
	}
	if err := verifyLocalGitCommit(context.Background(), dir, strings.Repeat("0", 40)); err == nil {
		t.Fatal("nonexistent commit accepted")
	}
	blobCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD:receipt.txt")
	blob, err := blobCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyLocalGitCommit(context.Background(), dir, strings.TrimSpace(string(blob))); err == nil {
		t.Fatal("Git blob was incorrectly accepted as commit")
	}
}

type packetMock struct {
	project, work, run, caseID, caseVersion, runItem, artifact uuid.UUID
	sha, build                                                 string
	covered                                                    bool
	attempts                                                   int
	runProject                                                 uuid.UUID
	content                                                    []byte
	beginKeys, byteKeys, completeKeys                          []string
	artifactWrites                                             int
	badBeginProject, badReadyProject                           bool
}

func (h *packetMock) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-agent-pat" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	base := "/api/v1/projects/" + h.project.String()
	write := func(code int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			panic(err)
		}
	}
	switch {
	case r.Method == "GET" && r.URL.Path == base+"/work-items/"+h.work.String():
		write(200, map[string]any{"id": h.work, "project_id": h.project})
	case r.Method == "GET" && r.URL.Path == base+"/test-runs/"+h.run.String():
		write(200, map[string]any{
			"id": h.run, "project_id": h.runProject, "build_ref": h.build,
			"items": []any{map[string]any{
				"id": h.runItem, "test_case_id": h.caseID, "test_case_key": "TEST-TC-1",
				"case_version_id": h.caseVersion, "snapshot_sha256": h.sha,
				"latest_status": "passed", "attempt_count": h.attempts,
			}},
		})
	case r.Method == "GET" && r.URL.Path == base+"/work-items/"+h.work.String()+"/test-coverage":
		items := []any{}
		if h.covered {
			items = append(items, map[string]any{"id": h.caseID, "key": "TEST-TC-1"})
		}
		write(200, map[string]any{"items": items})
	case r.Method == "POST" && r.URL.Path == base+"/artifacts":
		h.artifactWrites++
		h.beginKeys = append(h.beginKeys, r.Header.Get("Idempotency-Key"))
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			panic(err)
		}
		if b["target_kind"] != "work_item" || b["target_id"] != h.work.String() ||
			b["filename"] != packetFilename || b["media_type"] != "application/json" {
			panic("artifact was not bound to the expected WorkItem")
		}
		projectID := h.project
		if h.badBeginProject {
			projectID = uuid.New()
		}
		write(201, map[string]any{
			"id": h.artifact, "project_id": projectID,
			"target_kind": "work_item", "target_id": h.work,
			"sha256": b["sha256"],
		})
	case r.Method == "PUT" && r.URL.Path == base+"/artifacts/"+h.artifact.String()+"/content":
		h.artifactWrites++
		h.byteKeys = append(h.byteKeys, r.Header.Get("Idempotency-Key"))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		if h.content != nil && string(h.content) != string(body) {
			panic("replayed manifest changed bytes")
		}
		h.content = body
		write(200, map[string]any{"id": h.artifact, "state": "uploaded"})
	case r.Method == "POST" && r.URL.Path == base+"/artifacts/"+h.artifact.String()+"/complete":
		h.artifactWrites++
		h.completeKeys = append(h.completeKeys, r.Header.Get("Idempotency-Key"))
		hash := sha256.Sum256(h.content)
		projectID := h.project
		if h.badReadyProject {
			projectID = uuid.New()
		}
		write(200, map[string]any{
			"id": h.artifact, "project_id": projectID,
			"target_kind": "work_item", "target_id": h.work,
			"size_bytes": len(h.content), "sha256": hex.EncodeToString(hash[:]),
			"state": "ready",
		})
	default:
		panic(fmt.Sprintf("unexpected packet call: %s %s", r.Method, r.URL.Path))
	}
}

func packetHarness(t *testing.T) (*client, *packetMock, workEvidencePacketInput) {
	t.Helper()
	repo, sha := fixtureGitCommit(t)
	h := &packetMock{
		project: uuid.New(), work: uuid.New(), run: uuid.New(), caseID: uuid.New(),
		caseVersion: uuid.New(), runItem: uuid.New(), artifact: uuid.New(),
		sha: strings.Repeat("a", 64), build: sha, covered: true, attempts: 1,
	}
	h.runProject = h.project
	srv := httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(srv.Close)
	c := &client{base: srv.URL, bearer: "test-agent-pat", http: srv.Client()}
	in := workEvidencePacketInput{
		ProjectID: h.project.String(), WorkItemID: h.work.String(),
		TestRunID: h.run.String(), GitCommit: sha, GitRepo: repo,
		RequestKey: "qual-evidence-packet-replay-stable",
	}
	return c, h, in
}

func TestPacketComposesVerifiedWorkItemArtifactAndReplaysIdentically(t *testing.T) {
	c, h, in := packetHarness(t)
	if err := runWorkEvidencePacket(context.Background(), c, in); err != nil {
		t.Fatal(err)
	}
	if err := runWorkEvidencePacket(context.Background(), c, in); err != nil {
		t.Fatal(err)
	}
	if h.artifactWrites != 6 || len(h.beginKeys) != 2 || len(h.byteKeys) != 2 || len(h.completeKeys) != 2 ||
		h.beginKeys[0] != h.beginKeys[1] || h.byteKeys[0] != h.byteKeys[1] ||
		h.completeKeys[0] != h.completeKeys[1] || h.beginKeys[0] == h.byteKeys[0] {
		t.Fatal("idempotent packet upload did not replay identical three distinct stages")
	}
	var manifest workEvidenceManifest
	if err := json.Unmarshal(h.content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.WorkItemID != h.work || manifest.ProjectID != h.project ||
		manifest.TestRunID != h.run || manifest.GitCommit != in.GitCommit ||
		manifest.GitVerification != "local-git-cat-file-commit" ||
		len(manifest.CaseVersions) != 1 || manifest.CaseVersions[0].CaseVersionID != h.caseVersion ||
		manifest.CaseVersions[0].SnapshotSHA256 != h.sha || manifest.CaseVersions[0].AttemptCount != 1 {
		t.Fatalf("packet manifest lost provenance: %+v", manifest)
	}
}
func TestPacketRejectsUncoveredAndUnexecutedRunWithoutS3Writes(t *testing.T) {
	for _, name := range []string{"missing coverage", "no Attempt", "different build", "other project"} {
		t.Run(name, func(t *testing.T) {
			c, h, in := packetHarness(t)
			switch name {
			case "missing coverage":
				h.covered = false
			case "no Attempt":
				h.attempts = 0
			case "different build":
				h.build = strings.Repeat("f", 40)
			case "other project":
				h.runProject = uuid.New()
			}
			if err := runWorkEvidencePacket(context.Background(), c, in); err == nil || h.artifactWrites != 0 {
				t.Fatalf("%s accepted with %d Artifact writes", name, h.artifactWrites)
			}
		})
	}
}

func TestPacketNeverTrustsCrossProjectArtifactResponses(t *testing.T) {
	for _, stage := range []string{"begin", "ready"} {
		t.Run(stage, func(t *testing.T) {
			c, h, input := packetHarness(t)
			if stage == "begin" {
				h.badBeginProject = true
			} else {
				h.badReadyProject = true
			}
			err := runWorkEvidencePacket(context.Background(), c, input)
			if err == nil {
				t.Fatal("cross-project artifact response must never be reported Ready")
			}
			if stage == "begin" && h.artifactWrites != 1 {
				t.Fatal("untrusted begin should not upload bytes")
			}
			if stage == "ready" && (h.artifactWrites != 3 || !strings.Contains(err.Error(), "PARTIAL")) {
				t.Fatalf("invalid Ready must report partial, not success: %v", err)
			}
		})
	}
}
