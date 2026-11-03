//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func TestMCPTestingRecordsUseREST(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	server := mcpserver.New(mcpserver.Bridge{APIURL: f.server.URL, Bearer: f.agent})
	a, b := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "phase4-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			rawError, _ := json.Marshal(result.Content)
			t.Fatalf("%s: %v %s", name, err, rawError)
		}
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	pid := f.project.String()
	c := call("test_case_create", map[string]any{"project_id": pid, "idempotency_key": "mcp-case-record-1",
		"specification": map[string]any{"title": "MCP regression", "preconditions": "", "steps": []any{}, "expected_result": "ok"}})
	v := call("test_case_publish", map[string]any{"project_id": pid, "case_id": c["id"], "expected_revision": 1, "idempotency_key": "mcp-case-publish-1"})
	run := call("test_run_create", map[string]any{"project_id": pid, "idempotency_key": "mcp-run-record-1", "name": "external results", "case_version_ids": []any{v["id"]}})
	item := run["items"].([]any)[0].(map[string]any)
	attempt := call("test_result_record", map[string]any{"project_id": pid, "item_id": item["id"], "status": "failed", "note": "External observation", "idempotency_key": "mcp-result-record-1"})
	if attempt["status"] != "failed" {
		t.Fatalf("wrong status: %v", attempt)
	}
	history := call("test_attempts_search", map[string]any{"project_id": pid, "item_id": item["id"]})
	if len(history["items"].([]any)) != 1 {
		t.Fatalf("MCP result not recorded: %v", history)
	}
	defect := call("defect_create_from_failure", map[string]any{"project_id": pid, "attempt_id": attempt["id"], "title": "MCP discovered bug", "idempotency_key": "mcp-defect-record-1"})
	if defect["created"] != true {
		t.Fatalf("bug not created: %v", defect)
	}
	if status, _, _ := request(t, "GET", f.url(f.project)+"/"+defect["bug_id"].(string), f.agent, "", "", nil); status != 200 {
		t.Fatalf("REST not consistent with MCP: %d", status)
	}
}
