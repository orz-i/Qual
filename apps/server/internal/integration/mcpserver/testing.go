package mcpserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qual/internal/testing/domain"
)

type testProjectArgs struct {
	ProjectID string `json:"project_id"`
}
type testCaseArgs struct {
	ProjectID string `json:"project_id"`
	CaseID    string `json:"case_id"`
}
type testRunArgs struct {
	ProjectID string `json:"project_id"`
	RunID     string `json:"run_id"`
}
type testItemArgs struct {
	ProjectID string `json:"project_id"`
	ItemID    string `json:"item_id"`
}
type createTestCaseArgs struct {
	ProjectID      string               `json:"project_id"`
	IdempotencyKey string               `json:"idempotency_key"`
	Specification  domain.Specification `json:"specification"`
	SuiteID        string               `json:"suite_id,omitempty"`
}
type publishTestCaseArgs struct {
	ProjectID        string `json:"project_id"`
	CaseID           string `json:"case_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	IdempotencyKey   string `json:"idempotency_key"`
}
type createTestRunArgs struct {
	ProjectID      string   `json:"project_id"`
	IdempotencyKey string   `json:"idempotency_key"`
	Name           string   `json:"name"`
	CaseVersionIDs []string `json:"case_version_ids"`
	WorkItemID     string   `json:"work_item_id,omitempty"`
	SourceSystem   string   `json:"source_system,omitempty"`
	Environment    string   `json:"environment,omitempty"`
	BuildRef       string   `json:"build_ref,omitempty"`
}
type testResultArgs struct {
	ProjectID          string `json:"project_id"`
	ItemID             string `json:"item_id"`
	IdempotencyKey     string `json:"idempotency_key"`
	Status             string `json:"status"`
	Note               string `json:"note,omitempty"`
	ExternalAttemptKey string `json:"external_attempt_key,omitempty"`
}
type failureDefectArgs struct {
	ProjectID      string `json:"project_id"`
	AttemptID      string `json:"attempt_id"`
	IdempotencyKey string `json:"idempotency_key"`
	BugID          string `json:"bug_id,omitempty"`
	Title          string `json:"title,omitempty"`
	Severity       string `json:"severity,omitempty"`
}

func ensureUUID(value, label string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", errors.New("VALIDATION_ERROR: " + label + " must be a UUID")
	}
	return id.String(), nil
}
func (b Config) testRemote(ctx context.Context, tool string, in any) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("TESTING_REST_ADAPTER_REQUIRED")
	}
	var project string
	switch v := in.(type) {
	case testProjectArgs:
		project = v.ProjectID
	case testCaseArgs:
		project = v.ProjectID
	case testRunArgs:
		project = v.ProjectID
	case testItemArgs:
		project = v.ProjectID
	case createTestCaseArgs:
		project = v.ProjectID
	case publishTestCaseArgs:
		project = v.ProjectID
	case createTestRunArgs:
		project = v.ProjectID
	case testResultArgs:
		project = v.ProjectID
	case failureDefectArgs:
		project = v.ProjectID
	default:
		return nil, errors.New("VALIDATION_ERROR")
	}
	pid, err := ensureUUID(project, "project_id")
	if err != nil {
		return nil, err
	}
	base := "/api/v1/projects/" + pid
	switch tool {
	case "test_case_search":
		return b.call(ctx, "GET", base+"/test-cases", nil, "", 0)
	case "test_case_get", "test_case_versions":
		a := in.(testCaseArgs)
		id, err := ensureUUID(a.CaseID, "case_id")
		if err != nil {
			return nil, err
		}
		path := base + "/test-cases/" + id
		if tool == "test_case_versions" {
			path += "/versions"
		}
		return b.call(ctx, "GET", path, nil, "", 0)
	case "test_case_create":
		a := in.(createTestCaseArgs)
		body := map[string]any{"specification": a.Specification}
		if a.SuiteID != "" {
			sid, err := ensureUUID(a.SuiteID, "suite_id")
			if err != nil {
				return nil, err
			}
			body["suite_id"] = sid
		}
		return b.call(ctx, "POST", base+"/test-cases", body, a.IdempotencyKey, 0)
	case "test_case_publish":
		a := in.(publishTestCaseArgs)
		id, err := ensureUUID(a.CaseID, "case_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "POST", base+"/test-cases/"+id+"/versions", map[string]any{"expected_revision": a.ExpectedRevision}, a.IdempotencyKey, 0)
	case "test_run_get":
		a := in.(testRunArgs)
		id, err := ensureUUID(a.RunID, "run_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "GET", base+"/test-runs/"+id, nil, "", 0)
	case "test_run_create":
		a := in.(createTestRunArgs)
		versions := make([]string, 0, len(a.CaseVersionIDs))
		for _, raw := range a.CaseVersionIDs {
			id, err := ensureUUID(raw, "case_version_ids")
			if err != nil {
				return nil, err
			}
			versions = append(versions, id)
		}
		source := a.SourceSystem
		if source == "" {
			source = "external-mcp"
		}
		body := map[string]any{
			"name": a.Name, "source_system": source, "case_version_ids": versions,
			"environment": a.Environment, "build_ref": a.BuildRef,
		}
		if a.WorkItemID != "" {
			wid, err := ensureUUID(a.WorkItemID, "work_item_id")
			if err != nil {
				return nil, err
			}
			body["work_item_id"] = wid
		}
		return b.call(ctx, "POST", base+"/test-runs", body, a.IdempotencyKey, 0)
	case "test_attempts_search":
		a := in.(testItemArgs)
		id, err := ensureUUID(a.ItemID, "item_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "GET", base+"/test-run-items/"+id+"/attempts", nil, "", 0)
	case "test_result_record":
		a := in.(testResultArgs)
		id, err := ensureUUID(a.ItemID, "item_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "POST", base+"/test-run-items/"+id+"/attempts", map[string]any{
			"status": a.Status, "note": a.Note, "source_kind": "external",
			"external_attempt_key": a.ExternalAttemptKey,
		}, a.IdempotencyKey, 0)
	case "defect_create_from_failure":
		a := in.(failureDefectArgs)
		id, err := ensureUUID(a.AttemptID, "attempt_id")
		if err != nil {
			return nil, err
		}
		body := map[string]any{}
		if a.BugID != "" {
			bug, err := ensureUUID(a.BugID, "bug_id")
			if err != nil {
				return nil, err
			}
			body["bug_id"] = bug
		} else {
			body["title"] = a.Title
			severity := a.Severity
			if severity == "" {
				severity = "major"
			}
			body["severity"] = severity
		}
		return b.call(ctx, "POST", base+"/test-attempts/"+id+"/defects", body, a.IdempotencyKey, 0)
	default:
		return nil, errors.New("UNKNOWN_TEST_TOOL")
	}
}

func addTestingTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "test_case_search", Description: "Read TestCase drafts and published versions in authorized project."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testProjectArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_case_search", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_case_get", Description: "Read a specific TestCase draft and current revision."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testCaseArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_case_get", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_case_versions", Description: "Read immutable versions, specification and SHA-256."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testCaseArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_case_versions", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_case_create", Description: "Record a reusable TestCase draft. Does not execute test steps."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createTestCaseArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_case_create", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_case_publish", Description: "Publish immutable TestCaseVersion for expected draft revision."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in publishTestCaseArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_case_publish", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_run_create", Description: "Register an external run referencing exact CaseVersion UUIDs. Never launches tests or CI."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createTestRunArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_run_create", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_run_get", Description: "Read registered run, frozen CaseVersion hashes and latest reported statuses."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testRunArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_run_get", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_attempts_search", Description: "Read append-only results for RunItem; failures persist after passing retries."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_attempts_search", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "test_result_record", Description: "Append a reported result with source identity. Never executes tests."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in testResultArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "test_result_record", in)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "defect_create_from_failure", Description: "Create or associate an existing Bug with a failed Attempt only."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in failureDefectArgs) (*mcp.CallToolResult, map[string]any, error) {
			result, err := b.testRemote(ctx, "defect_create_from_failure", in)
			return nil, result, err
		})
}
