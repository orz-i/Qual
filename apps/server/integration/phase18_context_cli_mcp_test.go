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

func TestQual28ActualStdioReconnectAndImmediateRevocation(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "qual28-mcp-external-stdio-reconnect")
	wi := work["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	readOnce := func() (*mcp.CallToolResult, error) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "go", "run", "../cmd/qual-mcp")
		cmd.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+f.agent)
		client := mcp.NewClient(&mcp.Implementation{Name: "new-pi-session", Version: "1.0"}, nil)
		sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
		if err != nil {
			return nil, err
		}
		defer sess.Close()
		return sess.CallTool(ctx, &mcp.CallToolParams{Name: "work_item_get_context", Arguments: map[string]any{
			"project_id": f.project.String(), "item_id": wi, "section": "overview", "limit": 3}})
	}
	one, err := readOnce()
	if err != nil || one.IsError {
		t.Fatalf("actual first stdio MCP v2 failed: %v %+v", err, one)
	}
	two, err := readOnce()
	if err != nil || two.IsError {
		t.Fatalf("actual independent stdio resume failed: %v %+v", err, two)
	}
	for _, result := range []*mcp.CallToolResult{one, two} {
		obj := result.StructuredContent.(map[string]any)
		if obj["schema_version"] != float64(2) || obj["context_revision"] != float64(1) {
			t.Fatalf("stdio v2 did not resume persisted version: %+v", obj)
		}
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE id=$1", f.agentCredential); err != nil {
		t.Fatal(err)
	}
	denied, err := readOnce()
	if err != nil || denied == nil || !denied.IsError {
		t.Fatalf("revoked real stdio process reused credentials: %v %+v", err, denied)
	}
}

