package mcpserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ArtifactListArgs struct {
	ProjectID  string `json:"project_id"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
}
type ArtifactGetArgs struct {
	ProjectID  string `json:"project_id"`
	ArtifactID string `json:"artifact_id"`
}
type ExternalReferenceArgs struct {
	ProjectID      string `json:"project_id"`
	TargetKind     string `json:"target_kind"`
	TargetID       string `json:"target_id"`
	Provider       string `json:"provider"`
	Kind           string `json:"kind"`
	ExternalID     string `json:"external_id"`
	URL            string `json:"url"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (b Config) evidenceRemote(ctx context.Context, action string, input any) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("EVIDENCE_REST_ADAPTER_REQUIRED")
	}
	var pid string
	switch v := input.(type) {
	case ArtifactListArgs:
		pid = v.ProjectID
	case ArtifactGetArgs:
		pid = v.ProjectID
	case ExternalReferenceArgs:
		pid = v.ProjectID
	default:
		return nil, errors.New("VALIDATION_ERROR")
	}
	project, err := ensureUUID(pid, "project_id")
	if err != nil {
		return nil, err
	}
	base := "/api/v1/projects/" + project
	switch action {
	case "artifact_search", "external_reference_search":
		var kind, target string
		if action == "artifact_search" {
			a := input.(ArtifactListArgs)
			kind = a.TargetKind
			target = a.TargetID
		} else {
			a := input.(ArtifactListArgs)
			kind = a.TargetKind
			target = a.TargetID
		}
		id, err := ensureUUID(target, "target_id")
		if err != nil {
			return nil, err
		}
		q := url.Values{"target_kind": {kind}, "target_id": {id}}
		p := "/artifacts"
		if action == "external_reference_search" {
			p = "/external-references"
		}
		return b.call(ctx, "GET", base+p+"?"+q.Encode(), nil, "", 0)
	case "artifact_get":
		a := input.(ArtifactGetArgs)
		id, err := ensureUUID(a.ArtifactID, "artifact_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "GET", base+"/artifacts/"+id, nil, "", 0)
	case "external_reference_create":
		a := input.(ExternalReferenceArgs)
		id, err := ensureUUID(a.TargetID, "target_id")
		if err != nil {
			return nil, err
		}
		return b.call(ctx, "POST", base+"/external-references", map[string]any{
			"target_kind": a.TargetKind, "target_id": id, "provider": a.Provider,
			"kind": a.Kind, "external_id": a.ExternalID, "url": a.URL,
		}, a.IdempotencyKey, 0)
	default:
		return nil, errors.New("UNKNOWN_EVIDENCE_TOOL")
	}
}
func addEvidenceTools(server *mcp.Server, b Config) {
	mcp.AddTool(server, &mcp.Tool{Name: "artifact_search", Description: "List project-scoped verified Artifact metadata for a WorkItem/TestCase/Run/Attempt; use CLI/API for file bytes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ArtifactListArgs) (*mcp.CallToolResult, map[string]any, error) {
			value, err := b.evidenceRemote(ctx, "artifact_search", in)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "artifact_get", Description: "Read one authorized artifact's immutable SHA-256 and state. Does not return S3 keys."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ArtifactGetArgs) (*mcp.CallToolResult, map[string]any, error) {
			value, err := b.evidenceRemote(ctx, "artifact_get", in)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "external_reference_search", Description: "Read external Git/CI links recorded as facts without fetching their URLs."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ArtifactListArgs) (*mcp.CallToolResult, map[string]any, error) {
			value, err := b.evidenceRemote(ctx, "external_reference_search", in)
			return nil, value, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "external_reference_create", Description: "Record an HTTPS external work or test evidence link, audited and idempotent; no external fetch."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ExternalReferenceArgs) (*mcp.CallToolResult, map[string]any, error) {
			value, err := b.evidenceRemote(ctx, "external_reference_create", in)
			return nil, value, err
		})
}
