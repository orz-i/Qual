package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// testReportInput is a client-side composition of up to THREE existing,
// independently audited REST operations. It never runs tests, edits CaseVersions
// or pretends the separate writes form one transaction.
type testReportInput struct {
	ProjectID     string
	CaseVersionID string
	WorkItemID    string
	Title         string
	Status        string
	Note          string
	Source        string
	Environment   string
	BuildRef      string
	RequestKey    string
}

func validateTestReport(in testReportInput) (uuid.UUID, error) {
	id, err := uuid.Parse(in.CaseVersionID)
	if err != nil || id == uuid.Nil || id.String() != in.CaseVersionID {
		return uuid.Nil, errors.New("test report requires a canonical -case-version UUID")
	}
	if strings.TrimSpace(in.Title) != in.Title || in.Title == "" || utf8.RuneCountInString(in.Title) > 240 {
		return uuid.Nil, errors.New("test report requires a nonempty -title (max 240 chars)")
	}
	if in.Source == "" || in.Source == "manual" || in.Source == "junit" ||
		strings.TrimSpace(in.Source) != in.Source {
		return uuid.Nil, errors.New("test report must identify a non-manual external source; use test import for JUnit")
	}
	if utf8.RuneCountInString(in.Source) > 80 || utf8.RuneCountInString(in.Environment) > 2000 ||
		utf8.RuneCountInString(in.BuildRef) > 240 {
		return uuid.Nil, errors.New("test report metadata exceeds size limits")
	}
	if in.Status != "passed" && in.Status != "failed" && in.Status != "blocked" && in.Status != "skipped" {
		return uuid.Nil, errors.New("test report -status must be passed|failed|blocked|skipped")
	}
	if strings.TrimSpace(in.Note) == "" || utf8.RuneCountInString(in.Note) > 20000 {
		return uuid.Nil, errors.New("test report requires a factual -note describing actual external observations")
	}
	// Required rather than generated: recovery after the first successful
	// write MUST use the same stable key and unchanged arguments.
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 128 || strings.TrimSpace(in.RequestKey) != in.RequestKey {
		return uuid.Nil, errors.New("test report requires stable -request-key (8..128 chars) for safe retries")
	}
	if in.WorkItemID != "" {
		workID, err := uuid.Parse(in.WorkItemID)
		if err != nil || workID == uuid.Nil || workID.String() != in.WorkItemID {
			return uuid.Nil, errors.New("test report -work-item must be a canonical WorkItem UUID")
		}
	}
	return id, nil
}

// Derived keys are fixed length (within backend 8..128) and purpose-separated.
// Never embed arbitrary request keys into output or forward them to unrelated
// operations. Backend idempotency is per authenticated Principal and operation.
func reportStepKey(root, step string) string {
	sum := sha256.Sum256([]byte("qual-test-report/v1/" + step + "/" + root))
	return "qual-report-" + step + "-" + hex.EncodeToString(sum[:])
}

