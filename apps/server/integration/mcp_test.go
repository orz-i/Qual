//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func TestMCPWorkToolsUseSameAuthorizedService(t *testing.T) {
	f := setup(t)
	server := mcpserver.New(mcpserver.Config{APIURL: f.server.URL, Bearer: f.agent})
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "qual-test", Version: "0.1.0"}, nil)
	connection, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	tools, err := connection.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"work_item_search", "work_item_get", "work_item_get_context",
		"work_item_create", "work_item_claim", "work_item_update", "work_item_transition",
		"work_item_add_comment", "activity_search"} {
		if !names[name] {
			t.Fatalf("MCP tool missing %s", name)
		}
	}
	result, err := connection.CallTool(ctx, &mcp.CallToolParams{
		Name: "work_item_create",
		Arguments: map[string]any{
			"project_id": f.project.String(), "kind": "task", "title": "MCP created item",
			"idempotency_key": "mcp-create-test",
		},
	})
	if err != nil || result.IsError {
		t.Fatalf("MCP create: err=%v, result=%+v", err, result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var obj struct {
		WorkItem struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"work_item"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj.WorkItem.ID == "" || obj.WorkItem.Version != 1 {
		t.Fatalf("invalid create result %s", raw)
	}
	// Verify MCP writes are available over ordinary REST and governed by the same database state.
	status, apiItem, _ := request(t, "GET", f.url(f.project)+"/"+obj.WorkItem.ID, f.agent, "", "", nil)
	if status != 200 || apiItem["title"] != "MCP created item" {
		t.Fatalf("REST/MCP mismatch: %d %v", status, apiItem)
	}
	result, err = connection.CallTool(ctx, &mcp.CallToolParams{
		Name: "work_item_claim",
		Arguments: map[string]any{"project_id": f.project.String(), "item_id": obj.WorkItem.ID,
			"expected_version": 1, "idempotency_key": "mcp-claim-test"},
	})
	if err != nil || result.IsError {
		t.Fatalf("MCP claim: err=%v result=%v", err, result)
	}
	result, err = connection.CallTool(ctx, &mcp.CallToolParams{
		Name: "work_item_claim",
		Arguments: map[string]any{"project_id": f.project.String(), "item_id": obj.WorkItem.ID,
			"expected_version": 1, "idempotency_key": "mcp-second-claim"},
	})
	if err != nil || !result.IsError {
		t.Fatalf("second claim should fail: err=%v result=%v", err, result)
	}
	message, _ := json.Marshal(result.Content)
	if !strings.Contains(string(message), "VERSION_CONFLICT") {
		t.Fatalf("expected version conflict in tool error: %s", message)
	}
	result, err = connection.CallTool(ctx, &mcp.CallToolParams{
		Name:      "work_item_get_context",
		Arguments: map[string]any{"project_id": f.project.String(), "item_id": obj.WorkItem.ID},
	})
	if err != nil || result.IsError {
		t.Fatalf("MCP context err=%v result=%v", err, result)
	}
	raw, _ = json.Marshal(result.StructuredContent)
	var contextV2 map[string]any
	if err := json.Unmarshal(raw, &contextV2); err != nil {
		t.Fatal(err)
	}
	if contextV2["schema_version"] != float64(2) || contextV2["section"] != "overview" ||
		contextV2["context_revision"] != float64(2) {
		t.Fatalf("omitted MCP section must use authoritative v2 overview: %s", raw)
	}
	if _, oldSynthesis := contextV2["recent_comments"]; oldSynthesis {
		t.Fatal("obsolete context v1 multi-REST synthesis remained reachable")
	}
}
