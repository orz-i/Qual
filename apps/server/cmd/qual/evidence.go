package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func (c *client) raw(ctx context.Context, method, path, idem, contentType string, body []byte) (int, []byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, errors.New("Qual request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 34*1024*1024))
	if err != nil {
		return resp.StatusCode, nil, nil, err
	}
	if resp.StatusCode >= 400 {
		var e apiResponse
		_ = json.Unmarshal(data, &e)
		return resp.StatusCode, nil, nil, fmt.Errorf("%s: %s (HTTP %d)", e.Error.Code, e.Error.Message, resp.StatusCode)
	}
	return resp.StatusCode, data, resp.Header, nil
}
func validID(value, flagName string) (string, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return "", fmt.Errorf("valid -%s UUID required", flagName)
	}
	return id.String(), nil
}
func saveBytes(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("output file already exists or cannot be created")
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	return f.Sync()
}
func checkChecksum(body []byte, header http.Header) error {
	expected := header.Get("X-Content-SHA256")
	sum := sha256.Sum256(body)
	if len(expected) != 64 || !strings.EqualFold(expected, hex.EncodeToString(sum[:])) {
		return errors.New("download did not match Qual SHA-256 header")
	}
	return nil
}
func runEvidence(ctx context.Context, c *client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: qual evidence packet|upload|list|download|refs|ref-create; qual archive export|list|download; qual webhook subscribe|list|disable|deliveries")
	}
	group, cmd := args[0], args[1]
	fs := flag.NewFlagSet(group+" "+cmd, flag.ContinueOnError)
	project := fs.String("project", "", "project key or UUID")
	targetKind := fs.String("target-kind", "", "work_item|test_case|test_run|test_attempt")
	target := fs.String("target", "", "target UUID")
	id := fs.String("id", "", "artifact/export/subscription UUID")
	file := fs.String("file", "", "local upload source path")
	workItem := fs.String("work-item", "", "real WorkItem UUID for evidence packet")
	testRun := fs.String("test-run", "", "existing TestRun UUID for evidence packet")
	gitCommit := fs.String("git-commit", "", "full local Git commit SHA, for evidence packet")
	gitRepo := fs.String("git-repo", ".", "local Git repository path for read-only commit verification")
	out := fs.String("out", "", "new local download path (will not overwrite)")
	provider := fs.String("provider", "", "external provider")
	kind := fs.String("kind", "", "external reference kind")
	externalID := fs.String("external-id", "", "external provider ID")
	link := fs.String("url", "", "HTTPS external reference")
	endpoint := fs.String("endpoint", "", "allowlisted HTTPS webhook endpoint")
	events := fs.String("events", "", "comma-separated event types")
	keyFlag := fs.String("request-key", "", "optional idempotency key")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("-project is required")
	}
	pid, err := c.projectID(ctx, *project)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + pid
	key := chooseKey(*keyFlag)
	fetch := func(method, path string, body any) error {
		raw, status, err := c.do(ctx, method, base+path, body, 0, key)
		return output(raw, status, err)
	}
	switch group {
	case "evidence":
		switch cmd {
		case "packet":
			return runWorkEvidencePacket(ctx, c, workEvidencePacketInput{
				ProjectID: pid, WorkItemID: *workItem, TestRunID: *testRun,
				GitCommit: *gitCommit, GitRepo: *gitRepo, RequestKey: *keyFlag,
			})
		case "upload":
			targetID, err := validID(*target, "target")
			if err != nil {
				return err
			}
			if *targetKind == "" || *file == "" {
				return errors.New("-target-kind and -file are required")
			}
			data, err := os.ReadFile(*file)
			if err != nil {
				return errors.New("cannot read artifact file")
			}
			if len(data) < 1 || len(data) > 10*1024*1024 {
				return errors.New("artifact size must be 1..10485760 bytes")
			}
			if len(key) > 115 {
				return errors.New("request key too long for multi-stage upload")
			}
			filename := filepath.Base(*file)
			contentType := mime.TypeByExtension(filepath.Ext(filename))
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			if position := strings.Index(contentType, ";"); position >= 0 {
				contentType = contentType[:position]
			}
			targetUUID, _ := uuid.Parse(targetID) // already validated by validID
			result, err := c.artifactUpload(ctx, base, *targetKind, targetUUID,
				filename, contentType, data, key)
			return output(result, 200, err)
		case "list", "refs":
			id, err := validID(*target, "target")
			if err != nil {
				return err
			}
			if *targetKind == "" {
				return errors.New("-target-kind required")
			}
			path := "/artifacts"
			if cmd == "refs" {
				path = "/external-references"
			}
			q := url.Values{"target_kind": {*targetKind}, "target_id": {id}}
			return fetch("GET", path+"?"+q.Encode(), nil)
		case "download":
			id, err := validID(*id, "id")
			if err != nil {
				return err
			}
			if *out == "" {
				return errors.New("-out required")
			}
			_, data, header, err := c.raw(ctx, "GET", base+"/artifacts/"+id+"/content", "", "", nil)
			if err != nil {
				return err
			}
			if err := checkChecksum(data, header); err != nil {
				return err
			}
			if err := saveBytes(*out, data); err != nil {
				return err
			}
			return printJSON(json.RawMessage(fmt.Sprintf(`{"sha256":%q,"size_bytes":%d,"saved_to":%q}`, header.Get("X-Content-SHA256"), len(data), *out)))
		case "ref-create":
			tid, err := validID(*target, "target")
			if err != nil {
				return err
			}
			if *targetKind == "" || *provider == "" || *kind == "" || *externalID == "" || *link == "" {
				return errors.New("-target-kind -provider -kind -external-id -url required")
			}
			return fetch("POST", "/external-references", map[string]any{
				"target_kind": *targetKind, "target_id": tid, "provider": *provider,
				"kind": *kind, "external_id": *externalID, "url": *link,
			})
		default:
			return errors.New("unknown qual evidence subcommand")
		}
	case "archive":
		switch cmd {
		case "export":
			return fetch("POST", "/exports", nil)
		case "list":
			return fetch("GET", "/exports", nil)
		case "download":
			eid, err := validID(*id, "id")
			if err != nil {
				return err
			}
			if *out == "" {
				return errors.New("-out required")
			}
			_, data, header, err := c.raw(ctx, "GET", base+"/exports/"+eid+"/content", "", "", nil)
			if err != nil {
				return err
			}
			if err := checkChecksum(data, header); err != nil {
				return err
			}
			if err := saveBytes(*out, data); err != nil {
				return err
			}
			return printJSON(json.RawMessage(fmt.Sprintf(`{"sha256":%q,"size_bytes":%d,"saved_to":%q}`, header.Get("X-Content-SHA256"), len(data), *out)))
		default:
			return errors.New("unknown qual archive subcommand")
		}
	case "webhook":
		switch cmd {
		case "list":
			return fetch("GET", "/webhooks", nil)
		case "deliveries":
			return fetch("GET", "/webhook-deliveries", nil)
		case "subscribe":
			if *endpoint == "" || *events == "" {
				return errors.New("-endpoint and -events required; secret shown only once")
			}
			return fetch("POST", "/webhooks", map[string]any{"endpoint": *endpoint, "event_types": strings.Split(*events, ",")})
		case "disable":
			sid, err := validID(*id, "id")
			if err != nil {
				return err
			}
			status, _, _, err := c.raw(ctx, "POST", base+"/webhooks/"+sid+"/disable", key, "", nil)
			if err != nil {
				return err
			}
			return printJSON(json.RawMessage(fmt.Sprintf(`{"status":%q,"http":%d}`, "disabled", status)))
		default:
			return errors.New("unknown qual webhook subcommand")
		}
	default:
		return errors.New("unknown Qual command group")
	}
}
