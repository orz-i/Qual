package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

type GovernanceWorkPolicyChangeArgs struct {
	ProjectID             string `json:"project_id"`
	ItemID                string `json:"item_id"`
	Mode                  string `json:"mode"`
	ExpectedVersion       int64  `json:"expected_version"`
	ExpectedPolicyVersion int64  `json:"expected_policy_version"`
	Reason                string `json:"reason"`
	IdempotencyKey        string `json:"idempotency_key"`
}

func addWorkPolicyTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "work_review_policy_get", Description: "Read WorkItem effective review policy, project safety upper bound and current CAS/override versions. No Reviewer approval is created."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			data, e := b.call(ctx, "GET", "/api/v1/projects/"+p.String()+"/work-items/"+id.String()+"/review-policy", nil, "", 0)
			return nil, data, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_review_policy_change", Description: "CAS and audited WorkItem-level review override. Requires access:manage; Owner loosening requires actual Human Workspace Admin. Invalidates stale Reviewer approvals."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceWorkPolicyChangeArgs) (*mcp.CallToolResult, map[string]any, error) {
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			if in.ExpectedVersion < 1 || in.ExpectedPolicyVersion < 0 || in.IdempotencyKey == "" {
				return nil, nil, errors.New("PRECONDITION_REQUIRED")
			}
			input := application.WorkReviewPolicyChange{Mode: domain.ReviewPolicyMode(in.Mode), ExpectedVersion: in.ExpectedVersion,
				ExpectedPolicyVersion: in.ExpectedPolicyVersion, Reason: in.Reason}
			v, e := b.call(ctx, "PUT", "/api/v1/projects/"+p.String()+"/work-items/"+id.String()+"/review-policy", input, in.IdempotencyKey, 0)
			return nil, v, e
		})
}
