package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

var allowedContextSections = map[string]bool{
	"overview": true, "dependencies": true, "comments": true,
	"reviews": true, "acceptance": true, "coverage": true,
	"attempts": true, "artifacts": true, "references": true,
}

func contextPath(path, section string, limit int, cursor string) (string, error) {
	if !allowedContextSections[section] || limit < 1 || limit > 100 ||
		len(cursor) > 512 || cursor != "" && (section == "overview" || section == "acceptance") {
		return "", errors.New("context requires valid -section, -limit 1..100 and compatible -cursor")
	}
	q := url.Values{"section": {section}, "limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return path + "/context?" + q.Encode(), nil
}

func runContext(ctx context.Context, c *client, path, section string, limit int, cursor string) error {
	q, err := contextPath(path, section, limit, cursor)
	if err != nil {
		return err
	}
	body, code, err := c.do(ctx, "GET", q, nil, 0, "")
	return output(body, code, err)
}

// resume is a local read-only composition of individually authorized bounded
// REST pages. It never stores a session or assumes cross-domain atomicity.
func runResume(ctx context.Context, c *client, path string, limit int) error {
	if limit < 1 || limit > 10 {
		return errors.New("resume -limit must be 1..10 per section")
	}
	overviewPath, _ := contextPath(path, "overview", limit, "")
	raw, status, err := c.do(ctx, "GET", overviewPath, nil, 0, "")
	if err != nil {
		return &exitError{code: 3, cause: fmt.Errorf("resume overview: %w", err)}
	}
	if status != 200 {
		return errors.New("resume overview was not authorized")
	}
	var overview struct {
		SchemaVersion   int             `json:"schema_version"`
		ContextRevision int64           `json:"context_revision"`
		WorkItem        json.RawMessage `json:"work_item"`
		LatestReview    json.RawMessage `json:"latest_review"`
		Acceptance      json.RawMessage `json:"acceptance"`
	}
	if err := json.Unmarshal(raw, &overview); err != nil {
		return errors.New("invalid WorkContext overview")
	}
	if overview.SchemaVersion != 2 || overview.ContextRevision < 1 {
		return errors.New("unsupported WorkContext version")
	}
	sections := map[string]any{}
	omitted := map[string]string{}
	for _, section := range []string{"dependencies", "reviews", "coverage", "attempts", "artifacts", "references"} {
		q, _ := contextPath(path, section, limit, "")
		b, code, err := c.do(ctx, "GET", q, nil, 0, "")
		if code == 403 {
			omitted[section] = "not_authorized"
			continue
		}
		if code == 503 {
			omitted[section] = "bounded_source_read_unavailable"
			continue
		}
		if err != nil {
			return fmt.Errorf("resume section %s failed: %w", section, err)
		}
		var page struct {
			ContextRevision int64             `json:"context_revision"`
			Items           []json.RawMessage `json:"items"`
			HasMore         bool              `json:"has_more"`
			PagingLimited   bool              `json:"paging_limited"`
			NextCursor      string            `json:"next_cursor"`
		}
		if json.Unmarshal(b, &page) != nil || page.ContextRevision != overview.ContextRevision {
			return errors.New("CONTEXT_CHANGED: WorkItem version changed during resume; retry")
		}
		sections[section] = map[string]any{"items": page.Items, "has_more": page.HasMore,
			"paging_limited": page.PagingLimited, "next_cursor": page.NextCursor}
	}
	// Thread associations have their own authenticated project read boundary.
	// They are not in WorkContext v2; fetch only five project-scoped links.
	b, code, err := c.do(ctx, "GET", path+"/threads?limit="+strconv.Itoa(limit), nil, 0, "")
	if code == 403 {
		omitted["threads"] = "not_authorized"
	} else if err != nil {
		return fmt.Errorf("resume Thread links: %w", err)
	} else {
		var links map[string]any
		if json.Unmarshal(b, &links) != nil {
			return errors.New("invalid Thread response")
		}
		sections["threads"] = links
	}
	out := map[string]any{
		"schema_version": 2, "context_revision": overview.ContextRevision,
		"consistency": "best_effort", "work_item": overview.WorkItem,
		"latest_review": overview.LatestReview, "acceptance": overview.Acceptance,
		"sections": sections, "omitted_sections": omitted,
		"warnings": []string{"Read fresh WorkItem/version and permissions before any write.",
			"Only persisted source facts are shown; review needs_work/failed histories and explicit Done Gate.",
			"No cross-domain snapshot guarantee; use section cursors for more records.",
			"Qual never resumes or starts an external Agent process."},
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return printJSON(encoded)
}
