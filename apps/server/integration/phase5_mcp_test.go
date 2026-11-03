//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func TestPhase5MCPArtifactAndReferenceTools(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("S3 required")
	}
	work := create(t, f, f.agent, "MCP artifact record")
	id, _ := testUploadArtifact(t, f, work["id"].(string), []byte("MCP artifact bytes reported by external Agent"))
	ctx := context.Background()
	server := mcpserver.New(mcpserver.Bridge{APIURL: f.server.URL, Bearer: f.agent})
	a, b := mcp.NewInMemoryTransports()
	s, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase5-mcp-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil || result.IsError {
			raw, _ := json.Marshal(result.Content)
			t.Fatalf("tool %s: %v %s", tool, err, raw)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	project := f.project.String()
	target := work["id"].(string)
	listing := call("artifact_search", map[string]any{"project_id": project, "target_kind": "work_item", "target_id": target})
	if len(listing["items"].([]any)) != 1 {
		t.Fatalf("artifact MCP list %v", listing)
	}
	ainfo := call("artifact_get", map[string]any{"project_id": project, "artifact_id": id})
	if ainfo["state"] != "ready" {
		t.Fatalf("not ready %v", ainfo)
	}
	ref := call("external_reference_create", map[string]any{"project_id": project, "target_kind": "work_item",
		"target_id": target, "provider": "git", "kind": "commit", "external_id": "mcp-abc",
		"url": "https://example.com/commit/mcp-abc", "idempotency_key": "phase5-mcp-ref-create"})
	if ref["provider"] != "git" {
		t.Fatalf("MCP reference %v", ref)
	}
	links := call("external_reference_search", map[string]any{"project_id": project, "target_kind": "work_item", "target_id": target})
	if len(links["items"].([]any)) != 1 {
		t.Fatalf("MCP ref missing %v", links)
	}
}
