package mcpserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qual/internal/work/application"
	"qual/internal/work/domain"
)

type TaskModePreviewArgs struct {
	ProjectID string `json:"project_id"`
	Mode      string `json:"mode"`
}
type TaskModeChangeArgs struct {
	ProjectID                 string `json:"project_id"`
	Mode                      string `json:"mode"`
	ExpectedVersion           int64  `json:"expected_version"`
	Reason                    string `json:"reason"`
	AcknowledgeLegacyWidening bool   `json:"acknowledge_legacy_widening"`
	IdempotencyKey            string `json:"idempotency_key"`
}

func addTaskModeGovernanceTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "project_task_authorization_get",
		Description: "Read the current project task authorization mode, CAS version and real Goose20 schema readiness; requires access:manage."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceProjectArgs) (*mcp.CallToolResult, map[string]any, error) {
			pid, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			v, e := b.call(ctx, "GET", "/api/v1/projects/"+pid.String()+"/task-authorization-policy", nil, "", 0)
			return nil, v, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "project_task_authorization_preview",
		Description: "Read-only preview of a legacy/task_scoped policy change; requires access:manage; shows affected task counts and whether rollback widens old scopes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in TaskModePreviewArgs) (*mcp.CallToolResult, map[string]any, error) {
			pid, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			mode := domain.TaskAuthorizationMode(in.Mode)
			if mode != domain.LegacyAuthorization && mode != domain.TaskScopedAuthorization {
				return nil, nil, errors.New("VALIDATION_ERROR")
			}
			q := url.Values{"mode": {in.Mode}}
			v, e := b.call(ctx, "GET", "/api/v1/projects/"+pid.String()+"/task-authorization-policy/preview?"+q.Encode(), nil, "", 0)
			return nil, v, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "project_task_authorization_change",
		Description: "Explicit versioned policy change, access:manage + same-transaction credential check; rollback to legacy requires acknowledgement of wider historical project scopes. No implicit switch or credential issuance."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in TaskModeChangeArgs) (*mcp.CallToolResult, map[string]any, error) {
			pid, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			change := application.TaskAuthorizationPolicyChange{
				Mode: domain.TaskAuthorizationMode(in.Mode), ExpectedVersion: in.ExpectedVersion,
				Reason: in.Reason, AcknowledgeLegacyWidening: in.AcknowledgeLegacyWidening,
			}
			if err = change.Validate(); err != nil {
				return nil, nil, userError(err)
			}
			if len(in.IdempotencyKey) < 8 {
				return nil, nil, errors.New("PRECONDITION_REQUIRED")
			}
			view, e := b.call(ctx, "PUT", "/api/v1/projects/"+pid.String()+"/task-authorization-policy",
				change, in.IdempotencyKey, 0)
			return nil, view, e
		})
}
