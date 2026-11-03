package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// runTesting is strictly a REST client; no DB credentials or test execution.
func runTesting(ctx context.Context, c *client, args []string) error {
	if len(args) == 0 {
		return errors.New("test subcommands: cases|case-create|case-show|case-versions|publish|runs|run-create|report|attempts|record|defect|import")
	}
	cmd := args[0]
	fs := flag.NewFlagSet("test "+cmd, flag.ContinueOnError)
	project := fs.String("project", "", "project key or UUID")
	caseID := fs.String("case", "", "TestCase UUID")
	runID := fs.String("run", "", "TestRun UUID")
	itemID := fs.String("item", "", "RunItem UUID")
	attemptID := fs.String("attempt", "", "Attempt UUID")
	title := fs.String("title", "", "title of new case/run/Bug")
	expected := fs.Int64("version", 0, "draft revision for publish")
	versionIDs := fs.String("versions", "", "comma-separated immutable CaseVersion UUIDs")
	caseVersion := fs.String("case-version", "", "single published CaseVersion UUID for test report")
	workItem := fs.String("work-item", "", "optional WorkItem UUID to link this TestCase as coverage")
	status := fs.String("status", "", "passed|failed|blocked|skipped")
	note := fs.String("note", "", "reported observation")
	source := fs.String("source", "manual", "run source system")
	reportSource := fs.String("report-source", "external-cli", "source of external test report (report only)")
	environment := fs.String("environment", "", "test environment")
	buildRef := fs.String("build", "", "commit/build reference")
	filename := fs.String("file", "", "local JUnit XML path (max 1 MiB)")
	bugID := fs.String("bug", "", "existing Bug UUID for defect linking")
	keyFlag := fs.String("request-key", "", "explicit idempotency key for safe retries")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("-project required")
	}
	pid, err := c.projectID(ctx, *project)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + pid
	key := chooseKey(*keyFlag)
	do := func(method, path string, body any, revision int64) error {
		data, code, err := c.do(ctx, method, base+path, body, revision, key)
		return output(data, code, err)
	}
	validUUID := func(v, label string) (string, error) {
		id, err := uuid.Parse(v)
		if err != nil || id == uuid.Nil {
			return "", fmt.Errorf("valid -%s UUID required", label)
		}
		return id.String(), nil
	}
	switch cmd {
	case "report":
		return runTestReport(ctx, c, testReportInput{
			ProjectID: pid, CaseVersionID: *caseVersion, WorkItemID: *workItem, Title: *title,
			Status: *status, Note: *note, Source: *reportSource,
			Environment: *environment, BuildRef: *buildRef,
			RequestKey: *keyFlag,
		})
	case "cases":
		return do("GET", "/test-cases", nil, 0)
	case "runs":
		return do("GET", "/test-runs", nil, 0)
	case "case-create":
		if strings.TrimSpace(*title) == "" {
			return errors.New("-title required")
		}
		return do("POST", "/test-cases", map[string]any{"specification": map[string]any{
			"title": strings.TrimSpace(*title), "preconditions": "", "steps": []any{}, "expected_result": "",
		}}, 0)
	case "case-show", "case-versions", "publish":
		id, err := validUUID(*caseID, "case")
		if err != nil {
			return err
		}
		path := "/test-cases/" + id
		if cmd == "case-show" {
			return do("GET", path, nil, 0)
		}
		if cmd == "case-versions" {
			return do("GET", path+"/versions", nil, 0)
		}
		if *expected < 1 {
			return errors.New("-version expected draft revision required")
		}
		return do("POST", path+"/versions", map[string]any{"expected_revision": *expected}, 0)
	case "run-create":
		if strings.TrimSpace(*title) == "" {
			return errors.New("-title run name required")
		}
		list := strings.Split(*versionIDs, ",")
		if *versionIDs == "" || len(list) > 100 {
			return errors.New("-versions requires 1..100 comma-separated CaseVersion UUIDs")
		}
		ids := make([]string, 0, len(list))
		for _, v := range list {
			id, err := validUUID(strings.TrimSpace(v), "versions")
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return do("POST", "/test-runs", map[string]any{
			"name": strings.TrimSpace(*title), "source_system": *source, "environment": *environment, "build_ref": *buildRef,
			"case_version_ids": ids,
		}, 0)
	case "attempts", "record":
		id, err := validUUID(*itemID, "item")
		if err != nil {
			return err
		}
		path := "/test-run-items/" + id + "/attempts"
		if cmd == "attempts" {
			return do("GET", path, nil, 0)
		}
		if *status == "" {
			return errors.New("-status required")
		}
		return do("POST", path, map[string]any{"status": *status, "note": *note, "source_kind": "external"}, 0)
	case "defect":
		id, err := validUUID(*attemptID, "attempt")
		if err != nil {
			return err
		}
		if *bugID != "" && *title != "" {
			return errors.New("provide either -bug or -title")
		}
		if *bugID != "" {
			existing, err := validUUID(*bugID, "bug")
			if err != nil {
				return err
			}
			return do("POST", "/test-attempts/"+id+"/defects", map[string]any{"bug_id": existing}, 0)
		}
		if strings.TrimSpace(*title) == "" {
			return errors.New("new Bug requires -title")
		}
		return do("POST", "/test-attempts/"+id+"/defects", map[string]any{"title": strings.TrimSpace(*title), "severity": "major"}, 0)
	case "import":
		id, err := validUUID(*runID, "run")
		if err != nil {
			return err
		}
		if *filename == "" {
			return errors.New("-file JUnit XML path required")
		}
		data, err := os.ReadFile(*filename)
		if err != nil {
			return errors.New("unable to read local XML report")
		}
		if len(data) < 1 || len(data) > 1<<20 {
			return errors.New("JUnit XML requires 1..1048576 bytes")
		}
		basename := filepath.Base(*filename)
		if !strings.HasSuffix(strings.ToLower(basename), ".xml") {
			return errors.New("JUnit report filename must end in .xml")
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.base+base+"/test-runs/"+id+"/junit-imports", bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.bearer)
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("X-Report-Filename", basename)
		res, err := c.http.Do(req)
		if err != nil {
			return errors.New("JUnit upload failed")
		}
		defer res.Body.Close()
		response, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		if err != nil {
			return err
		}
		if res.StatusCode >= 400 {
			var obj apiResponse
			_ = json.Unmarshal(response, &obj)
			return output(nil, res.StatusCode, fmt.Errorf("%s: JUnit import was rejected", obj.Error.Code))
		}
		return output(json.RawMessage(response), res.StatusCode, nil)
	default:
		return errors.New("unknown test subcommand")
	}
}
