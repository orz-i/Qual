// Package mcpserver exposes Qual work application use cases as local stdio
// MCP tools. It intentionally never launches Agents or executes tests.
package mcpserver

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	identity "qual/internal/identity/application"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// Config is the single supported MCP transport: stdio tools proxy through
// the authenticated REST API. No in-process database/service bypass exists.
type Config struct {
	APIURL string
	Bearer string
}

func parseIDs(project, item string) (uuid.UUID, uuid.UUID, error) {
	pid, err := uuid.Parse(project)
	if err != nil || pid == uuid.Nil {
		return uuid.Nil, uuid.Nil, errors.New("VALIDATION_ERROR: project_id must be a UUID")
	}
	if item == "" {
		return pid, uuid.Nil, nil
	}
	wid, err := uuid.Parse(item)
	if err != nil || wid == uuid.Nil {
		return uuid.Nil, uuid.Nil, errors.New("VALIDATION_ERROR: item_id must be a UUID")
	}
	return pid, wid, nil
}
func userError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		return errors.New("UNAUTHENTICATED")
	case errors.Is(err, identity.ErrForbidden):
		return errors.New("PERMISSION_DENIED")
	case errors.Is(err, identity.ErrNotFound), errors.Is(err, application.ErrNotFound):
		return errors.New("NOT_FOUND")
	case errors.Is(err, application.ErrPrecondition):
		return errors.New("PRECONDITION_REQUIRED: idempotency_key and expected_version are required")
	case errors.Is(err, application.ErrIdempotency):
		return errors.New("IDEMPOTENCY_CONFLICT")
	case errors.Is(err, domain.ErrDoneGate):
		return errors.New("ACCEPTANCE_REQUIRED")
	case errors.Is(err, domain.ErrConflict):
		return errors.New("VERSION_CONFLICT: reload work item before retry")
	case errors.Is(err, domain.ErrClaimed):
		return errors.New("ALREADY_CLAIMED")
	case errors.Is(err, domain.ErrReview):
		return errors.New("PERMISSION_DENIED: Human review required")
	case errors.Is(err, domain.ErrTransition):
		return errors.New("INVALID_TRANSITION")
	case errors.Is(err, domain.ErrDependency):
		return errors.New("INVALID_DEPENDENCY")
	case errors.Is(err, domain.ErrValidation):
		return errors.New("VALIDATION_ERROR")
	default:
		return errors.New("INTERNAL_ERROR")
	}
}

type SearchArgs struct {
	ProjectID string `json:"project_id" jsonschema:"project UUID"`
	Status    string `json:"status,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}
type ItemArgs struct {
	ProjectID string `json:"project_id"`
	ItemID    string `json:"item_id"`
}

// A missing section now uses authorized Context v2 overview. All requests
// share one versioned server-side projection and bounded cursor semantics.
type ContextArgs struct {
	ItemArgs
	Section string `json:"section,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}
type WriteArgs struct {
	ProjectID       string `json:"project_id"`
	ItemID          string `json:"item_id"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}
type CreateArgs struct {
	ProjectID          string             `json:"project_id"`
	IdempotencyKey     string             `json:"idempotency_key"`
	Kind               string             `json:"kind"`
	Title              string             `json:"title"`
	Description        string             `json:"description,omitempty"`
	Priority           string             `json:"priority,omitempty"`
	AcceptanceCriteria []string           `json:"acceptance_criteria,omitempty"`
	BugDetails         *domain.BugDetails `json:"bug_details,omitempty"`
}
type ReassignArgs struct {
	WriteArgs
	NewAssigneeID string `json:"new_assignee_id"`
}
type UpdateArgs struct {
	WriteArgs
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Priority    *string `json:"priority,omitempty"`
}
type TransitionArgs struct {
	WriteArgs
	Status string `json:"status"`
}
type CommentArgs struct {
	WriteArgs
	Body string `json:"body"`
}
type ActivityArgs struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit,omitempty"`
}

func New(b Config) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "qual", Version: "0.3.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_search", Description: "Read a project-scoped page of Task/Bug work items. No execution or scheduling."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_search", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_get", Description: "Get one authorized work item by UUID."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_get", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_capabilities_get", Description: "Fresh server-derived task-scoped/legacy my_capacity and allowed_actions for the authenticated Principal only. Read-only UX hint; writes reauthorize."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_capabilities_get", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_get_context", Description: "Persisted bounded authorized WorkItem Context v2; omitted section defaults to overview. Cursor pages are versioned; never AI-generated."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ContextArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_get_context", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_create", Description: "Create a persisted Task or Bug in the authorized project. Does not start an Agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in CreateArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_create", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_claim", Description: "Atomically claim an unassigned Task/Bug, version protected; never launches an Agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_claim", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_release", Description: "Release this task's assignee after scoped authorization; does not stop an Agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_release", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_reassign", Description: "Transfer a task to an eligible real Principal with CAS; manager recovery is independently authorized."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ReassignArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_reassign", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_review_claim", Description: "Claim the independent reviewer relationship on an in-review WorkItem, subject to persisted executor history and CAS."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_review_claim", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_review_release", Description: "Relinquish current independent reviewer assignment without modifying historical role facts."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_review_release", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_update", Description: "Update title/description/priority with optimistic version check."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_update", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_transition", Description: "Apply a legal status transition. Human review is required to complete a task."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in TransitionArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_transition", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_add_comment", Description: "Append a persisted work comment and audit; no Agent execution."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in CommentArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "work_item_add_comment", in)
			return nil, data, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "activity_search", Description: "Read recent project activity records."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ActivityArgs) (*mcp.CallToolResult, map[string]any, error) {
			data, err := b.remote(ctx, "activity_search", in)
			return nil, data, err
		})
	addGovernanceTools(server, b)
	addTaskModeGovernanceTools(server, b)
	addWorkPolicyTools(server, b)
	addTestingTools(server, b)
	addEvidenceTools(server, b)
	addAccessTools(server, b)
	addRoleTools(server, b)
	return server
}
