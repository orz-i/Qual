package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/google/uuid"
)

// WorkItem display keys are project-scoped. Resolve them through the existing
// authorized and bounded listing API, not a database shortcut or a new REST
// endpoint. UUID paths keep their original single-request behavior.
var taskDisplayKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,62}-[1-9][0-9]{0,9}$`)

func resolveTaskID(ctx context.Context, c *client, projectID, input string) (string, error) {
	if id, err := uuid.Parse(input); err == nil {
		if id == uuid.Nil {
			return "", errors.New("invalid WorkItem UUID")
		}
		return id.String(), nil
	}
	if !taskDisplayKey.MatchString(input) {
		return "", errors.New("-item must be a WorkItem UUID or a project display key such as QUAL-66")
	}

	// Project-local listing cursors are opaque, versioned source facts. A
	// bounded scan supports projects with more than 100 items, never relying
	// on a stale first page or confusing a title with an ID.
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		body, _, err := c.do(ctx, "GET",
			"/api/v1/projects/"+url.PathEscape(projectID)+"/work-items?"+query.Encode(), nil, 0, "")
		if err != nil {
			return "", err
		}
		var result struct {
			Items []struct {
				ID  uuid.UUID `json:"id"`
				Key string    `json:"key"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", errors.New("invalid work item listing response")
		}
		for _, item := range result.Items {
			if item.Key == input {
				if item.ID == uuid.Nil {
					return "", errors.New("invalid WorkItem ID in listing")
				}
				return item.ID.String(), nil
			}
		}
		if result.NextCursor == "" {
			return "", fmt.Errorf("work item %q not found or not accessible in this project", input)
		}
		if len(result.Items) == 0 || seen[result.NextCursor] {
			return "", errors.New("invalid WorkItem listing cursor")
		}
		seen[result.NextCursor] = true
		cursor = result.NextCursor
	}
	return "", errors.New("WorkItem display key lookup exceeded bounded scan; use the UUID")
}
