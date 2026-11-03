package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type client struct {
	base   string
	bearer string
	http   *http.Client
}

func newClient() (*client, error) {
	base := strings.TrimSuffix(os.Getenv("QUAL_API_URL"), "/")
	if base == "" {
		base = "http://127.0.0.1:18080"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid QUAL_API_URL")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("refusing to send Bearer token over non-loopback HTTP; configure HTTPS")
		}
	} else if parsed.Scheme != "https" {
		return nil, errors.New("QUAL_API_URL must be HTTP loopback or HTTPS")
	}
	token := os.Getenv("QUAL_TOKEN")
	if token == "" {
		return nil, errors.New("QUAL_TOKEN must contain a scoped external Agent/Human PAT (never a command argument)")
	}
	return &client{base: base, bearer: token, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type apiResponse struct {
	Error apiError `json:"error"`
}

func (c *client) do(ctx context.Context, method, path string, body any, version int64, key string) (json.RawMessage, int, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, buf)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if version > 0 {
		req.Header.Set("If-Match", fmt.Sprintf("%q", fmt.Sprintf("%d", version)))
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024))
	if err != nil {
		return nil, res.StatusCode, err
	}
	if res.StatusCode >= 400 {
		var response apiResponse
		_ = json.Unmarshal(payload, &response)
		if response.Error.Code == "" {
			response.Error.Code = "HTTP_ERROR"
		}
		return nil, res.StatusCode, fmt.Errorf("%s: %s (HTTP %d)", response.Error.Code, response.Error.Message, res.StatusCode)
	}
	return payload, res.StatusCode, nil
}