// Actual independent client processes use one PostgreSQL-backed fixture.
// The original failed Attempt is never replaced by a later Passed report.
func TestQual28StdioContextV1V2AndCLIResume(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "qual28-cli-mcp-recovery")
	id := work["id"].(string)
	caseID, _ := mustCase(t, f, "qual28-case", "Recover a real immutable report")
	vID, _ := publishCase(t, f, caseID, "qual28-publish", 1)
	run := mustRun(t, f, []string{vID}, "qual28-run")
	runItemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	failed := mustAttempt(t, f, runItemID, f.agent, "qual28-failed", "failed")
	_ = mustAttempt(t, f, runItemID, f.agent, "qual28-passed", "passed")
	status, _ := testRequest(t, "POST", testsURL(f)+"/test-cases/"+caseID+"/coverage", f.agent,
		"qual28-coverage", "", map[string]any{"work_item_id": id})
	if status != 201 {
		t.Fatalf("coverage: HTTP%d", status)
	}
	status, _, _ = request(t, "POST", threadBase(f, id), f.agent, "qual28-thread", "",
		threadInput("example/project/pull/91", 1))
	if status != 201 {
		t.Fatalf("thread link: HTTP%d", status)
	}

	ctx := context.Background()
	call := func(token string, args map[string]any) (*mcp.CallToolResult, error) {
		t.Helper()
		server := mcpserver.New(mcpserver.Config{APIURL: f.server.URL, Bearer: token})
		st, ct := mcp.NewInMemoryTransports()
		session, err := server.Connect(ctx, st, nil)
		if err != nil {
			return nil, err
		}
		defer session.Close()
		c := mcp.NewClient(&mcp.Implementation{Name: "qual28-session", Version: "0.1"}, nil)
		conn, err := c.Connect(ctx, ct, nil)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return conn.CallTool(ctx, &mcp.CallToolParams{Name: "work_item_get_context", Arguments: args})
	}
	args := map[string]any{"project_id": f.project.String(), "item_id": id}
	defaultContext, err := call(f.agent, args)
	if err != nil || defaultContext.IsError {
		t.Fatalf("default MCP overview: %v", err)
	}
	defaultFacts := defaultContext.StructuredContent.(map[string]any)
	if defaultFacts["schema_version"] != float64(2) || defaultFacts["section"] != "overview" ||
		defaultFacts["context_revision"] != float64(2) {
		t.Fatalf("default MCP must use bounded authoritative Context v2: %+v", defaultFacts)
	}
	if _, retained := defaultFacts["recent_comments"]; retained {
		t.Fatal("obsolete MCP v1 multi-request synthesis still active")
	}
	args["section"] = "attempts"
	args["limit"] = 2
	v2, err := call(f.agent, args)
	if err != nil || v2.IsError {
		t.Fatalf("fresh MCP v2: %v %+v", err, v2)
	}
	facts := v2.StructuredContent.(map[string]any)
	if facts["schema_version"] != float64(2) {
		t.Fatalf("context v2 missing: %+v", facts)
	}
	attempts := facts["items"].([]any)
	if len(attempts) != 2 || attempts[1].(map[string]any)["source_id"] != failed["id"] ||
		attempts[1].(map[string]any)["status"] != "failed" {
		t.Fatalf("previous Failed Attempt erased by newer report: %v", attempts)
	}
	workOnly := workOnlyContextPrincipal(t, f)
	denied, err := call(workOnly, args)
	if err != nil || !denied.IsError {
		t.Fatalf("viewer bypassed test:read: %v %+v", err, denied)
	}
	args["section"] = "audit"
	invalid, err := call(f.agent, args)
	if err != nil || !invalid.IsError {
		t.Fatalf("unrestricted audit section accepted: %v", err)
	}

	cli := filepath.Join(t.TempDir(), "qual")
	build := exec.Command("go", "build", "-o", cli, "../cmd/qual")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CLI build failure (redacted): %v %d", err, len(output))
	}
	invoke := func(token string, command ...string) (int, map[string]any) {
		t.Helper()
		cmd := exec.Command(cli, command...)
		env := []string{}
		for _, e := range os.Environ() {
			if len(e) >= 11 && (e[:11] == "QUAL_TOKEN=" || e[:11] == "QUAL_API_UR") {
				continue
			}
			env = append(env, e)
		}
		cmd.Env = append(env, "QUAL_TOKEN="+token, "QUAL_API_URL="+f.server.URL)
		result, err := cmd.Output()
		if err != nil {
			if ex, ok := err.(*exec.ExitError); ok {
				return ex.ExitCode(), nil
			}
			t.Fatal(err)
		}
		var obj map[string]any
		if err := json.Unmarshal(result, &obj); err != nil {
			t.Fatal(err)
		}
		return 0, obj
	}
	opts := []string{"-project", f.project.String(), "-item", id}
	code, page := invoke(f.agent, append([]string{"context"}, append(opts, "-section", "attempts", "-limit", "2")...)...)
	if code != 0 || page["schema_version"] != float64(2) {
		t.Fatalf("qual context failed: %d %+v", code, page)
	}
	code, resume := invoke(f.agent, append([]string{"resume"}, opts...)...)
	if code != 0 || resume["schema_version"] != float64(2) ||
		resume["consistency"] != "best_effort" {
		t.Fatalf("qual resume failed: %d %+v", code, resume)
	}
	sections := resume["sections"].(map[string]any)
	if len(sections["attempts"].(map[string]any)["items"].([]any)) != 2 ||
		sections["threads"] == nil {
		t.Fatalf("resume failed to reconstruct factual dependencies: %+v", sections)
	}
	code, limited := invoke(workOnly, append([]string{"resume"}, opts...)...)
	if code != 0 {
		t.Fatalf("work:read viewer resume should work, got %d", code)
	}
	omitted := limited["omitted_sections"].(map[string]any)
	if omitted["attempts"] != "not_authorized" || omitted["artifacts"] != "not_authorized" {
		t.Fatalf("viewer received protected context: %+v", limited)
	}
	// The next independent remote client starts with no retained snapshot.
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE id=$1", f.agentCredential); err != nil {
		t.Fatal(err)
	}
	revoked, err := call(f.agent, map[string]any{"project_id": f.project.String(), "item_id": id, "section": "overview"})
	if err != nil || !revoked.IsError {
		t.Fatalf("MCP used stale session permission: %v %+v", err, revoked)
	}
	code, _ = invoke(f.agent, append([]string{"context"}, opts...)...)
	if code != 3 {
		t.Fatalf("revoked CLI credential accepted, exit=%d", code)
	}
}
