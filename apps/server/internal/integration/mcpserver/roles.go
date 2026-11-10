package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type RoleSearchArgs struct {
	ProjectID string `json:"project_id"`
	ItemID    string `json:"item_id"`
}
type RoleRecordArgs struct {
	ProjectID       string `json:"project_id"`
	ItemID          string `json:"item_id"`
	Capacity        string `json:"capacity"`
	Outcome         string `json:"outcome"`
	Summary         string `json:"summary"`
	ReferenceKind   string `json:"reference_kind"`
	ReferenceValue  string `json:"reference_value"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (b Config) roleRemote(ctx context.Context, action, projectID, itemID string, input *RoleRecordArgs) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("ROLE_REST_ADAPTER_REQUIRED")
	}
	pid, err := ensureUUID(projectID, "project_id")
	if err != nil {
		return nil, err
	}
	wid, err := ensureUUID(itemID, "item_id")
	if err != nil {
		return nil, err
	}
	path := "/api/v1/projects/" + pid + "/work-items/" + wid + "/role-entries"
	switch action {
	case "search":
		return b.call(ctx, "GET", path, nil, "", 0)
	case "record":
		if input == nil || input.IdempotencyKey == "" || input.ExpectedVersion < 1 || input.Summary == "" {
			return nil, errors.New("VALIDATION_ERROR")
		}
		return b.call(ctx, "POST", path, map[string]any{
			"capacity": input.Capacity, "outcome": input.Outcome, "summary": input.Summary,
			"reference_kind": input.ReferenceKind, "reference_value": input.ReferenceValue,
			"expected_version": input.ExpectedVersion,
		}, input.IdempotencyKey, 0)
	default:
		return nil, errors.New("UNKNOWN_ROLE_TOOL")
	}
}
func addRoleTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "work_role_entries_search",
		Description: "Read immutable per-WorkItem capacity records by real Principal (executor/tester/reviewer/auditor); mark self_review when appropriate. Does not execute Agents or tests."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RoleSearchArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.roleRemote(ctx, "search", in.ProjectID, in.ItemID, nil)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_role_entry_record",
		Description: "Record an actual executor/tester/reviewer/auditor action with evidence, expected WorkItem version and Idempotency-Key. Role labels cannot impersonate or bypass credential scopes; review is not an automatic done."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RoleRecordArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.roleRemote(ctx, "record", in.ProjectID, in.ItemID, &in)
			return nil, data, err
		})
}
