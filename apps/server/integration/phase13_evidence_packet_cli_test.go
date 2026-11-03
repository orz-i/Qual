//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This is a disposable PostgreSQL + actual S3 + real HTTP/CLI test. No
// business writes are allowed in the user's live/default QUAL database.
func TestPhase13WorkItemEvidencePacketUsesRealGitRunAndS3(t *testing.T) {
	databaseURL := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(databaseURL, "/qual13_packet_") {
		t.Skip("requires disposable qual13_packet_* migrated PostgreSQL database")
	}
	f := setup(t)
	if f.objects == nil {
		t.Skip("requires isolated S3 test fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	work := create(t, f, f.agent, "phase13-real-packet-work-item")
	workID := work["id"].(string)
	caseID, _ := mustCase(t, f, "phase13-case", "Actually reported external TestRun")
	versionID, version := publishCase(t, f, caseID, "phase13-publish", 1)
	if version != 1 {
		t.Fatal("immutable CaseVersion changed")
	}
	code, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage",
		f.agent, "phase13-case-work-coverage", "", map[string]any{"work_item_id": workID})
	if code != 201 {
		t.Fatalf("could not link CaseVersion coverage, status=%d", code)
	}

	// Only an external Git host performs the commit. Qual CLI will later
	// inspect this already-created Git object using read-only cat-file.
	repo := filepath.Join(t.TempDir(), "source-repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic external Git setup failed (output omitted): %v", err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "unit-test.txt"), []byte("actual fixture assertion\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "unit-test.txt")
	git("-c", "user.name=Qual integration", "-c", "user.email=qual-integration@example.invalid",
		"commit", "-q", "-m", "fixture result")
	sha := git("rev-parse", "HEAD")
	if len(sha) != 40 {
		t.Fatal("unsupported disposable Git commit format")
	}
	code, run := testRequest(t, "POST", testsURL(f)+"/test-runs", f.agent,
		"phase13-real-external-run", "", map[string]any{
			"name": "actual external fixture run", "source_system": "pi-integration-host",
			"environment": "disposable postgres and seaweedfs", "build_ref": sha,
			"case_version_ids": []string{versionID},
		})
	if code != 201 {
		t.Fatalf("could not create fixture TestRun: %d", code)
	}
	runID := run["id"].(string)
	items := run["items"].([]any)
	if len(items) != 1 {
		t.Fatal("one frozen CaseVersion expected")
	}
	runItemID := items[0].(map[string]any)["id"].(string)
	// The old failure must remain after the later passing observation.
	failed := mustAttempt(t, f, runItemID, f.agent, "phase13-failed", "failed")
	passed := mustAttempt(t, f, runItemID, f.agent, "phase13-later-passed", "passed")
	if failed["id"] == passed["id"] {
		t.Fatal("immutable Failed and Passed Attempts were overwritten")
	}

	bin := filepath.Join(t.TempDir(), "qual")
	builder := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual")
	if _, err := builder.CombinedOutput(); err != nil {
		t.Fatalf("build external CLI failed (output omitted): %v", err)
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "QUAL_TOKEN=") && !strings.HasPrefix(v, "QUAL_API_URL=") {
			env = append(env, v)
		}
	}
	env = append(env, "QUAL_TOKEN="+f.agent, "QUAL_API_URL="+f.server.URL)
	call := func(args ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	packetArgs := []string{"evidence", "packet", "-project", f.project.String(),
		"-work-item", workID, "-test-run", runID, "-git-commit", sha,
		"-git-repo", repo, "-request-key", "phase13-one-immutable-work-packet-event-1"}
	raw, err := call(packetArgs...)
	if err != nil {
		t.Fatalf("external evidence packet failed (output redacted): %v", err)
	}
	var receipt struct {
		ArtifactID     uuid.UUID `json:"artifact_id"`
		ArtifactState  string    `json:"artifact_state"`
		ArtifactSHA256 string    `json:"artifact_sha256"`
		WorkItemID     uuid.UUID `json:"work_item_id"`
		GitCommit      string    `json:"git_commit"`
		TestRunID      uuid.UUID `json:"test_run_id"`
		CaseVersions   int       `json:"case_versions"`
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatalf("invalid evidence packet receipt (output omitted): %v", err)
	}
	if receipt.ArtifactID == uuid.Nil || receipt.ArtifactState != "ready" ||
		receipt.WorkItemID.String() != workID || receipt.TestRunID.String() != runID ||
		receipt.GitCommit != sha || receipt.CaseVersions != 1 || len(receipt.ArtifactSHA256) != 64 {
		t.Fatal("actual packet identity, readiness or SHA missing")
	}
	if strings.Contains(string(raw), f.agent) {
		t.Fatal("packet leaked bearer credential")
	}
	rawReplay, err := call(packetArgs...)
	if err != nil || string(rawReplay) != string(raw) {
		t.Fatal("same key and inputs did not replay identical Artifact identity")
	}
	artifactID := receipt.ArtifactID.String()
	code, listing := testRequest(t, "GET", testsURL(f)+"/artifacts?target_kind=work_item&target_id="+workID,
		f.agent, "", "", nil)
	if code != 200 || len(listing["items"].([]any)) != 1 {
		t.Fatal("WorkItem Artifact listing contains missing/duplicate entries")
	}
	listed := listing["items"].([]any)[0].(map[string]any)
	if listed["id"] != artifactID || listed["state"] != "ready" ||
		listed["sha256"] != receipt.ArtifactSHA256 {
		t.Fatal("listed Ready Artifact does not match packet")
	}
	var objectKey string
	var actualPrincipal uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT object_key FROM qual.artifacts WHERE id=$1 AND project_id=$2`,
		receipt.ArtifactID, f.project).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT principal_id FROM qual.credentials WHERE id=$1`,
		f.agentCredential).Scan(&actualPrincipal); err != nil {
		t.Fatal(err)
	}
	if listed["created_by"] != actualPrincipal.String() {
		t.Fatal("immutable Artifact actor is not the actual PI Agent Principal")
	}
	// S3 fixture is shared; remove only this scratch test object afterwards.
	t.Cleanup(func() { _ = f.objects.Delete(context.Background(), objectKey) })
	downloaded := filepath.Join(t.TempDir(), "downloaded-packet.json")
	if _, err := call("evidence", "download", "-project", f.project.String(),
		"-id", artifactID, "-out", downloaded); err != nil {
		t.Fatalf("authenticated SHA-verified packet download failed: %v", err)
	}
	body, err := os.ReadFile(downloaded)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != receipt.ArtifactSHA256 {
		t.Fatal("S3 bytes have a different SHA-256 than Ready Artifact")
	}
	var document struct {
		SchemaVersion int       `json:"schema_version"`
		ProjectID     uuid.UUID `json:"project_id"`
		WorkItemID    uuid.UUID `json:"work_item_id"`
		GitCommit     string    `json:"git_commit"`
		TestRunID     uuid.UUID `json:"test_run_id"`
		CaseVersions  []struct {
			CaseVersionID  uuid.UUID `json:"case_version_id"`
			SnapshotSHA256 string    `json:"snapshot_sha256"`
			AttemptCount   int       `json:"attempt_count"`
			LatestStatus   string    `json:"latest_status"`
		} `json:"case_versions"`
	}
	if err := json.Unmarshal(body, &document); err != nil ||
		document.SchemaVersion != 1 || document.ProjectID != f.project ||
		document.WorkItemID.String() != workID || document.TestRunID.String() != runID ||
		document.GitCommit != sha || len(document.CaseVersions) != 1 ||
		document.CaseVersions[0].CaseVersionID.String() != versionID ||
		document.CaseVersions[0].AttemptCount != 2 ||
		document.CaseVersions[0].LatestStatus != "passed" ||
		len(document.CaseVersions[0].SnapshotSHA256) != 64 {
		t.Fatal("S3 packet failed Git/CaseVersion/Attempt provenance round trip")
	}
	code, attempts := testRequest(t, "GET", attemptURL(f, runItemID), f.agent, "", "", nil)
	if code != 200 || len(attempts["items"].([]any)) != 2 ||
		attempts["items"].([]any)[1].(map[string]any)["status"] != "failed" {
		t.Fatal("packet operation overwrote previous Failed Attempt")
	}
	// A missing work coverage, nonexistent Git object, or unscoped project
	// cannot create a second Artifact on the original WorkItem.
	uncovered := create(t, f, f.agent, "uncovered work item")
	uncoveredArgs := append([]string{}, packetArgs...)
	for i := range uncoveredArgs {
		if uncoveredArgs[i] == workID {
			uncoveredArgs[i] = uncovered["id"].(string)
		}
	}
	uncoveredArgs[len(uncoveredArgs)-1] = "phase13-uncovered-packet-event"
	if _, err := call(uncoveredArgs...); err == nil {
		t.Fatal("packet accepted a Run with no WorkItem Coverage")
	}
	invalidGit := append([]string{}, packetArgs...)
	for i := range invalidGit {
		if invalidGit[i] == sha {
			invalidGit[i] = strings.Repeat("0", 40)
		}
	}
	invalidGit[len(invalidGit)-1] = "phase13-invalid-git-object"
	if _, err := call(invalidGit...); err == nil {
		t.Fatal("packet accepted a missing local Git commit")
	}
	code, listing = testRequest(t, "GET", testsURL(f)+"/artifacts?target_kind=work_item&target_id="+workID,
		f.agent, "", "", nil)
	if code != 200 || len(listing["items"].([]any)) != 1 {
		t.Fatal("negative/retry actions created extra Artifacts")
	}
}
