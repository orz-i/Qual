//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

// Governance history is an immutable project export, not a summary inferred
// from WorkItem Done. Goose12 archive remains backward compatible.
func TestPhase11ArchiveContainsRealReviewGovernanceWithoutFabrication(t *testing.T) {
	gate13(t)
	f := setup(t)
	if f.objects == nil {
		t.Skip("local private S3 must be configured")
	}
	authorizeOwnerPolicy(t, f, "governance-export")
	run := reviewRun(t, f, "governance-export")
	id := reviewTarget(t, f, f.admin, "governance-export-owner", run)
	code, done, _ := request(t, "POST", completeURL(f, id), f.admin, "governance-export-done", "",
		reviewCompletion(run, 4, "owner_acceptance", true))
	if code != 200 || done["status"] != "done" {
		t.Fatalf("Owner fixture Done not persisted %d %#v", code, done)
	}
	other := create(t, f, f.agent, "governance export WorkItem independent override")
	wpath := f.url(f.project) + "/" + other["id"].(string) + "/review-policy"
	code, _, _ = request(t, "PUT", wpath, f.admin, "governance-export-override", "",
		map[string]any{"mode": "independent_review", "expected_version": 1, "expected_policy_version": 0,
			"reason": "Human Administrator requires independent review for this WorkItem"})
	if code != 200 {
		t.Fatalf("work item override for archive not recorded %d", code)
	}
	base := f.server.URL + "/api/v1/projects/" + f.project.String() + "/exports"
	if code, _, _ := request(t, "POST", base, f.agent, "governance-export-agent-denied", "", nil); code != 403 {
		t.Fatalf("Agent could export governance history %d", code)
	}
	status, exp, _ := request(t, "POST", base, f.admin, "governance-export-create", "", nil)
	if status != 201 {
		t.Fatalf("Human scoped export failed %d %#v", status, exp)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/"+exp["id"].(string)+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.admin)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("export GET status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 33*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = evidencepg.VerifyArchive(raw); err != nil {
		t.Fatalf("signed project export invalid: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.Name == "project_review_policies.jsonl" ||
			entry.Name == "work_review_policy_overrides.jsonl" ||
			entry.Name == "review_policy_changes.jsonl" ||
			entry.Name == "work_acceptance_decisions.jsonl" {
			input, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(input)
			_ = input.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[entry.Name] = b
		}
	}
	if len(files) != 4 {
		t.Fatalf("Goose13 project ZIP is missing review governance tables %v", files)
	}
	var decision map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(files["work_acceptance_decisions.jsonl"]), &decision); err != nil {
		t.Fatal(err)
	}
	if decision["acceptance_mode"] != "owner_acceptance" || decision["independent"] != false ||
		decision["explicit_self_review"] != true || decision["work_item_id"] != id {
		t.Fatalf("ZIP fabricated Owner approval: %v", decision)
	}
	if !bytes.Contains(files["work_review_policy_overrides.jsonl"], []byte(other["id"].(string))) ||
		!bytes.Contains(files["review_policy_changes.jsonl"], []byte(other["id"].(string))) {
		t.Fatal("per-WorkItem override or append-only changes missing from archive")
	}
}
