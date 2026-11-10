//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

// The read-only allowed_actions projection must track exactly the real
// persisted assignee, reviewer, policy and credential facts used by writes.
func TestQual59TaskCapabilitiesFollowClaimTransferAndDeny(t *testing.T) {
	f, x, y, _, _, ta, tb := qual58Fixture(t)
	get := func(token, id string) (int, map[string]any) {
		t.Helper()
		code, data, _ := request(t, "GET", f.url(f.project)+"/"+id+"/capabilities", token, "", "", nil)
		return code, data
	}
	allowed := func(d map[string]any, a string) bool {
		actions, ok := d["allowed_actions"].([]any)
		if !ok {
			return false
		}
		for _, value := range actions {
			if value == a {
				return true
			}
		}
		return false
	}
	if code, cap := get(ta, y); code != 200 || cap["project_mode"] != "task_scoped" ||
		!allowed(cap, "task.claim") || allowed(cap, "task.update") {
		t.Fatalf("unclaimed Y: HTTP%d %v", code, cap)
	}
	if code, cap := get(ta, x); code != 200 || cap["work_version"] != float64(4) ||
		!allowed(cap, "task.update") || !allowed(cap, "task.report_test") || allowed(cap, "task.claim") ||
		!allowed(cap, "task.submit_review") {
		t.Fatalf("A scoped X: HTTP%d %v", code, cap)
	}
	if code, cap := get(tb, x); code != 200 || allowed(cap, "task.update") ||
		allowed(cap, "task.report_test") || cap["work_version"] != float64(4) {
		t.Fatalf("B has task X privileges before claim: HTTP%d %v", code, cap)
	}
	// The same B may be Executor on Y, yet retain no X privileges.
	qual56Claim(t, f, tb, y, "qual59-capabilities-b-claims-y", 1)
	if code, cap := get(tb, y); code != 200 || !allowed(cap, "task.update") || allowed(cap, "task.claim") {
		t.Fatalf("B cannot see own Y executor capability: HTTP%d %v", code, cap)
	}
	if code, cap := get(ta, y); code != 200 || allowed(cap, "task.update") {
		t.Fatalf("A sees B-only capability Y: HTTP%d %v", code, cap)
	}
	// JSON does not contain anybody else's actual token/scope or target id.
	if code, cap := get(ta, x); code != 200 {
		t.Fatal(code)
	} else {
		if _, ok := cap["credential_scopes"]; ok {
			t.Fatal("exposed credential scopes")
		}
		if _, ok := cap["other_principals"]; ok {
			t.Fatal("exposed other principals")
		}
	}
	if status, cap := get(f.agent2, x); status != 200 || allowed(cap, "task.update") {
		t.Fatalf("broad legacy work:write bypassed task projection HTTP%d %v", status, cap)
	}
	qual56Status(t, f, ta, x, "in_review", "qual59-a-submits-review", 4)
	if code, cap := get(ta, x); code != 200 || allowed(cap, "task.update") ||
		allowed(cap, "task.report_test") || !allowed(cap, "task.comment") {
		t.Fatalf("A retained executing permissions in_review: HTTP%d %v", code, cap)
	}
	if code, cap := get(tb, x); code != 200 || !allowed(cap, "task.claim_review") ||
		allowed(cap, "task.record_review") || allowed(cap, "task.update") {
		t.Fatalf("B reviewer candidate projection HTTP%d %v", code, cap)
	}
	if code, cap := get(ta, x); code != 200 || allowed(cap, "task.claim_review") ||
		cap["denied_reasons"].(map[string]any)["task.claim_review"] == nil {
		t.Fatalf("self-review actor A wrongly obtained independent claim: HTTP%d %v", code, cap)
	}
	if code, ctx, _ := request(t, "GET", f.url(f.project)+"/"+x+"/context?section=overview", ta, "", "", nil); code != 200 {
		t.Fatalf("task context not readable by current Executor HTTP%d %v", code, ctx)
	} else {
		caps, ok := ctx["capabilities"].(map[string]any)
		if !ok || caps["work_version"] != float64(5) || caps["project_mode"] != "task_scoped" {
			t.Fatalf("WorkContext missing same versioned capabilities: %v", ctx)
		}
	}
	if code, cap := qual57ReviewAction(t, f, tb, x, "review-claim", "qual59-b-takes-review", 5); code != 200 || cap["version"] != float64(6) {
		t.Fatalf("B claim review failed: %d %v", code, cap)
	}
	if code, cap := get(tb, x); code != 200 || !allowed(cap, "task.record_review") ||
		!allowed(cap, "task.release_review") || allowed(cap, "task.claim_review") {
		t.Fatalf("B assigned reviewer projection HTTP%d %v", code, cap)
	}
	if code, cap := get(ta, x); code != 200 || allowed(cap, "task.record_review") {
		t.Fatalf("A gained B Reviewer rights HTTP%d %v", code, cap)
	}
	if code, _ := qual57ReviewAction(t, f, tb, x, "review-release", "qual59-b-relinquish-review", 6); code != 200 {
		t.Fatal(code)
	}
	if code, cap := get(tb, x); code != 200 || allowed(cap, "task.record_review") ||
		allowed(cap, "task.release_review") || !allowed(cap, "task.claim_review") {
		t.Fatalf("Reviewer release did not immediately update projection HTTP%d %v", code, cap)
	}
	if code, _ := qual57ReviewAction(t, f, tb, x, "review-claim", "qual59-b-claims-review-again", 7); code != 200 {
		t.Fatal(code)
	}
	qual56Status(t, f, tb, x, "in_progress", "qual59-b-returns-x-to-work", 8)
	if code, cap := get(tb, x); code != 200 || allowed(cap, "task.record_review") ||
		allowed(cap, "task.release_review") {
		t.Fatalf("review return retained reviewer ability HTTP%d %v", code, cap)
	}
	// Project explicit Deny immediately removes A's current task write.
	a := principalFor(t, f, f.agent)
	if code, _ := setPermission(t, f, f.admin, a, "work:write", "deny", 0, "qual59-a-explicit-write-deny"); code != 200 {
		t.Fatal(code)
	}
	if code, cap := get(ta, x); code != 200 || allowed(cap, "task.update") ||
		cap["denied_reasons"].(map[string]any)["task.update"] != "project_explicit_deny" {
		t.Fatalf("task projection ignored project explicit Deny HTTP%d %v", code, cap)
	}
	if code, cap := get(ta, y); code != 200 || allowed(cap, "task.update") {
		t.Fatalf("A unexpectedly can edit B task after Deny %d %v", code, cap)
	}
	// B remains independent from A's deny, and becomes current Executor on
	// X only after a valid authenticated Reassign, never from project scope.
	if code, moved := qual56Write(t, f, f.admin, x, "reassign", "qual59-manager-handoff-x-to-b", 9, principalFor(t, f, f.agent2)); code != 200 || moved["assignee_id"] != principalFor(t, f, f.agent2).String() {
		t.Fatalf("manager transfer X to B failed: %d %v", code, moved)
	}
	if code, cap := get(ta, x); code != 200 || allowed(cap, "task.update") {
		t.Fatalf("A retained X after transfer %d %v", code, cap)
	}
	if code, cap := get(tb, x); code != 200 || !allowed(cap, "task.update") || cap["work_version"] != float64(10) {
		t.Fatalf("B missing X after transfer %d %v", code, cap)
	}
	if code, _, _ := request(t, "GET",
		f.url(f.otherProject)+"/"+x+"/capabilities", ta, "", "", nil); code != 404 {
		t.Fatalf("cross project capability resource existence leaked: HTTP%d", code)
	}
}

