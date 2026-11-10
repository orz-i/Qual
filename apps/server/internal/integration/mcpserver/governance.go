package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// Explicit review governance tools only archive authenticated decisions.
// They never provide a way to impersonate a Human, create a Reviewer entry,
// bypass access:manage, or mark Done without a real server-side acceptance.
type GovernanceProjectArgs struct {
	ProjectID string `json:"project_id"`
}
type GovernancePolicyChangeArgs struct {
	ProjectID                   string `json:"project_id"`
	DefaultMode                 string `json:"default_mode"`
	OwnerAcceptanceAllowed      bool   `json:"owner_acceptance_allowed"`
	ForceIndependentForHighRisk bool   `json:"force_independent_for_high_risk"`
	ExpectedVersion             int64  `json:"expected_version"`
	Reason                      string `json:"reason"`
	IdempotencyKey              string `json:"idempotency_key"`
}
type GovernanceItemArgs struct {
	ProjectID string `json:"project_id"`
	ItemID    string `json:"item_id"`
}
type GovernanceCompleteArgs struct {
	ProjectID             string `json:"project_id"`
	ItemID                string `json:"item_id"`
	AcceptanceMode        string `json:"acceptance_mode"`
	ExpectedVersion       int64  `json:"expected_version"`
	Reason                string `json:"reason"`
	AcknowledgeSelfReview bool   `json:"acknowledge_self_review"`
	ReferenceKind         string `json:"reference_kind"`
	ReferenceValue        string `json:"reference_value"`
	IdempotencyKey        string `json:"idempotency_key"`
}

func (b Config) governanceRemote(ctx context.Context, action, projectID, itemID string, body any, idem string) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("REST_ADAPTER_REQUIRED")
	}
	pid, id, err := parseIDs(projectID, itemID)
	if err != nil {
		return nil, err
	}
	base := "/api/v1/projects/" + pid.String()
	switch action {
	case "policy_read":
		return b.call(ctx, "GET", base+"/review-policy", nil, "", 0)
	case "policy_change":
		return b.call(ctx, "PUT", base+"/review-policy", body, idem, 0)
	case "acceptance_read":
		return b.call(ctx, "GET", base+"/work-items/"+id.String()+"/acceptances", nil, "", 0)
	case "complete":
		return b.call(ctx, "POST", base+"/work-items/"+id.String()+"/complete", body, idem, 0)
	}
	return nil, errors.New("VALIDATION_ERROR")
}

func addGovernanceTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "project_review_policy_get", Description: "Read real project review-policy defaults, effective restrictions and CAS version; disabled on Goose12."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceProjectArgs) (*mcp.CallToolResult, map[string]any, error) {
			v, e := b.governanceRemote(ctx, "policy_read", in.ProjectID, "", nil, "")
			return nil, v, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "project_review_policy_change", Description: "CAS project policy change. Requires access:manage; loosening Owner acceptance or high-risk rule requires actual Human Workspace Admin. Never grants the caller privileges."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernancePolicyChangeArgs) (*mcp.CallToolResult, map[string]any, error) {
			if in.IdempotencyKey == "" {
				return nil, nil, errors.New("PRECONDITION_REQUIRED")
			}
			data := application.ProjectReviewPolicyChange{DefaultMode: domain.ReviewPolicyMode(in.DefaultMode), OwnerAcceptanceAllowed: in.OwnerAcceptanceAllowed, ForceIndependentForHighRisk: in.ForceIndependentForHighRisk, ExpectedVersion: in.ExpectedVersion, Reason: in.Reason}
			if e := data.Validate(); e != nil {
				return nil, nil, userError(e)
			}
			v, e := b.governanceRemote(ctx, "policy_change", in.ProjectID, "", data, in.IdempotencyKey)
			return nil, v, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_acceptance_get", Description: "Read genuine WorkItem acceptance decision or legacy_unclassified historical Done. No fabricated Reviewer or credential secrets."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			v, e := b.governanceRemote(ctx, "acceptance_read", in.ProjectID, in.ItemID, nil, "")
			return nil, v, e
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_complete", Description: "Complete in_review only through explicit independent_review or Owner self-acceptance. Real work:review, verified same-work evidence, policy and CAS required; never invents reviewer approval."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GovernanceCompleteArgs) (*mcp.CallToolResult, map[string]any, error) {
			if in.IdempotencyKey == "" || in.ExpectedVersion < 1 {
				return nil, nil, errors.New("PRECONDITION_REQUIRED")
			}
			d := domain.DoneDecision{Mode: domain.ReviewPolicyMode(in.AcceptanceMode), ExpectedVersion: in.ExpectedVersion,
				Reason: in.Reason, AcknowledgeSelfReview: in.AcknowledgeSelfReview,
				ReferenceKind: domain.ReferenceKind(in.ReferenceKind), ReferenceValue: in.ReferenceValue}
			v, e := b.governanceRemote(ctx, "complete", in.ProjectID, in.ItemID, d, in.IdempotencyKey)
			return nil, v, e
		})
}
