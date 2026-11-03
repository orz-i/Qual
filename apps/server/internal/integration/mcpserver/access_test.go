package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestLocalMCPAllToolsRegisteredWithoutBackend(t *testing.T) {
	ctx := context.Background()
	server := New(Bridge{APIURL: "http://127.0.0.1:18080", Bearer: "not-a-token"})
	transportServer, transportClient := mcp.NewInMemoryTransports()
	s, err := server.Connect(ctx, transportServer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "qual-access-unit-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, transportClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	const want = 34
	if len(tools.Tools) != want {
		t.Fatalf("stdio declared tools=%d want %d", len(tools.Tools), want)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"project_access_self", "project_access_list", "project_access_change",
		"work_item_transition", "activity_search", "test_case_search", "artifact_get", "work_role_entries_search", "work_role_entry_record",
		"project_review_policy_get", "project_review_policy_change", "work_acceptance_get", "work_item_complete",
		"work_review_policy_get", "work_review_policy_change"} {
		if !names[name] {
			t.Fatalf("MCP missing %s", name)
		}
	}
}