func TestQual59LegacyCapabilitiesAreOptionalAndNonEscalating(t *testing.T) {
	f := setup(t)
	x := create(t, f, f.agent, "qual59-legacy-capability")
	path := f.url(f.project) + "/" + x["id"].(string)
	code, cap, _ := request(t, "GET", path+"/capabilities", f.agent, "", "", nil)
	if code != 200 || cap["project_mode"] != "legacy" || cap["work_version"] != float64(1) {
		t.Fatalf("legacy capability unavailable on old Goose17: HTTP%d %v", code, cap)
	}
	if _, ok := cap["allowed_actions"]; !ok {
		t.Fatal("legacy allowed_actions missing")
	}
	if code, cap, _ := request(t, "GET", path+"/capabilities", f.viewer, "", "", nil); code != 200 {
		t.Fatalf("legacy Viewer cannot view safe caps %d %v", code, cap)
	} else {
		for _, value := range cap["allowed_actions"].([]any) {
			if value == "task.update" || value == "task.claim" || value == "task.complete_independent" {
				t.Fatalf("legacy Viewer escalated task action %v", value)
			}
		}
	}
	if code, ctx, _ := request(t, "GET", path+"/context?section=overview", f.agent, "", "", nil); code != 200 {
		t.Fatalf("legacy WorkContext failed HTTP%d %v", code, ctx)
	} else if caps, ok := ctx["capabilities"].(map[string]any); !ok || caps["project_mode"] != "legacy" {
		t.Fatalf("legacy WorkContext capability enrichment missing: %v", ctx)
	}
	if code, cap, _ := request(t, "GET",
		f.url(f.otherProject)+"/"+x["id"].(string)+"/capabilities", f.agent, "", "", nil); code != 404 {
		t.Fatalf("foreign WorkItem capabilities exposed %d %v", code, cap)
	}
}

