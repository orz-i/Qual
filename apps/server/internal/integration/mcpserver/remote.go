package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

func ValidateAPIURL(raw string) (string, error) {
	if raw == "" {
		raw = "http://127.0.0.1:18080"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("invalid QUAL_API_URL")
	}
	if parsed.Scheme == "http" {
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("MCP cannot forward bearer tokens over remote plaintext HTTP")
		}
	} else if parsed.Scheme != "https" {
		return "", errors.New("QUAL_API_URL requires HTTPS or loopback HTTP")
	}
	return strings.TrimSuffix(raw, "/"), nil
}

func (b Bridge) call(ctx context.Context, method, path string, payload any, idemKey string, version int64) (map[string]any, error) {
	if b.APIURL == "" {
		return nil, errors.New("INTERNAL_ERROR")
	}
	body := []byte(nil)
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, errors.New("VALIDATION_ERROR")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, b.APIURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("INTERNAL_ERROR")
	}
	req.Header.Set("Authorization", "Bearer "+b.Bearer)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	if version > 0 {
		req.Header.Set("If-Match", fmt.Sprintf("\"%d\"", version))
	}
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("DEPENDENCY_UNAVAILABLE")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if err != nil {
		return nil, errors.New("DEPENDENCY_UNAVAILABLE")
	}
	if res.StatusCode >= 400 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &failure)
		code := failure.Error.Code
		if code == "" {
			return nil, fmt.Errorf("HTTP_ERROR_%d", res.StatusCode)
		}
		// The remote service's raw error message is intentionally not forwarded
		// to agents, avoiding backend URL, token and SQL disclosure.
		for _, r := range code {
			if r < 'A' || r > 'Z' {
				if r != '_' {
					return nil, errors.New("HTTP_ERROR")
				}
			}
		}
		return nil, errors.New(code)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("INVALID_SERVER_RESPONSE")
	}
	return result, nil
}

func (b Bridge) remote(ctx context.Context, tool string, input any) (map[string]any, error) {
	// Tool arguments are validated by the official SDK before reaching this adapter.
	var project string
	var item string
	switch v := input.(type) {
	case SearchArgs:
		project = v.ProjectID
	case ItemArgs:
		project = v.ProjectID
		item = v.ItemID
	case ContextArgs:
		project = v.ProjectID
		item = v.ItemID
	case CreateArgs:
		project = v.ProjectID
	case WriteArgs:
		project = v.ProjectID
		item = v.ItemID
	case UpdateArgs:
		project = v.ProjectID
		item = v.ItemID
	case TransitionArgs:
		project = v.ProjectID
		item = v.ItemID
	case CommentArgs:
		project = v.ProjectID
		item = v.ItemID
	case ActivityArgs:
		project = v.ProjectID
	default:
		return nil, errors.New("VALIDATION_ERROR")
	}
	p, id, err := parseIDs(project, item)
	if err != nil {
		return nil, err
	}
	base := "/api/v1/projects/" + p.String()
	workURL := base + "/work-items"
	if id != uuid.Nil {
		workURL += "/" + id.String()
	}
	switch tool {
	case "work_item_search":
		a := input.(SearchArgs)
		q := url.Values{}
		if a.Status != "" {
			q.Set("status", a.Status)
		}
		if a.Kind != "" {
			q.Set("kind", a.Kind)
		}
		if a.Limit > 0 {
			q.Set("limit", fmt.Sprintf("%d", a.Limit))
		}
		if a.Cursor != "" {
			q.Set("cursor", a.Cursor)
		}
		path := workURL
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return b.call(ctx, "GET", path, nil, "", 0)
	case "work_item_get":
		item, err := b.call(ctx, "GET", workURL, nil, "", 0)
		return map[string]any{"work_item": item}, err
	case "work_item_get_context":
		a := input.(ContextArgs)
		if a.Section != "" || a.Limit != 0 || a.Cursor != "" {
			q, err := contextSectionQuery(a.Section, a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return b.call(ctx, "GET", workURL+"/context?"+q.Encode(), nil, "", 0)
		}
		item, err := b.call(ctx, "GET", workURL, nil, "", 0)
		if err != nil {
			return nil, err
		}
		comments, err := b.call(ctx, "GET", workURL+"/comments", nil, "", 0)
		if err != nil {
			return nil, err
		}
		deps, err := b.call(ctx, "GET", workURL+"/dependencies", nil, "", 0)
		if err != nil {
			return nil, err
		}
		recent, _ := comments["items"].([]any)
		if len(recent) > 10 {
			recent = recent[:10]
		}
		return map[string]any{"schema_version": 1, "work_item": item, "recent_comments": recent, "dependencies": deps["items"], "context_revision": item["version"]}, nil
	case "work_item_create":
		a := input.(CreateArgs)
		body := map[string]any{"kind": a.Kind, "title": a.Title, "priority": a.Priority, "description": a.Description, "acceptance_criteria": a.AcceptanceCriteria}
		if a.BugDetails != nil {
			body["bug_details"] = a.BugDetails
		}
		if a.Priority == "" {
			body["priority"] = "medium"
		}
		item, err := b.call(ctx, "POST", workURL, body, a.IdempotencyKey, 0)
		return map[string]any{"work_item": item}, err
	case "work_item_claim":
		a := input.(WriteArgs)
		item, err := b.call(ctx, "POST", workURL+"/claim", map[string]any{"expected_version": a.ExpectedVersion}, a.IdempotencyKey, 0)
		return map[string]any{"work_item": item}, err
	case "work_item_update":
		a := input.(UpdateArgs)
		changes := map[string]any{}
		if a.Title != nil {
			changes["title"] = *a.Title
		}
		if a.Description != nil {
			changes["description"] = *a.Description
		}
		if a.Priority != nil {
			changes["priority"] = *a.Priority
		}
		item, err := b.call(ctx, "PATCH", workURL, changes, a.IdempotencyKey, a.ExpectedVersion)
		return map[string]any{"work_item": item}, err
	case "work_item_transition":
		a := input.(TransitionArgs)
		item, err := b.call(ctx, "POST", workURL+"/transitions", map[string]any{"status": a.Status, "expected_version": a.ExpectedVersion}, a.IdempotencyKey, 0)
		return map[string]any{"work_item": item}, err
	case "work_item_add_comment":
		a := input.(CommentArgs)
		item, err := b.call(ctx, "POST", workURL+"/comments", map[string]any{"body": a.Body, "expected_version": a.ExpectedVersion}, a.IdempotencyKey, 0)
		return map[string]any{"work_item": item}, err
	case "activity_search":
		a := input.(ActivityArgs)
		path := base + "/activity"
		if a.Limit > 0 {
			path += fmt.Sprintf("?limit=%d", a.Limit)
		}
		return b.call(ctx, "GET", path, nil, "", 0)
	default:
		return nil, errors.New("UNKNOWN_TOOL")
	}
}
