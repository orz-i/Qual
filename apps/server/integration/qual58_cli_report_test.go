//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A real Go CLI process must preserve one-command test report.
// It records a Run with a task binding, and backend auto-links legitimate
// new coverage. Qual never executes external tests/CI.
func TestQual58RealCLIReportCreatesBoundRunAndCoverageInOneCommand(t *testing.T) {
	f, x, _, _, _, ta, _ := qual58Fixture(t)
	caseID, _ := mustCase(t, f, "qual58-cli-report-"+uuid.NewString()[:8], "Fresh published Case only")
	version, _ := publishCase(t, f, caseID, "qual58-cli-report-publish-"+uuid.NewString()[:8], 1)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./cmd/qual", "test", "report",
		"-project", f.project.String(),
		"-case-version", version,
		"-work-item", x,
		"-title", "Externally reported synthetic fixture observation",
		"-status", "passed",
		"-note", "Synthetic observed passed from actual isolated integration test, not executed by Qual",
		"-source", "external-cli",
		"-request-key", "qual58-cli-real-bound-report-"+uuid.NewString()[:8])
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "QUAL_API_URL="+f.server.URL, "QUAL_TOKEN="+ta)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("actual CLI test report failed: %v (%s)", err, output)
	}
	var receipt struct {
		RunID          uuid.UUID `json:"test_run_id"`
		AttemptID      uuid.UUID `json:"attempt_id"`
		WorkItemID     uuid.UUID `json:"work_item_id"`
		CoverageLinked bool      `json:"coverage_linked"`
	}
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatalf("CLI response not valid receipt: %v (%s)", err, output)
	}
	if receipt.RunID == uuid.Nil || receipt.AttemptID == uuid.Nil ||
		receipt.WorkItemID.String() != x || !receipt.CoverageLinked {
		t.Fatalf("CLI did not bind WorkItem/coverage/Attempt: %+v", receipt)
	}
	var bound *uuid.UUID
	if err := f.pool.QueryRow(context.Background(),
		`SELECT work_item_id FROM qual.test_runs WHERE project_id=$1 AND id=$2`,
		f.project, receipt.RunID).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound == nil || bound.String() != x {
		t.Fatalf("CLI generated unbound Run, got %v", bound)
	}
	var links int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM qual.test_case_coverage
 WHERE project_id=$1 AND work_item_id=$2 AND test_case_id=$3`,
		f.project, x, caseID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("CLI forgot/duplicated WorkItem coverage links: %d", links)
	}
	t.Log("real CLI subprocess reported one bound Run + Attempt and one WorkItem coverage without manual setup")
}
