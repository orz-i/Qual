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

func TestQual56RemoteAndLocalMCPTransfer(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a, b := principalFor(t, f, f.agent), principalFor(t, f, f.agent2)
	item := create(t, f, f.agent, "qual56-mcp-task")
	id := item["id"].(string)
	qual56Claim(t, f, f.agent, id, "qual56-mcp-claim-a", 1)
	type peer struct {
		client *mcp.ClientSession
		server *mcp.ServerSession
	}
	connect := func(bridge mcpserver.Config, label string) *peer {
		t.Helper()
		server := mcpserver.New(bridge)
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: label, Version: "1"}, nil)
		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
		return &peer{client: cs, server: ss}
	}
	call := func(p *peer, name string, version int, target string, key string) (bool, map[string]any, string) {
		t.Helper()
		args := map[string]any{"project_id": f.project.String(), "item_id": id, "expected_version": version, "idempotency_key": key}
		if target != "" {
			args["new_assignee_id"] = target
		}
		res, err := p.client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("MCP %s invocation: %v", name, err)
		}
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			return false, nil, string(raw)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var result struct {
			WorkItem map[string]any `json:"work_item"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return true, result.WorkItem, ""
	}
	remoteA := connect(mcpserver.Config{APIURL: f.server.URL, Bearer: f.agent}, "qual56-remote-agent-a")
	ok, work, _ := call(remoteA, "work_item_reassign", 2, b.String(), "qual56-mcp-a-to-b")
	if !ok || work["assignee_id"] != b.String() || work["version"] != float64(3) {
		t.Fatalf("remote reassign failed %v %+v", ok, work)
	}
	ok, _, message := call(remoteA, "work_item_release", 3, "", "qual56-mcp-a-old-release")
	if ok || !strings.Contains(message, "PERMISSION_DENIED") {
		t.Fatalf("old actor remote release was not denied %s", message)
	}
	remoteB := connect(mcpserver.Config{APIURL: f.server.URL, Bearer: f.agent2}, "qual56-remote-agent-b")
	ok, work, _ = call(remoteB, "work_item_release", 3, "", "qual56-mcp-b-release")
	if !ok || work["assignee_id"] != nil || work["version"] != float64(4) {
		t.Fatalf("remote release failed %v %+v", ok, work)
	}
	qual56Claim(t, f, f.agent2, id, "qual56-mcp-b-reclaim", 4)
	ok, work, _ = call(remoteB, "work_item_reassign", 5, a.String(), "qual56-mcp-remote-b-to-a")
	if !ok || work["assignee_id"] != a.String() || work["version"] != float64(6) {
		t.Fatalf("REST MCP reassign mismatch %v %+v", ok, work)
	}
	status, read, _ := request(t, "GET", f.url(f.project)+"/"+id, f.agent, "", "", nil)
	if status != 200 || read["assignee_id"] != a.String() {
		t.Fatalf("MCP/REST drift %d %+v", status, read)
	}
	_ = remoteA.server
}
