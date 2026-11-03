package mcpserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AccessProjectArgs struct {
	ProjectID string `json:"project_id"`
}
type AccessChangeArgs struct {
	ProjectID       string `json:"project_id"`
	PrincipalID     string `json:"principal_id"`
	Permission      string `json:"permission"`
	Effect          string `json:"effect"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotency_key"`
}

func (b Bridge) accessRemote(ctx context.Context, action string, project, principal string, change *AccessChangeArgs) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("ACCESS_REST_ADAPTER_REQUIRED")
	}
	pid, err := ensureUUID(project, "project_id")
	if err != nil {
		return nil, err
	}
	path := "/api/v1/projects/" + pid + "/access"
	switch action {
	case "access_self":
		return b.call(ctx, "GET", path+"/me", nil, "", 0)
	case "access_list":
		return b.call(ctx, "GET", path, nil, "", 0)
	case "access_change":
		if change == nil {
			return nil, errors.New("VALIDATION_ERROR")
		}
		target, err := ensureUUID(principal, "principal_id")
		if err != nil {
			return nil, err
		}
		if change.Permission == "" || change.IdempotencyKey == "" || change.Reason == "" {
			return nil, errors.New("VALIDATION_ERROR")
		}
		return b.call(ctx, "PUT", path+"/"+target+"/permissions/"+url.PathEscape(change.Permission),
			map[string]any{"effect": change.Effect, "expected_version": change.ExpectedVersion, "reason": change.Reason},
			change.IdempotencyKey, 0)
	default:
		return nil, errors.New("UNKNOWN_ACCESS_TOOL")
	}
}
func addAccessTools(server *mcp.Server, b Bridge) {
	mcp.AddTool(server, &mcp.Tool{Name: "project_access_self", Description: "Read this Agent/Human credential's effective Project Scope and role; no token values."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AccessProjectArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.accessRemote(ctx, "access_self", input.ProjectID, "", nil)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "project_access_list", Description: "List member permission overrides in this project; requires access:manage. No credential secrets."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AccessProjectArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.accessRemote(ctx, "access_list", input.ProjectID, "", nil)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "project_access_change", Description: "Audited optimistic grant/deny/inherit for another Project member; requires access:manage, cannot elevate yourself or grant management unless Human Workspace Admin. Does not run Agents."},
		func(ctx context.Context, _ *mcp.CallToolRequest, input AccessChangeArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.accessRemote(ctx, "access_change", input.ProjectID, input.PrincipalID, &input)
			return nil, data, err
		})
}
