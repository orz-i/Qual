//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalCLIPhase4FactWorkflow(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "qual-cli")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/qual")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	invoke := func(args ...string) map[string]any {
		t.Helper()
		command := exec.CommandContext(ctx, binary, append([]string{"test"}, args...)...)
		command.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+f.agent)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v error: %v %s", args, err, output)
		}
		var data map[string]any
		if err := json.Unmarshal(output, &data); err != nil {
			t.Fatalf("CLI JSON %v: %s", err, output)
		}
		return data
	}
	pid := f.project.String()
	c := invoke("case-create", "-project", pid, "-title", "CLI documented regression")
	version := invoke("publish", "-project", pid, "-case", c["id"].(string), "-version", "1")
	run := invoke("run-create", "-project", pid, "-title", "CLI external results", "-versions", version["id"].(string))
	itemID := run["items"].([]any)[0].(map[string]any)["id"].(string)
	attempt := invoke("record", "-project", pid, "-item", itemID, "-status", "failed", "-note", "external failure")
	if attempt["status"] != "failed" {
		t.Fatalf("wrong CLI result %v", attempt)
	}
	defect := invoke("defect", "-project", pid, "-attempt", attempt["id"].(string), "-title", "CLI discovered Bug")
	if defect["created"] != true {
		t.Fatalf("CLI Bug not created: %v", defect)
	}
	file := filepath.Join(t.TempDir(), "ci-report.xml")
	xml := fmt.Sprintf(`<testsuite><testcase name="runner-case"><properties><property name="qual.case_key" value="%s"/></properties><failure message="JUnit reported error"/></testcase></testsuite>`, c["key"].(string))
	if err := os.WriteFile(file, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	imported := invoke("import", "-project", pid, "-run", run["id"].(string), "-file", file)
	if imported["failed"] != float64(1) {
		t.Fatalf("CLI JUnit import missing: %v", imported)
	}
	history := invoke("attempts", "-project", pid, "-item", itemID)
	if len(history["items"].([]any)) != 2 {
		t.Fatalf("CLI history missing after JUnit import %v", history)
	}
}
