// Package domain defines evidence invariants, independent of S3/SQL/HTTP.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrValidation  = errors.New("invalid evidence command")
	ErrNotFound    = errors.New("evidence resource not found")
	ErrConflict    = errors.New("evidence state conflict")
	ErrIntegrity   = errors.New("evidence hash or length mismatch")
	ErrUnavailable = errors.New("evidence object store unavailable")
	ErrLimit       = errors.New("export limit exceeded")
)

const MaxArtifactSize = 10 * 1024 * 1024

type TargetKind string

const (
	WorkItem    TargetKind = "work_item"
	TestCase    TargetKind = "test_case"
	TestRun     TargetKind = "test_run"
	TestAttempt TargetKind = "test_attempt"
)

func ValidTarget(k TargetKind) bool {
	return k == WorkItem || k == TestCase || k == TestRun || k == TestAttempt
}

var simpleName = regexp.MustCompile(`^[^\\/\x00-\x1F\x7F]{1,160}$`)

func ValidFilename(s string) bool {
	return simpleName.MatchString(s) && s == path.Base(s) && strings.TrimSpace(s) == s && s != "." && s != ".."
}

type BeginInput struct {
	TargetKind TargetKind `json:"target_kind"`
	TargetID   uuid.UUID  `json:"target_id"`
	Filename   string     `json:"filename"`
	MediaType  string     `json:"media_type"`
	SizeBytes  int64      `json:"size_bytes"`
	SHA256     string     `json:"sha256"`
}

func (in BeginInput) Validate() error {
	if !ValidTarget(in.TargetKind) || in.TargetID == uuid.Nil || !ValidFilename(in.Filename) ||
		in.SizeBytes <= 0 || in.SizeBytes > MaxArtifactSize || len(in.MediaType) < 3 || len(in.MediaType) > 120 ||
		strings.ContainsAny(in.MediaType, "\r\n;") || len(in.SHA256) != 64 {
		return ErrValidation
	}
	if _, err := hex.DecodeString(in.SHA256); err != nil {
		return ErrValidation
	}
	return nil
}

type Artifact struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	TargetKind TargetKind `json:"target_kind"`
	TargetID   uuid.UUID  `json:"target_id"`
	Filename   string     `json:"filename"`
	MediaType  string     `json:"media_type"`
	SizeBytes  int64      `json:"size_bytes"`
	SHA256     string     `json:"sha256"`
	State      string     `json:"state"`
	CreatedBy  uuid.UUID  `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	UploadedAt *time.Time `json:"uploaded_at"`
	ReadyAt    *time.Time `json:"ready_at"`
}

func Verify(data []byte, size int64, sum string) error {
	digest := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(digest[:]) != sum {
		return ErrIntegrity
	}
	return nil
}

type ReferenceInput struct {
	TargetKind TargetKind `json:"target_kind"`
	TargetID   uuid.UUID  `json:"target_id"`
	Provider   string     `json:"provider"`
	Kind       string     `json:"kind"`
	ExternalID string     `json:"external_id"`
	URL        string     `json:"url"`
}

func (in ReferenceInput) Validate() error {
	if !ValidTarget(in.TargetKind) || in.TargetID == uuid.Nil || len(strings.TrimSpace(in.Provider)) < 1 || len(in.Provider) > 60 ||
		len(strings.TrimSpace(in.Kind)) < 1 || len(in.Kind) > 60 || len(strings.TrimSpace(in.ExternalID)) < 1 || len(in.ExternalID) > 240 || len(in.URL) > 2048 {
		return ErrValidation
	}
	u, err := url.Parse(in.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" && len(u.RawQuery) > 1000 {
		return ErrValidation
	}
	return nil
}

type Reference struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	ReferenceInput
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}
type Export struct {
	ID         uuid.UUID `json:"id"`
	ProjectID  uuid.UUID `json:"project_id"`
	SHA256     string    `json:"sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	SnapshotAt time.Time `json:"snapshot_at"`
	CreatedAt  time.Time `json:"created_at"`
	Manifest   any       `json:"manifest"`
}
type FileManifest struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Records   int    `json:"records,omitempty"`
}
type Manifest struct {
	ArchiveSchemaVersion int            `json:"archive_schema_version"`
	ProjectID            uuid.UUID      `json:"project_id"`
	SnapshotAt           time.Time      `json:"snapshot_at"`
	Files                []FileManifest `json:"files"`
}
