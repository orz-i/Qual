//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func TestMCPStdioRemoteAdapterUsesOnlyScopedHTTP(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	server := mcpserver.New(mcpserver.Config{APIURL: f.server.URL, Bearer: f.agent})
	a, b := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "remote-adapter-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "work_item_create",
		Arguments: map[string]any{"project_id": f.project.String(), "kind": "task", "title": "MCP via REST",
			"idempotency_key": "remote-mcp-test"},
	})
	if err != nil || result.IsError {
		t.Fatalf("remote MCP create %v %+v", err, result)
	}
	bytes, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var item struct {
		WorkItem struct {
			ID string `json:"id"`
		} `json:"work_item"`
	}
	if err := json.Unmarshal(bytes, &item); err != nil || item.WorkItem.ID == "" {
		t.Fatalf("bad item output: %s %v", bytes, err)
	}
	status, got, _ := request(t, "GET", f.url(f.project)+"/"+item.WorkItem.ID, f.agent, "", "", nil)
	if status != 200 || got["title"] != "MCP via REST" {
		t.Fatalf("MCP/REST mismatch %d %v", status, got)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "work_item_get", Arguments: map[string]any{"project_id": f.otherProject.String(), "item_id": item.WorkItem.ID},
	})
	if err != nil || !result.IsError {
		t.Fatalf("cross-project should deny: err=%v response=%v", err, result)
	}
	failure, _ := json.Marshal(result.Content)
	if !strings.Contains(string(failure), "NOT_FOUND") {
		t.Fatalf("expected privacy-preserving not found: %s", failure)
	}
}

func TestMCPActualStdioProcess(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "../cmd/qual-mcp")
	cmd.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+f.agent)
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-process-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("stdio command transport connect: %v", err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("stdio list tools: %v", err)
	}
	if len(tools.Tools) != 42 {
		t.Fatalf("stdio tools got %d, want 42", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "work_item_search",
		Arguments: map[string]any{"project_id": f.project.String(), "limit": 10}})
	if err != nil || result.IsError {
		t.Fatalf("stdio search: %v %+v", err, result)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(raw), "\"items\"") {
		t.Fatalf("unexpected stdio output %s", raw)
	}
	work := create(t, f, f.agent, "MCP governance read-only history")
	accepted, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "work_acceptance_get",
		Arguments: map[string]any{"project_id": f.project.String(), "item_id": work["id"]}})
	if err != nil || accepted.IsError {
		t.Fatalf("scoped acceptance history MCP read failed: %v %+v", err, accepted)
	}
	history, _ := json.Marshal(accepted.StructuredContent)
	if !strings.Contains(string(history), "not_completed") {
		t.Fatalf("unexpected acceptance history: %s", history)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "work_item_complete", Arguments: map[string]any{
		"project_id": f.project.String(), "item_id": work["id"], "acceptance_mode": "independent_review",
		"expected_version": 1, "reference_kind": "test_run", "reference_value": uuid.NewString(),
		"idempotency_key": "mcp-low-actor-must-not-complete", "acknowledge_self_review": false}})
	if err == nil && !denied.IsError {
		t.Fatalf("ordinary PI Agent gained forbidden complete action: %+v", denied)
	}
}

func TestMCPRefusesUnencryptedRemoteURL(t *testing.T) {
	if _, err := mcpserver.ValidateAPIURL("http://example.com:8080"); err == nil {
		t.Fatal("remote plaintext bearer URL accepted")
	}
	if url, err := mcpserver.ValidateAPIURL("http://127.0.0.1:18080"); err != nil || url == "" {
		t.Fatalf("loopback should be allowed: %v", err)
	}
}
