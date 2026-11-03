package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// artifactUpload is shared by evidence upload (an existing file) and
// evidence packet (deterministic metadata composed by the external CLI).
// Every stage uses the same authorized, idempotent API as before.
func (c *client) artifactUpload(ctx context.Context, base, targetKind string, targetID uuid.UUID,
	filename, mediaType string, data []byte, key string) (json.RawMessage, error) {
	if targetID == uuid.Nil || len(data) == 0 || len(data) > 10*1024*1024 ||
		len(key) < 8 || len(key) > 115 || filename == "" || strings.ContainsAny(filename, "/\\\r\n") ||
		len(filename) > 160 || strings.TrimSpace(filename) != filename ||
		mediaType == "" || strings.ContainsAny(mediaType, "\r\n;") {
		return nil, errors.New("invalid artifact source, filename or stable request key")
	}
	const prefix = "/api/v1/projects/"
	projectID, projectErr := uuid.Parse(strings.TrimPrefix(base, prefix))
	if !strings.HasPrefix(base, prefix) || projectErr != nil || projectID == uuid.Nil ||
		base != prefix+projectID.String() {
		return nil, errors.New("invalid scoped Project path for Artifact upload")
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	rawBegin, _, err := c.do(ctx, "POST", base+"/artifacts", map[string]any{
		"target_kind": targetKind,
		"target_id":   targetID,
		"filename":    filename,
		"media_type":  mediaType,
		"size_bytes":  len(data),
		"sha256":      hash,
	}, 0, key+"-begin")
	if err != nil {
		return nil, fmt.Errorf("artifact begin failed (no Ready Artifact confirmed): %w", err)
	}
	var start struct {
		ID         uuid.UUID `json:"id"`
		ProjectID  uuid.UUID `json:"project_id"`
		TargetKind string    `json:"target_kind"`
		TargetID   uuid.UUID `json:"target_id"`
		SHA256     string    `json:"sha256"`
	}
	if err := json.Unmarshal(rawBegin, &start); err != nil || start.ID == uuid.Nil ||
		start.ProjectID != projectID || start.TargetKind != targetKind || start.TargetID != targetID ||
		!strings.EqualFold(start.SHA256, hash) {
		return nil, errors.New("artifact begin returned invalid target or checksum; no upload attempted")
	}
	path := base + "/artifacts/" + start.ID.String()
	_, _, _, err = c.raw(ctx, "PUT", path+"/content", key+"-bytes", "application/octet-stream", data)
	if err != nil {
		return nil, fmt.Errorf("artifact PARTIAL: %s started, content not confirmed (%v); retry same key and bytes",
			start.ID, err)
	}
	rawReady, _, err := c.do(ctx, "POST", path+"/complete", nil, 0, key+"-complete")
	if err != nil {
		return nil, fmt.Errorf("artifact PARTIAL: %s uploaded, Ready not confirmed (%v); retry same key and bytes",
			start.ID, err)
	}
	var ready struct {
		ID         uuid.UUID `json:"id"`
		ProjectID  uuid.UUID `json:"project_id"`
		TargetKind string    `json:"target_kind"`
		TargetID   uuid.UUID `json:"target_id"`
		SizeBytes  int64     `json:"size_bytes"`
		SHA256     string    `json:"sha256"`
		State      string    `json:"state"`
	}
	if err := json.Unmarshal(rawReady, &ready); err != nil || ready.ID != start.ID ||
		ready.ProjectID != projectID || ready.TargetKind != targetKind || ready.TargetID != targetID ||
		ready.SizeBytes != int64(len(data)) ||
		!strings.EqualFold(ready.SHA256, hash) || ready.State != "ready" {
		return nil, fmt.Errorf("artifact PARTIAL: %s has an unverified Ready response; retry same key and bytes", start.ID)
	}
	return rawReady, nil
}
