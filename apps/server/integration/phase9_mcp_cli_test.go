//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func TestPhase9MCPAndCLIRoleProvenance(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "phase9-mcp-cli-roles")
	id := work["id"].(string)
	pid := f.project.String()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	server := mcpserver.New(mcpserver.Bridge{APIURL: f.server.URL, Bearer: f.agent})
	st, ct := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase9-multi-role", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	args := map[string]any{
		"project_id": pid, "item_id": id, "capacity": "executor", "outcome": "observed",
		"summary":        "Same real PI Principal records this implementation, not a fictional second Agent",
		"reference_kind": "git_commit", "reference_value": "4d6b9ee",
		"expected_version": 1, "idempotency_key": "phase9-role-mcp-real-record",
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "work_role_entry_record", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("MCP record %v %v", err, result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["capacity"] != "executor" || entry["work_version"] != float64(2) {
		t.Fatalf("MCP record returned %v", entry)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "work_role_entries_search",
		Arguments: map[string]any{"project_id": pid, "item_id": id}})
	if err != nil || result.IsError {
		t.Fatalf("MCP search %v %v", err, result)
	}
	raw, _ = json.Marshal(result.StructuredContent)
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response["items"].([]any)) != 1 {
		t.Fatalf("MCP role list %v", response)
	}
	// Reproduce the same work history via a freshly built user-visible CLI.
	bin := filepath.Join(t.TempDir(), "qual")
	builder := exec.CommandContext(ctx, "go", "build", "-o", bin, "../cmd/qual")
	if raw, err := builder.CombinedOutput(); err != nil {
		t.Fatalf("CLI build: %v %s", err, raw)
	}
	command := exec.CommandContext(ctx, bin, "role", "list", "-project", pid, "-item", id)
	command.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+f.agent)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI role list %v %s", err, output)
	}
	var page map[string]any
	if err := json.Unmarshal(output, &page); err != nil {
		t.Fatal(err)
	}
	if len(page["items"].([]any)) != 1 {
		t.Fatalf("CLI role list mismatch: %s", output)
	}
}
