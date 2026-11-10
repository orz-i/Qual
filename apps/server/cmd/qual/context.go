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
	"attempts": true, "historical_attributions": true, "artifacts": true, "references": true,
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
	// An unclaimed Candidate may read the ordinary Task contract (work:read),
	// but is NOT allowed into private WorkContext. Fall back to exactly that
	// separately authorized public WorkItem; do not query any private sections.
	if status == 403 {
		return runBasicTaskResume(ctx, c, path)
	}
	if err != nil {
		return &exitError{code: 3, cause: fmt.Errorf("resume overview: %w", err)}
	}
	if status != 200 {
		return errors.New("resume overview was not authorized")
	}
	var overview struct {
		SchemaVersion     int             `json:"schema_version"`
		ContextRevision   int64           `json:"context_revision"`
		GeneratedAt       json.RawMessage `json:"generated_at"`
		WorkItem          json.RawMessage `json:"work_item"`
		LatestReview      json.RawMessage `json:"latest_review"`
		Acceptance        json.RawMessage `json:"acceptance"`
		Capabilities      json.RawMessage `json:"capabilities"`
		AvailableSections []string        `json:"available_sections"`
	}
	if err := json.Unmarshal(raw, &overview); err != nil {
		return errors.New("invalid WorkContext overview")
	}
	if overview.SchemaVersion != 2 || overview.ContextRevision < 1 {
		return errors.New("unsupported WorkContext version")
	}
	// Older, still deployed v2 servers project only a short Summary. Obtain
	// the missing Task-native description/acceptance from the already protected
	// WorkItem GET and ensure it has the SAME version. No private data is read
	// through this compatibility path, and no second Task fact store exists.
	var summary map[string]json.RawMessage
	if json.Unmarshal(overview.WorkItem, &summary) != nil || summary == nil {
		return errors.New("invalid WorkContext WorkItem summary")
	}
	_, hasDescription := summary["description"]
	_, hasCriteria := summary["acceptance_criteria"]
	if !hasDescription || !hasCriteria {
		if err := hydrateResumeTask(ctx, c, path, overview.ContextRevision, summary); err != nil {
			return err
		}
		overview.WorkItem, err = json.Marshal(summary)
		if err != nil {
			return err
		}
	}
	sections := map[string]any{}
	omitted := map[string]string{}
	// Only a server advertising the Goose21 provenance section is queried;
	// preserve read-only resume on older Context v2 installations.
	sectionNames := []string{"dependencies", "comments", "reviews", "coverage", "attempts", "artifacts", "references"}
	for _, offered := range overview.AvailableSections {
		if offered == "historical_attributions" {
			sectionNames = append(sectionNames, offered)
			break
		}
	}
	for _, section := range sectionNames {
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
		"generated_at": overview.GeneratedAt,
		"consistency":  "best_effort", "work_item": overview.WorkItem,
		"latest_review": overview.LatestReview, "acceptance": overview.Acceptance,
		"capabilities": overview.Capabilities,
		"sections":     sections, "omitted_sections": omitted,
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
