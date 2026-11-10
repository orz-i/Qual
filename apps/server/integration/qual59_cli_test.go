//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Test the actual built Go CLI process, not a mocked request map. The CLI
// is a read-only consumer of server-authoritative capability facts; no extra
// agent identity, policy token or project permission is minted.
func TestQual59CLIReadCapabilitiesAndResume(t *testing.T) {
	f, x, y, _, _, ta, _ := qual58Fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	read := func(args ...string) map[string]any {
		t.Helper()
		cmd := exec.CommandContext(ctx, "go", append([]string{"run", "./cmd/qual"}, args...)...)
		cmd.Dir = ".."
		cmd.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+ta)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("actual CLI %v failed: %v (%s)", args, err, raw)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("actual CLI JSON response malformed: %v (%s)", err, raw)
		}
		return data
	}
	xCap := read("capabilities", "-project", f.project.String(), "-item", x)
	if xCap["project_mode"] != "task_scoped" || xCap["work_version"] != float64(4) ||
		xCap["my_capacity"] == nil || xCap["allowed_actions"] == nil {
		t.Fatalf("CLI did not show current scoped task role/actions: %v", xCap)
	}
	yCap := read("capabilities", "-project", f.project.String(), "-item", y)
	if yCap["work_version"] != float64(1) || yCap["project_mode"] != "task_scoped" {
		t.Fatalf("CLI showed stale or cross-task role data: %v", yCap)
	}
	xResume := read("resume", "-project", f.project.String(), "-item", x)
	if xResume["schema_version"] != float64(2) || xResume["context_revision"] != float64(4) {
		t.Fatalf("CLI resume lost bounded context contract: %v", xResume)
	}
	nested, ok := xResume["capabilities"].(map[string]any)
	if !ok || nested["work_version"] != float64(4) || nested["project_mode"] != "task_scoped" {
		t.Fatalf("CLI resume did not include same service capability projection: %v", xResume)
	}
	t.Log("real CLI capabilities and resume return current task-specific server actions")
}
