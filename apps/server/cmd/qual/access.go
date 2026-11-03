package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// runAccess uses the same protected Project API and scoped PAT as PI MCP.
// Credential Scope alone does not grant a permission: a Project override or
// role default must also allow it. This CLI never writes SQL or runs Agents.
func runAccess(ctx context.Context, c *client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual access me|list|set -project KEY [flags]")
	}
	action := args[0]
	fs := flag.NewFlagSet("access "+action, flag.ContinueOnError)
	project := fs.String("project", "", "project UUID or key")
	target := fs.String("principal", "", "target Principal UUID (set)")
	permission := fs.String("permission", "", "permission name, e.g. work:review or audit:read")
	effect := fs.String("effect", "inherit", "allow|deny|inherit")
	expected := fs.Int64("expected-version", -1, "current override version (0 for new row)")
	reason := fs.String("reason", "", "auditable reason (3..500 chars)")
	requestKey := fs.String("request-key", "", "stable idempotency key (optional; generated when absent)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("-project is required")
	}
	pid, err := c.projectID(ctx, *project)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + pid + "/access"
	switch action {
	case "me":
		raw, status, err := c.do(ctx, "GET", base+"/me", nil, 0, "")
		return output(raw, status, err)
	case "list":
		raw, status, err := c.do(ctx, "GET", base, nil, 0, "")
		return output(raw, status, err)
	case "set":
		targetID, err := uuid.Parse(*target)
		if err != nil || targetID == uuid.Nil {
			return errors.New("valid -principal UUID required")
		}
		if *permission == "" || (*effect != "allow" && *effect != "deny" && *effect != "inherit") ||
			*expected < 0 || len(*reason) < 3 || len(*reason) > 500 {
			return errors.New("-permission -effect -expected-version -reason required")
		}
		key := chooseKey(*requestKey)
		path := fmt.Sprintf("%s/%s/permissions/%s", base, targetID, url.PathEscape(*permission))
		raw, status, err := c.do(ctx, "PUT", path, map[string]any{
			"effect": *effect, "expected_version": *expected, "reason": *reason,
		}, 0, key)
		return output(raw, status, err)
	default:
		return errors.New("unknown access command: use me|list|set")
	}
}
