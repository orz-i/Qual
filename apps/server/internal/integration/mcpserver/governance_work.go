package mcpserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
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

func addWorkPolicyTools(server *mcp.Server, b Bridge) {
	mcp.AddTool(server, &mcp.Tool{Name: "work_review_policy_get", Description: "Read WorkItem effective review policy, project safety upper bound and current CAS/override versions. No Reviewer approval is created."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			if b.APIURL != "" {
				data, e := b.call(ctx, "GET", "/api/v1/projects/"+p.String()+"/work-items/"+id.String()+"/review-policy", nil, "", 0)
				return nil, data, e
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			view, err := b.Work.GetWorkReviewPolicy(ctx, actor, p, id)
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_review_policy": view}, nil
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
			if b.APIURL != "" {
				v, e := b.call(ctx, "PUT", "/api/v1/projects/"+p.String()+"/work-items/"+id.String()+"/review-policy", input, in.IdempotencyKey, 0)
				return nil, v, e
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			w, err := b.Work.ChangeWorkReviewPolicy(ctx, actor, p, id, input, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": w}, nil
		})
}