func validateReportWorkItem(ctx context.Context, c *client, base string, projectID, workItemID uuid.UUID) error {
	raw, status, err := c.do(ctx, "GET", base+"/work-items/"+workItemID.String(), nil, 0, "")
	if err != nil {
		return fmt.Errorf("work-item preflight failed (no TestRun created): %w", err)
	}
	if status != 200 {
		return fmt.Errorf("work-item preflight failed (HTTP %d; no TestRun created)", status)
	}
	var item struct {
		ID        uuid.UUID `json:"id"`
		ProjectID uuid.UUID `json:"project_id"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.ID != workItemID {
		return errors.New("work-item preflight returned mismatched identity; no TestRun created")
	}
	if item.ProjectID != projectID {
		return errors.New("work-item belongs to another Project; no TestRun created")
	}
	return nil
}

func caseCoversWorkItem(ctx context.Context, c *client, base string, workItemID, caseID uuid.UUID) (bool, error) {
	raw, status, err := c.do(ctx, "GET", base+"/work-items/"+workItemID.String()+"/test-coverage", nil, 0, "")
	if err != nil {
		return false, fmt.Errorf("work-item coverage verification failed: %w", err)
	}
	if status != 200 {
		return false, fmt.Errorf("work-item coverage verification failed (HTTP %d)", status)
	}
	var response struct {
		Items []struct {
			ID uuid.UUID `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Items == nil {
		return false, errors.New("work-item coverage response is invalid")
	}
	for _, item := range response.Items {
		if item.ID == caseID {
			return true, nil
		}
	}
	return false, nil
}

func runTestReport(ctx context.Context, c *client, in testReportInput) error {
	caseVersionID, err := validateTestReport(in)
	if err != nil {
		return err
	}
	pid, err := uuid.Parse(in.ProjectID)
	if err != nil || pid == uuid.Nil || pid.String() != in.ProjectID {
		return errors.New("test report requires a resolved project UUID")
	}
	base := "/api/v1/projects/" + pid.String()
	var workItemID uuid.UUID
	if in.WorkItemID != "" {
		workItemID, _ = uuid.Parse(in.WorkItemID) // canonical UUID was checked above
		if err := validateReportWorkItem(ctx, c, base, pid, workItemID); err != nil {
			return err
		}
	}
	rawRun, _, err := c.do(ctx, "POST", base+"/test-runs", map[string]any{
		"name": in.Title, "source_system": in.Source,
		"environment": in.Environment, "build_ref": in.BuildRef,
		"case_version_ids": []string{caseVersionID.String()},
	}, 0, reportStepKey(in.RequestKey, "run"))
	if err != nil {
		return fmt.Errorf("test report run creation failed (no attempt submitted): %w", err)
	}
	var run struct {
		ID        uuid.UUID `json:"id"`
		ProjectID uuid.UUID `json:"project_id"`
		Items     []struct {
			ID            uuid.UUID `json:"id"`
			TestCaseID    uuid.UUID `json:"test_case_id"`
			CaseVersionID uuid.UUID `json:"case_version_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rawRun, &run); err != nil || run.ID == uuid.Nil ||
		run.ProjectID != pid || len(run.Items) != 1 ||
		run.Items[0].ID == uuid.Nil || run.Items[0].TestCaseID == uuid.Nil || run.Items[0].CaseVersionID != caseVersionID {
		return errors.New("test report run response had unexpected project/case identity; attempt NOT submitted")
	}
	itemID := run.Items[0].ID
	rawAttempt, _, err := c.do(ctx, "POST", base+"/test-run-items/"+itemID.String()+"/attempts", map[string]any{
		"status": in.Status, "note": in.Note, "source_kind": "external",
	}, 0, reportStepKey(in.RequestKey, "attempt"))
	if err != nil {
		return fmt.Errorf("test report PARTIAL: run %s exists, attempt not confirmed (%v); retry same -request-key and unchanged inputs", run.ID, err)
	}
	var attempt struct {
		ID        uuid.UUID `json:"id"`
		RunItemID uuid.UUID `json:"run_item_id"`
		AttemptNo int       `json:"attempt_no"`
		Status    string    `json:"status"`
		Source    string    `json:"source_kind"`
	}
	if err := json.Unmarshal(rawAttempt, &attempt); err != nil || attempt.ID == uuid.Nil ||
		attempt.RunItemID != itemID || attempt.AttemptNo < 1 || attempt.Status != in.Status || attempt.Source != "external" {
		return fmt.Errorf("test report PARTIAL: run %s exists, attempt response cannot be verified; retry same -request-key and inputs", run.ID)
	}
	if in.WorkItemID != "" {
		coverageBody, _, linkErr := c.do(ctx, "POST", base+"/test-cases/"+run.Items[0].TestCaseID.String()+"/coverage",
			map[string]any{"work_item_id": workItemID}, 0, reportStepKey(in.RequestKey, "coverage"))
		if linkErr == nil {
			var linkedCase struct {
				ID uuid.UUID `json:"id"`
			}
			if err := json.Unmarshal(coverageBody, &linkedCase); err != nil || linkedCase.ID != run.Items[0].TestCaseID {
				linkErr = errors.New("coverage response did not identify the expected TestCase")
			}
		}
		if linkErr != nil {
			// A racing reporter may have linked it already, or the server may
			// have committed before the response was lost. Verify before failing.
			linked, verifyErr := caseCoversWorkItem(ctx, c, base, workItemID, run.Items[0].TestCaseID)
			if verifyErr != nil || !linked {
				return fmt.Errorf("test report PARTIAL: run %s and attempt %s exist, WorkItem coverage is not confirmed (%v); retry same -request-key and unchanged inputs",
					run.ID, attempt.ID, linkErr)
			}
		}
	}
	// One concise receipt: durable identifiers, not a fabricated execution log.
	receipt := map[string]any{
		"test_run_id": run.ID, "run_item_id": itemID, "attempt_id": attempt.ID,
		"attempt_no": attempt.AttemptNo, "case_version_id": caseVersionID,
		"status": attempt.Status, "source_kind": "external",
	}
	if in.WorkItemID != "" {
		receipt["work_item_id"] = workItemID
		receipt["coverage_linked"] = true
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}
