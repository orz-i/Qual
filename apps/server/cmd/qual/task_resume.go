package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type basicResumeTask struct {
	ID                 uuid.UUID `json:"id"`
	Key                string    `json:"key"`
	Version            int64     `json:"version"`
	Description        string    `json:"description"`
	AcceptanceCriteria []string  `json:"acceptance_criteria"`
}

// readBasicResumeTask uses only existing project-authorized WorkItem GET.
// It cannot read private WorkContext, TestRuns, Artifacts or Agent sessions.
func readBasicResumeTask(ctx context.Context, c *client, path string) (json.RawMessage, basicResumeTask, error) {
	var task basicResumeTask
	raw, _, err := c.do(ctx, "GET", path, nil, 0, "")
	if err != nil {
		return nil, task, &exitError{code: 3, cause: fmt.Errorf("resume Task read: %w", err)}
	}
	if json.Unmarshal(raw, &task) != nil || task.ID == uuid.Nil || task.Key == "" ||
		task.Version < 1 || !strings.HasSuffix(path, "/"+task.ID.String()) {
		return nil, task, errors.New("invalid authorized WorkItem response")
	}
	return raw, task, nil
}

// Older WorkContext v2 installations omit the full Task contract. Hydrate
// only the public fields from a verified matching WorkItem version.
func hydrateResumeTask(ctx context.Context, c *client, path string, version int64, summary map[string]json.RawMessage) error {
	_, task, err := readBasicResumeTask(ctx, c, path)
	if err != nil {
		return err
	}
	if task.Version != version {
		return errors.New("CONTEXT_CHANGED: WorkItem version changed during resume; retry")
	}
	description, _ := json.Marshal(task.Description)
	criteria := task.AcceptanceCriteria
	if criteria == nil {
		criteria = []string{}
	}
	acceptance, _ := json.Marshal(criteria)
	summary["description"] = description
	summary["acceptance_criteria"] = acceptance
	return nil
}

// When private overview returns 403, a Candidate can still read the public
// WorkItem description via its own work:read. Do not query ANY private section.
func runBasicTaskResume(ctx context.Context, c *client, path string) error {
	taskRaw, task, err := readBasicResumeTask(ctx, c, path)
	if err != nil {
		return err
	}
	var capabilities json.RawMessage
	omitted := map[string]string{}
	for _, section := range []string{"overview", "dependencies", "comments", "reviews", "acceptance", "coverage",
		"attempts", "historical_attributions", "artifacts", "references", "threads"} {
		omitted[section] = "not_authorized"
	}
	capsRaw, status, err := c.do(ctx, "GET", path+"/capabilities", nil, 0, "")
	switch status {
	case 200:
		if err != nil {
			return err
		}
		var version struct {
			WorkItemID  uuid.UUID `json:"work_item_id"`
			WorkVersion int64     `json:"work_version"`
		}
		if json.Unmarshal(capsRaw, &version) != nil ||
			version.WorkItemID != task.ID || version.WorkVersion < 1 {
			return errors.New("invalid Task capability projection")
		}
		if version.WorkVersion != task.Version {
			return errors.New("CONTEXT_CHANGED: WorkItem version changed during resume; retry")
		}
		capabilities = capsRaw
	case 403:
		omitted["capabilities"] = "not_authorized"
	case 503:
		omitted["capabilities"] = "bounded_source_read_unavailable"
	default:
		if err == nil {
			return fmt.Errorf("resume capabilities returned unexpected HTTP %d", status)
		}
		return &exitError{code: 3, cause: fmt.Errorf("resume capabilities: %w", err)}
	}
	out := map[string]any{
		"schema_version":   2,
		"context_revision": task.Version,
		"generated_at":     time.Now().UTC(),
		"consistency":      "best_effort",
		"basic_only":       true,
		"work_item":        taskRaw,
		"capabilities":     capabilities,
		"sections":         map[string]any{},
		"omitted_sections": omitted,
		"warnings": []string{
			"Public Task description and acceptance are readable; private Context remains denied until a legitimate Task relationship exists.",
			"Only persisted source facts are shown; Task status never proves Git, TestAttempt or AgentRun success.",
			"Refresh Task/version and permissions before any write; obtain Claim or authorized Reassign rather than bypassing 403.",
			"No cross-domain snapshot guarantee; Qual never starts or resumes Agent processes.",
		},
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return printJSON(encoded)
}