// The real stdio REST-backed MCP projection must match the HTTP capability
// endpoint, with no redundant in-process bypass adapter.
func TestQual59MCPAndRESTCapabilitiesMatch(t *testing.T) {
	f, x, _, _, _, ta, _ := qual58Fixture(t)
	ctx := context.Background()
	connect := func(bridge mcpserver.Config) *mcp.ClientSession {
		t.Helper()
		server := mcpserver.New(bridge)
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "qual59-real-mcp-projection", Version: "1"}, nil)
		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
		return cs
	}
	call := func(session *mcp.ClientSession) (map[string]any, string) {
		t.Helper()
		out, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "work_item_capabilities_get",
			Arguments: map[string]any{"project_id": f.project.String(), "item_id": x}})
		if err != nil {
			t.Fatal(err)
		}
		if out.IsError {
			b, _ := json.Marshal(out.Content)
			return nil, string(b)
		}
		b, _ := json.Marshal(out.StructuredContent)
		var cap map[string]any
		if err := json.Unmarshal(b, &cap); err != nil {
			t.Fatal(err)
		}
		return cap, ""
	}
	remote := connect(mcpserver.Config{APIURL: f.server.URL, Bearer: ta})
	one, errOne := call(remote)
	if errOne != "" {
		t.Fatalf("MCP failed remote=%s", errOne)
	}
	code, two, _ := request(t, "GET", f.url(f.project)+"/"+x+"/capabilities", ta, "", "", nil)
	if code != 200 {
		t.Fatalf("REST capabilities status %d", code)
	}
	for _, field := range []string{"project_mode", "work_item_id", "work_version", "policy_version", "review_mode"} {
		if one[field] != two[field] {
			t.Errorf("MCP/REST drift %s MCP %v REST %v", field, one[field], two[field])
		}
	}
	if b1, _ := json.Marshal(one["allowed_actions"]); len(b1) == 0 {
		t.Fatal("MCP returned no actions")
	} else if b2, _ := json.Marshal(two["allowed_actions"]); string(b1) != string(b2) {
		t.Fatalf("MCP/REST action order drift mcp=%s rest=%s", b1, b2)
	}
	if one["work_version"] != float64(4) {
		t.Fatalf("MCP stale version: %v", one)
	}
	_, err := uuid.Parse(x)
	if err != nil {
		t.Fatal(err)
	}
}
