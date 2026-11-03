package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// Role history never creates an alternate Principal. The server authenticates
// the real caller and determines whether the review is independent.
func runRole(ctx context.Context, c *client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual role list|record -project PROJECT -item UUID [flags]")
	}
	command := args[0]
	fs := flag.NewFlagSet("role "+command, flag.ContinueOnError)
	project := fs.String("project", "", "project UUID/key")
	item := fs.String("item", "", "WorkItem UUID")
	capacity := fs.String("capacity", "", "executor|tester|reviewer|auditor")
	outcome := fs.String("outcome", "observed", "observed|passed|failed|needs_work")
	summary := fs.String("summary", "", "brief factual observation")
	refKind := fs.String("reference-kind", "none", "none|git_commit|test_run|artifact")
	ref := fs.String("reference", "", "Git SHA or Qual object UUID")
	version := fs.Int64("version", 0, "current WorkItem version; required unless -latest-version is used")
	latest := fs.Bool("latest-version", false, "fetch WorkItem version once before recording (no automatic 409 retry)")
	idem := fs.String("request-key", "", "stable idempotency key")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("-project is required")
	}
	workID, err := uuid.Parse(*item)
	if err != nil || workID == uuid.Nil {
		return errors.New("-item must be a WorkItem UUID")
	}
	pid, err := c.projectID(ctx, *project)
	if err != nil {
		return err
	}
	itemPath := "/api/v1/projects/" + pid + "/work-items/" + url.PathEscape(workID.String())
	path := itemPath + "/role-entries"
	switch command {
	case "list":
		raw, status, err := c.do(ctx, "GET", path, nil, 0, "")
		return output(raw, status, err)
	case "record":
		if *capacity == "" || *summary == "" || (*version < 1 && !*latest) || (*latest && *version != 0) {
			return errors.New("-capacity -summary plus exactly one of -version or -latest-version are required")
		}
		expected := *version
		if *latest {
			// Convenience only: read once, then submit CAS once. No blind retry
			// after concurrent updates or a previously rejected write.
			raw, status, err := c.do(ctx, "GET", itemPath, nil, 0, "")
			if err != nil {
				return err
			}
			if status != 200 {
				return fmt.Errorf("cannot read WorkItem version (HTTP %d)", status)
			}
			var current struct {
				ID        string `json:"id"`
				ProjectID string `json:"project_id"`
				Version   int64  `json:"version"`
			}
			if err := json.Unmarshal(raw, &current); err != nil || current.ID != workID.String() ||
				current.ProjectID != pid || current.Version < 1 {
				return errors.New("WorkItem version lookup returned missing or mismatched data")
			}
			expected = current.Version
		}
		request := map[string]any{
			"capacity": *capacity, "outcome": *outcome, "summary": *summary,
			"reference_kind": *refKind, "reference_value": *ref, "expected_version": expected,
		}
		raw, status, err := c.do(ctx, "POST", path, request, 0, chooseKey(*idem))
		return output(raw, status, err)
	default:
		return errors.New("unknown role command: use list|record")
	}
}
