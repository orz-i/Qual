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

func TestQual57MCPRemoteAndDirectReviewAssignment(t *testing.T) {
	f := setup(t)
	id, _, ta, tb, _, _ := qual57Prepare(t, f, "qual57-mcp")
	ctx := context.Background()
	connect := func(bridge mcpserver.Config, label string) *mcp.ClientSession {
		t.Helper()
		server := mcpserver.New(bridge)
		st, ct := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: label, Version: "1"}, nil)
		session, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close(); _ = serverSession.Close() })
		return session
	}
	call := func(session *mcp.ClientSession, tool, key string, expected int) (bool, map[string]any, string) {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: tool, Arguments: map[string]any{
				"project_id": f.project.String(), "item_id": id,
				"expected_version": expected, "idempotency_key": key,
			},
		})
		if err != nil {
			t.Fatalf("MCP %s invocation: %v", tool, err)
		}
		if result.IsError {
			raw, _ := json.Marshal(result.Content)
			return false, nil, string(raw)
		}
		raw, _ := json.Marshal(result.StructuredContent)
		var value struct {
			WorkItem map[string]any `json:"work_item"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return true, value.WorkItem, ""
	}
	remoteB := connect(mcpserver.Config{APIURL: f.server.URL, Bearer: tb}, "qual57-remote-reviewer-b")
	if ok, w, _ := call(remoteB, "work_item_review_claim", "qual57-remote-b-claim", 5); !ok || w["version"] != float64(6) {
		t.Fatalf("remote Reviewer claim failed %v %v", ok, w)
	}
	remoteA := connect(mcpserver.Config{APIURL: f.server.URL, Bearer: ta}, "qual57-remote-executor-a")
	if ok, _, reason := call(remoteA, "work_item_review_release", "qual57-executor-cannot-release", 6); ok || !strings.Contains(reason, "PERMISSION_DENIED") {
		t.Fatalf("executor remotely released reviewer %s", reason)
	}
	if ok, w, _ := call(remoteB, "work_item_review_release", "qual57-remote-b-release", 6); !ok || w["version"] != float64(7) {
		t.Fatalf("REST Reviewer release failed %v %v", ok, w)
	}
	if ok, w, _ := call(remoteB, "work_item_review_claim", "qual57-remote-b-reclaim", 7); !ok || w["version"] != float64(8) {
		t.Fatalf("remote Reviewer reclaim failed %v %v", ok, w)
	}
	if ok, _, reason := call(remoteB, "work_item_review_claim", "qual57-remote-b-double-claim", 8); ok || !strings.Contains(reason, "VERSION_CONFLICT") {
		t.Fatalf("double review claim succeeded unexpectedly: %s", reason)
	}
}
