//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
	"testing"
)

func TestPhase8MCPAccessToolsRespectScopeAndDelegation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	principal := principalFor(t, f, f.agent)
	managerToken := createAccessToken(t, f, principal, []string{"access:manage", "work:read"})
	bridge := mcpserver.Bridge{APIURL: f.server.URL, Bearer: managerToken}
	server := mcpserver.New(bridge)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	sessionServer, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase8-mcp-access-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args any) (map[string]any, bool) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			if len(result.Content) == 0 {
				return nil, true
			}
			return map[string]any{"error": result.Content[0]}, true
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out, false
	}
	pid := f.project.String()
	before, failed := call("project_access_self", map[string]any{"project_id": pid})
	if failed || before["kind"] != "agent" {
		t.Fatalf("MCP self returned %+v failed=%v", before, failed)
	}
	if _, failed := call("project_access_list", map[string]any{"project_id": pid}); !failed {
		t.Fatal("agent manager listed before grant")
	}
	if status, _ := setPermission(t, f, f.admin, principal, "access:manage", "allow", 0, "phase8-mcp-manager-access"); status != 200 {
		t.Fatal("Human admin grant manager failed")
	}
	members, failed := call("project_access_list", map[string]any{"project_id": pid})
	if failed || len(members["items"].([]any)) < 2 {
		t.Fatalf("manager MCP list failed: %v", members)
	}
	otherID := principalFor(t, f, f.agent2)
	changed, failed := call("project_access_change", map[string]any{
		"project_id": pid, "principal_id": otherID.String(), "permission": "work:review",
		"effect": "allow", "expected_version": 0,
		"reason":          "Human-delegated Agent managing ordinary Project Work review",
		"idempotency_key": "phase8-mcp-agent-review-delegation",
	})
	if failed || changed["effect"] != "allow" {
		t.Fatalf("Agent manager MCP change failed %v", changed)
	}
	if _, failed := call("project_access_change", map[string]any{
		"project_id": pid, "principal_id": otherID.String(), "permission": "access:manage",
		"effect": "allow", "expected_version": 0,
		"reason":          "Must be Human Workspace Admin to re-delegate management",
		"idempotency_key": "phase8-mcp-block-privilege-escalation",
	}); !failed {
		t.Fatal("MCP agent delegated protected management ability")
	}
	var updates int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.project_permission_changes
 WHERE project_id=$1 AND principal_id=$2`, f.project, otherID).Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("MCP change not audit-logged: %d %v", updates, err)
	}
	// The declared tool is safe against arbitrary paths / an unseen project.
	bad, failed := call("project_access_self", map[string]any{"project_id": uuid.NewString()})
	if !failed {
		t.Fatalf("foreign access succeeded: %v", bad)
	}
}
