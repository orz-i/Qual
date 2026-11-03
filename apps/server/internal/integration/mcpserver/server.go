// Package mcpserver exposes Qual work application use cases as local stdio
// MCP tools. It intentionally never launches Agents or executes tests.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	identity "qual/internal/identity/application"
	"qual/internal/work/application"
	"qual/internal/work/domain"
	contextapp "qual/internal/workcontext/application"
)

type Bridge struct {
	Auth    identity.Authenticator // optional in-process test/service adapter
	Work    application.Service    // optional in-process test/service adapter
	Context *contextapp.Service    // optional authorized v2 recovery projection
	APIURL  string                 // external stdio adapter uses only REST+scoped PAT
	Bearer  string
}

func (b Bridge) actor(ctx context.Context) (identity.Actor, error) {
	actor, err := b.Auth.Authenticate(ctx, b.Bearer)
	if err != nil {
		return actor, fmt.Errorf("UNAUTHENTICATED")
	}
	return actor, nil
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

// Empty section preserves the existing MCP v1 response for old callers.
// Opt-in section reads one bounded, separately authorized v2 projection.
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

func New(b Bridge) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "qual", Version: "0.3.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_search", Description: "Read a project-scoped page of Task/Bug work items. No execution or scheduling."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_search", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			project, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			page, err := b.Work.List(ctx, actor, project, application.Filter{
				Status: domain.Status(in.Status), Kind: domain.Kind(in.Kind), Limit: in.Limit, Cursor: in.Cursor,
			})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"items": page.Items, "next_cursor": page.NextCursor}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_get", Description: "Get one authorized work item by UUID."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ItemArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_get", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			item, err := b.Work.Get(ctx, actor, p, id)
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_get_context", Description: "Persisted work context: legacy v1 with no section; explicit bounded v2 section with cursor. Never AI generated."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ContextArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_get_context", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			if in.Section != "" || in.Limit != 0 || in.Cursor != "" {
				if b.Context == nil {
					return nil, nil, errors.New("CONTEXT_UNAVAILABLE")
				}
				if _, err := contextSectionQuery(in.Section, in.Limit, in.Cursor); err != nil {
					return nil, nil, err
				}
				section := in.Section
				if section == "" {
					section = "overview"
				}
				page, err := b.Context.Get(ctx, actor, p, id, contextapp.Input{Section: contextapp.Section(section), Limit: in.Limit, Cursor: in.Cursor})
				if err != nil {
					if errors.Is(err, contextapp.ErrInvalidQuery) {
						return nil, nil, errors.New("VALIDATION_ERROR")
					}
					if errors.Is(err, contextapp.ErrStaleCursor) {
						return nil, nil, errors.New("CONTEXT_CHANGED")
					}
					if errors.Is(err, contextapp.ErrScanBudget) {
						return nil, nil, errors.New("CONTEXT_QUERY_BUDGET")
					}
					return nil, nil, userError(err)
				}
				raw, err := json.Marshal(page)
				if err != nil {
					return nil, nil, errors.New("INVALID_SERVER_RESPONSE")
				}
				var data map[string]any
				if err := json.Unmarshal(raw, &data); err != nil {
					return nil, nil, errors.New("INVALID_SERVER_RESPONSE")
				}
				return nil, data, nil
			}
			item, err := b.Work.Get(ctx, actor, p, id)
			if err != nil {
				return nil, nil, userError(err)
			}
			comments, err := b.Work.Comments(ctx, actor, p, id)
			if err != nil {
				return nil, nil, userError(err)
			}
			deps, err := b.Work.Dependencies(ctx, actor, p, id)
			if err != nil {
				return nil, nil, userError(err)
			}
			if len(comments) > 10 {
				comments = comments[:10]
			}
			return nil, map[string]any{"schema_version": 1, "work_item": item, "recent_comments": comments, "dependencies": deps, "context_revision": item.Version}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_create", Description: "Create a persisted Task or Bug in the authorized project. Does not start an Agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in CreateArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_create", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			item, err := b.Work.Create(ctx, actor, p, domain.Draft{
				Kind: domain.Kind(in.Kind), Title: in.Title, Description: in.Description,
				Priority: domain.Priority(in.Priority), AcceptanceCriteria: in.AcceptanceCriteria,
				BugDetails: in.BugDetails,
			}, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_claim", Description: "Atomically claim an unassigned Task/Bug, version protected; never launches an Agent."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in WriteArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_claim", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			item, err := b.Work.Claim(ctx, actor, p, id, in.ExpectedVersion, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_update", Description: "Update title/description/priority with optimistic version check."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_update", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			ch := domain.Changes{Title: in.Title, Description: in.Description}
			if in.Priority != nil {
				priority := domain.Priority(*in.Priority)
				ch.Priority = &priority
			}
			item, err := b.Work.Update(ctx, actor, p, id, ch, in.ExpectedVersion, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_transition", Description: "Apply a legal status transition. Human review is required to complete a task."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in TransitionArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_transition", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			item, err := b.Work.Transition(ctx, actor, p, id, domain.Status(in.Status), in.ExpectedVersion, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "work_item_add_comment", Description: "Append a persisted work comment and audit; no Agent execution."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in CommentArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "work_item_add_comment", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, id, err := parseIDs(in.ProjectID, in.ItemID)
			if err != nil {
				return nil, nil, err
			}
			item, err := b.Work.AddComment(ctx, actor, p, id, in.Body, in.ExpectedVersion, application.Meta{RequestID: uuid.NewString(), IdempotencyKey: in.IdempotencyKey})
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"work_item": item}, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "activity_search", Description: "Read recent project activity records."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ActivityArgs) (*mcp.CallToolResult, map[string]any, error) {
			if b.APIURL != "" {
				data, err := b.remote(ctx, "activity_search", in)
				return nil, data, err
			}
			actor, err := b.actor(ctx)
			if err != nil {
				return nil, nil, err
			}
			p, _, err := parseIDs(in.ProjectID, "")
			if err != nil {
				return nil, nil, err
			}
			records, err := b.Work.Activity(ctx, actor, p, in.Limit)
			if err != nil {
				return nil, nil, userError(err)
			}
			return nil, map[string]any{"items": records}, nil
		})
	addGovernanceTools(server, b)
	addWorkPolicyTools(server, b)
	if b.APIURL != "" {
		addTestingTools(server, b)
		addEvidenceTools(server, b)
		addAccessTools(server, b)
		addRoleTools(server, b)
	}
	return server
}