type project struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (c *client) projectID(ctx context.Context, key string) (string, error) {
	if id, err := uuid.Parse(key); err == nil {
		return id.String(), nil
	}
	body, _, err := c.do(ctx, "GET", "/api/v1/projects", nil, 0, "")
	if err != nil {
		return "", err
	}
	var result struct {
		Items []project `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	for _, p := range result.Items {
		if p.Key == key {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found or not accessible", key)
}

func printJSON(body json.RawMessage) error {
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Never print credentials, request payloads or headers.
		fmt.Fprintln(os.Stderr, "qual:", err)
		var resultErr *exitError
		if errors.As(err, &resultErr) {
			os.Exit(resultErr.code)
		}
		os.Exit(1)
	}
}

type exitError struct {
	code  int
	cause error
}

func (e *exitError) Error() string { return e.cause.Error() }
func (e *exitError) Unwrap() error { return e.cause }

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: qual projects|items|reviews|show|context|resume|create|update|claim|transition|comment|activity|acceptance|complete|review-policy|review-policy-set|work-review-policy|work-review-policy-set|test|evidence|archive|webhook|access|role [flags]")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if args[0] == "role" {
		return runRole(ctx, c, args[1:])
	}
	if args[0] == "access" {
		return runAccess(ctx, c, args[1:])
	}
	if args[0] == "evidence" || args[0] == "archive" || args[0] == "webhook" {
		return runEvidence(ctx, c, args)
	}
	if args[0] == "test" {
		return runTesting(ctx, c, args[1:])
	}
	if args[0] == "projects" {
		data, status, err := c.do(ctx, "GET", "/api/v1/projects", nil, 0, "")
		return output(data, status, err)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project UUID or project key")
	itemFlag := fs.String("item", "", "work item UUID")
	kind := fs.String("kind", "task", "task|bug")
	title := fs.String("title", "", "work item title")
	description := fs.String("description", "", "description")
	priority := fs.String("priority", "medium", "urgent|high|medium|low")
	statusFlag := fs.String("status", "", "workflow status")
	body := fs.String("body", "", "comment body")
	version := fs.Int64("version", 0, "expected work item version")
	limit := fs.Int("limit", 30, "list page size")
	offset := fs.Int("offset", 0, "review queue offset (non-snapshot pagination)")
	section := fs.String("section", "overview", "context section: overview|dependencies|comments|reviews|acceptance|coverage|attempts|artifacts|references")
	cursor := fs.String("cursor", "", "opaque WorkItem context page cursor")
	requestKey := fs.String("request-key", "", "idempotency key; generated randomly if not supplied")
	mode := fs.String("mode", "", "independent_review|owner_acceptance")
	policyVersion := fs.Int64("policy-version", -1, "expected project review policy version (0 for default)")
	allowOwner := fs.Bool("allow-owner", false, "allow explicit Owner acceptance")
	forceIndependent := fs.Bool("force-independent", true, "force independent review for high-risk work")
	reason := fs.String("reason", "", "explicit risk acceptance or policy-change reason")
	acknowledge := fs.Bool("acknowledge-self-review", false, "explicitly acknowledge non-independent Owner acceptance")
	referenceKind := fs.String("reference-kind", "", "test_run|artifact")
	referenceValue := fs.String("reference-value", "", "UUID of verified same-WorkItem evidence")
	jsonFlag := fs.Bool("json", true, "always print structured JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	_ = *jsonFlag
	if *projectFlag == "" {
		return errors.New("-project is required")
	}
	projectID, err := c.projectID(ctx, *projectFlag)
	if err != nil {
		return err
	}
	path := "/api/v1/projects/" + url.PathEscape(projectID)
	if args[0] == "review-policy" {
		data, code, err := c.do(ctx, "GET", path+"/review-policy", nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "review-policy-set" {
		if *policyVersion < 0 || *reason == "" || (*mode != "independent_review" && *mode != "owner_acceptance") {
			return errors.New("review-policy-set requires -policy-version, -mode, -reason; Human Admin required to loosen policy")
		}
		body := map[string]any{"default_mode": *mode, "owner_acceptance_allowed": *allowOwner,
			"force_independent_for_high_risk": *forceIndependent,
			"expected_version":                *policyVersion, "reason": *reason}
		data, code, err := c.do(ctx, "PUT", path+"/review-policy", body, 0, chooseKey(*requestKey))
		return output(data, code, err)
	}
	if args[0] == "reviews" {
		if *limit < 1 || *limit > 100 || *offset < 0 || *offset > 10000 {
			return errors.New("-limit must be 1..100 and -offset 0..10000")
		}
		values := url.Values{}
		values.Set("limit", fmt.Sprintf("%d", *limit))
		values.Set("offset", fmt.Sprintf("%d", *offset))
		data, code, err := c.do(ctx, "GET", path+"/review-queue?"+values.Encode(), nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "items" {
		values := url.Values{}
		values.Set("limit", fmt.Sprintf("%d", *limit))
		if *statusFlag != "" {
			values.Set("status", *statusFlag)
		}
		if *kind != "" && *kind != "task" {
			values.Set("kind", *kind)
		}
		data, code, err := c.do(ctx, "GET", path+"/work-items?"+values.Encode(), nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "activity" {
		if *limit < 1 || *limit > 100 {
			return errors.New("-limit must be 1..100")
		}
		values := url.Values{}
		values.Set("limit", fmt.Sprintf("%d", *limit))
		data, code, err := c.do(ctx, "GET", path+"/activity?"+values.Encode(), nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "create" {
		if *title == "" {
			return errors.New("-title required")
		}
		request := map[string]any{"kind": *kind, "title": *title, "description": *description, "priority": *priority}
		key := chooseKey(*requestKey)
		data, code, err := c.do(ctx, "POST", path+"/work-items", request, 0, key)
		return output(data, code, err)
	}
	if *itemFlag == "" {
		return errors.New("-item UUID required")
	}
	if _, err := uuid.Parse(*itemFlag); err != nil {
		return errors.New("invalid -item UUID; project display keys will be supported later")
	}
	path += "/work-items/" + url.PathEscape(*itemFlag)
	if args[0] == "show" {
		data, code, err := c.do(ctx, "GET", path, nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "context" {
		return runContext(ctx, c, path, *section, *limit, *cursor)
	}
	if args[0] == "resume" {
		if *cursor != "" {
			return errors.New("resume reads fresh authorized sections; -cursor is for context paging only")
		}
		resumeLimit := 5 // intentionally smaller than the default list limit
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "limit" {
				resumeLimit = *limit
			}
		})
		return runResume(ctx, c, path, resumeLimit)
	}
	if args[0] == "acceptance" {
		data, code, err := c.do(ctx, "GET", path+"/acceptances", nil, 0, "")
		return output(data, code, err)
	}
	if args[0] == "work-review-policy" {
		data, code, err := c.do(ctx, "GET", path+"/review-policy", nil, 0, "")
		return output(data, code, err)
	}
	if *version < 1 {
		return errors.New("-version N >= 1 is required for mutations")
	}
	key := chooseKey(*requestKey)
	switch args[0] {
	case "work-review-policy-set":
		if (*mode != "independent_review" && *mode != "owner_acceptance") || *policyVersion < 0 || len([]rune(*reason)) < 16 {
			return errors.New("work-review-policy-set requires -mode, -policy-version N, -version N and -reason of at least 16 chars")
		}
		body := map[string]any{"mode": *mode, "expected_version": *version, "expected_policy_version": *policyVersion, "reason": *reason}
		data, code, err := c.do(ctx, "PUT", path+"/review-policy", body, 0, key)
		return output(data, code, err)
	case "complete":
		if (*mode != "independent_review" && *mode != "owner_acceptance") ||
			(*referenceKind != "test_run" && *referenceKind != "artifact") {
			return errors.New("complete requires -mode independent_review|owner_acceptance and -reference-kind test_run|artifact")
		}
		if _, err := uuid.Parse(*referenceValue); err != nil {
			return errors.New("complete requires -reference-value UUID of verified evidence")
		}
		if *mode == "owner_acceptance" && (!*acknowledge || len([]rune(*reason)) < 16) {
			return errors.New("Owner self-acceptance requires -acknowledge-self-review and -reason (at least 16 characters)")
		}
		body := map[string]any{"acceptance_mode": *mode, "expected_version": *version,
			"reason": *reason, "acknowledge_self_review": *acknowledge,
			"reference_kind": *referenceKind, "reference_value": *referenceValue}
		data, code, err := c.do(ctx, "POST", path+"/complete", body, 0, key)
		return output(data, code, err)
	case "claim":
		data, code, err := c.do(ctx, "POST", path+"/claim", map[string]any{"expected_version": *version}, 0, key)
		return output(data, code, err)
	case "transition":
		if *statusFlag == "" {
			return errors.New("-status required")
		}
		data, code, err := c.do(ctx, "POST", path+"/transitions", map[string]any{"status": *statusFlag, "expected_version": *version}, 0, key)
		return output(data, code, err)
	case "update":
		changes := map[string]any{}
		if *title != "" {
			changes["title"] = *title
		}
		if *description != "" {
			changes["description"] = *description
		}
		if len(changes) == 0 {
			return errors.New("provide -title or -description")
		}
		data, code, err := c.do(ctx, "PATCH", path, changes, *version, key)
		return output(data, code, err)
	case "comment":
		if *body == "" {
			return errors.New("-body required")
		}
		data, code, err := c.do(ctx, "POST", path+"/comments", map[string]any{"body": *body, "expected_version": *version}, 0, key)
		return output(data, code, err)
	default:
		return errors.New("unknown subcommand")
	}
}
func chooseKey(value string) string {
	if value != "" {
		return value
	}
	return "cli-" + uuid.NewString()
}
func output(body json.RawMessage, status int, err error) error {
	if err != nil {
		code := 5
		switch status {
		case 400, 413, 422, 428:
			code = 2
		case 401, 403, 404:
			code = 3
		case 409:
			code = 4
		}
		return &exitError{code: code, cause: err}
	}
	return printJSON(body)
}
