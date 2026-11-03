package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// A packet is one immutable, Ready S3 Artifact bound to a real WorkItem.
// The external CLI reads a local Git object and already persisted, authorized
// Qual Run/Attempt/Coverage facts. Qual never starts Git work, tests or Agents.
type workEvidencePacketInput struct {
	ProjectID  string
	WorkItemID string
	TestRunID  string
	GitCommit  string
	GitRepo    string
	RequestKey string
}

type workEvidenceCase struct {
	TestCaseID     uuid.UUID `json:"test_case_id"`
	TestCaseKey    string    `json:"test_case_key"`
	CaseVersionID  uuid.UUID `json:"case_version_id"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	AttemptCount   int       `json:"attempt_count"`
	LatestStatus   string    `json:"latest_status"`
}

type workEvidenceManifest struct {
	SchemaVersion   int                `json:"schema_version"`
	Source          string             `json:"source"`
	GitVerification string             `json:"git_verification"`
	ProjectID       uuid.UUID          `json:"project_id"`
	WorkItemID      uuid.UUID          `json:"work_item_id"`
	GitCommit       string             `json:"git_commit"`
	TestRunID       uuid.UUID          `json:"test_run_id"`
	CaseVersions    []workEvidenceCase `json:"case_versions"`
}

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
var packetHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

const packetFilename = "qual-work-evidence-v1.json"

// Only accept full Git object IDs. Never interpolate untrusted strings in a
// shell. -C points to an external repository; cat-file -t is read-only and
// does not run Git hooks or invoke a network transport.
func verifyLocalGitCommit(ctx context.Context, repo, sha string) error {
	if !commitSHA.MatchString(sha) || repo == "" {
		return errors.New("evidence packet requires a full lowercase 40/64-hex Git commit and local -git-repo")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-t", sha)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "commit" {
		// Never propagate git stderr, local paths, env, or executable errors.
		return errors.New("provided Git SHA is not a locally available commit object")
	}
	return nil
}

func validatePacketInput(in workEvidencePacketInput) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	pid, pErr := uuid.Parse(in.ProjectID)
	wid, wErr := uuid.Parse(in.WorkItemID)
	rid, rErr := uuid.Parse(in.TestRunID)
	if pErr != nil || wErr != nil || rErr != nil || pid == uuid.Nil || wid == uuid.Nil ||
		rid == uuid.Nil || pid.String() != in.ProjectID ||
		wid.String() != in.WorkItemID || rid.String() != in.TestRunID {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("evidence packet requires canonical Project, WorkItem and TestRun UUIDs")
	}
	if !commitSHA.MatchString(in.GitCommit) || in.GitRepo == "" {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("evidence packet requires full lowercase local Git commit and -git-repo")
	}
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 128 ||
		strings.TrimSpace(in.RequestKey) != in.RequestKey {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("evidence packet requires stable -request-key (8..128 chars) for safe retries")
	}
	return pid, wid, rid, nil
}

// Exactly one deterministic multi-stage upload key for this request event.
// The server will reject a changed manifest submitted under the same key.
func packetUploadKey(key string) string {
	digest := sha256.Sum256([]byte("qual-work-evidence-packet/v1/" + key))
	return "qual-packet-" + hex.EncodeToString(digest[:])
}

func runWorkEvidencePacket(ctx context.Context, c *client, in workEvidencePacketInput) error {
	pid, wid, rid, err := validatePacketInput(in)
	if err != nil {
		return err
	}
	if err := verifyLocalGitCommit(ctx, in.GitRepo, in.GitCommit); err != nil {
		return err
	}
	base := "/api/v1/projects/" + pid.String()
	// All preflight reads use the caller's actual scope+Project permissions,
	// before any upload mutation. Reuse the same already-audited REST paths.
	if err := validateReportWorkItem(ctx, c, base, pid, wid); err != nil {
		return fmt.Errorf("evidence packet WorkItem preflight failed: %w", err)
	}
	raw, _, err := c.do(ctx, "GET", base+"/test-runs/"+rid.String(), nil, 0, "")
	if err != nil {
		return fmt.Errorf("evidence packet TestRun preflight failed: %w", err)
	}
	var run struct {
		ID        uuid.UUID          `json:"id"`
		ProjectID uuid.UUID          `json:"project_id"`
		BuildRef  string             `json:"build_ref"`
		Items     []workEvidenceCase `json:"items"`
	}
	if err := json.Unmarshal(raw, &run); err != nil ||
		run.ID != rid || run.ProjectID != pid || len(run.Items) == 0 || len(run.Items) > 100 {
		return errors.New("evidence packet received mismatched or empty TestRun; no Artifact created")
	}
	// A local commit is not sufficient evidence of THIS TestRun's build.
	// Require the immutable reported Run build reference to identify it.
	if len(run.BuildRef) < 7 || !strings.HasPrefix(in.GitCommit, run.BuildRef) {
		return errors.New("TestRun build_ref does not match the verified local Git commit; no Artifact created")
	}
	seen := map[uuid.UUID]bool{}
	for _, item := range run.Items {
		if item.TestCaseID == uuid.Nil || item.CaseVersionID == uuid.Nil ||
			item.TestCaseKey == "" || !packetHash.MatchString(item.SnapshotSHA256) ||
			item.AttemptCount < 1 || seen[item.TestCaseID] ||
			(item.LatestStatus != "passed" && item.LatestStatus != "failed" &&
				item.LatestStatus != "blocked" && item.LatestStatus != "skipped") {
			return errors.New("TestRun has an unexecuted or malformed CaseVersion; no Artifact created")
		}
		seen[item.TestCaseID] = true
		covered, err := caseCoversWorkItem(ctx, c, base, wid, item.TestCaseID)
		if err != nil || !covered {
			return errors.New("TestRun CaseVersion is not covered by this WorkItem; no Artifact created")
		}
	}
	// Stable serialization supports retries without inventing a timestamp or
	// new evidence identities. Actual server artifact Actor/time are preserved
	// by the authoritative append-only Audit/Activity/Outbox.
	sort.Slice(run.Items, func(i, j int) bool {
		return run.Items[i].CaseVersionID.String() < run.Items[j].CaseVersionID.String()
	})
	manifest := workEvidenceManifest{
		SchemaVersion: 1, Source: "external-qual-cli",
		GitVerification: "local-git-cat-file-commit",
		ProjectID:       pid, WorkItemID: wid, GitCommit: in.GitCommit,
		TestRunID: rid, CaseVersions: run.Items,
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return errors.New("could not serialize deterministic WorkItem evidence")
	}
	payload = append(payload, '\n')
	readyRaw, err := c.artifactUpload(ctx, base, "work_item", wid, packetFilename,
		"application/json", payload, packetUploadKey(in.RequestKey))
	if err != nil {
		return err
	}
	var ready struct {
		ID        uuid.UUID `json:"id"`
		SHA256    string    `json:"sha256"`
		SizeBytes int64     `json:"size_bytes"`
		State     string    `json:"state"`
	}
	if err := json.Unmarshal(readyRaw, &ready); err != nil || ready.ID == uuid.Nil ||
		ready.State != "ready" {
		return errors.New("uploaded WorkItem Artifact receipt was not verified")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"artifact_id": ready.ID, "artifact_state": ready.State,
		"artifact_sha256": ready.SHA256, "artifact_size_bytes": ready.SizeBytes,
		"work_item_id": wid, "test_run_id": rid, "git_commit": in.GitCommit,
		"case_versions": len(run.Items), "git_local_verified": true,
	})
}
