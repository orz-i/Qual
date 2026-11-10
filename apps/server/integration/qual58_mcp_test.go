//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

// The real MCP stdio handler routes through authenticated REST, including
// its new immutable WorkItem binding and per-attempt authorization.
func TestQual58MCPBoundRunToActualAttemptAndRevocation(t *testing.T) {
	f, x, _, _, version, ta, tb := qual58Fixture(t)
	ctx := context.Background()
	makeSession := func(token string) *mcp.ClientSession {
		t.Helper()
		server := mcpserver.New(mcpserver.Config{APIURL: f.server.URL, Bearer: token})
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "qual58-mcp-verified", Version: "1"}, nil)
		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
		return cs
	}
	call := func(session *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("MCP %s returned transport error %v", name, err)
		}
		if res.IsError {
			raw, _ := json.Marshal(res.Content)
			return nil, string(raw)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		return d, ""
	}
	a := makeSession(ta)
	args := map[string]any{"project_id": f.project.String(), "name": "QUAL-58 scoped MCP fact",
		"case_version_ids": []string{version}, "work_item_id": x,
		"source_system": "external-mcp", "idempotency_key": "qual58-mcp-bound-create"}
	run, reason := call(a, "test_run_create", args)
	if reason != "" || run["work_item_id"] != x {
		t.Fatalf("MCP Run lost WorkItem binding %v %s", run, reason)
	}
	id := run["id"].(string)
	item := run["items"].([]any)[0].(map[string]any)
	runItem := item["id"].(string)
	if _, err := uuid.Parse(id); err != nil {
		t.Fatal(err)
	}
	read, reason := call(a, "test_run_get", map[string]any{"project_id": f.project.String(), "run_id": id})
	if reason != "" || read["work_item_id"] != x {
		t.Fatalf("MCP private scoped Run unreadable %v %s", read, reason)
	}
	attempt, reason := call(a, "test_result_record", map[string]any{
		"project_id": f.project.String(), "item_id": runItem, "idempotency_key": "qual58-mcp-scoped-attempt",
		"status": "passed", "note": "Synthetic local test observed in real PostgreSQL",
	})
	if reason != "" || attempt["status"] != "passed" {
		t.Fatalf("MCP real attempt missing %v %s", attempt, reason)
	}
	b := makeSession(tb)
	_, reason = call(b, "test_run_get", map[string]any{"project_id": f.project.String(), "run_id": id})
	if !strings.Contains(reason, "PERMISSION_DENIED") {
		t.Fatalf("B read unrelated MCP Run %s", reason)
	}
	if code, _ := qual56Write(t, f, ta, x, "release", "qual58-mcp-release-executor", 4, uuid.Nil); code != 200 {
		t.Fatalf("release %d", code)
	}
	_, reason = call(a, "test_result_record", map[string]any{
		"project_id": f.project.String(), "item_id": runItem, "idempotency_key": "qual58-mcp-revoked-write",
		"status": "passed", "note": "Not accepted after release",
	})
	if !strings.Contains(reason, "PERMISSION_DENIED") {
		t.Fatalf("released A still wrote MCP Attempt %s", reason)
	}
}
