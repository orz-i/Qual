// Package domain models reports about externally executed Agent runs.
// An AgentRun status is neither a WorkItem decision nor a TestAttempt result.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrValidation  = errors.New("invalid external AgentRun report")
	ErrConflict    = errors.New("AgentRun report conflict")
	ErrNotFound    = errors.New("AgentRun not found")
	ErrRateLimited = errors.New("heartbeat reported too frequently")
)

type Status string
type ReportKind string

const (
	Queued       Status     = "queued"
	Running      Status     = "running"
	Blocked      Status     = "blocked"
	Succeeded    Status     = "succeeded"
	Failed       Status     = "failed"
	Cancelled    Status     = "cancelled"
	StatusChange ReportKind = "status"
	Heartbeat    ReportKind = "heartbeat"
)

type Registration struct {
	WorkItemID    uuid.UUID `json:"work_item_id"`
	Source        string    `json:"source"`
	ExternalRunID string    `json:"external_run_id"`
	Host          string    `json:"host"`
}

type ReportInput struct {
	SourceReportID      string     `json:"source_report_id"`
	Kind                ReportKind `json:"kind"`
	Status              Status     `json:"status,omitempty"`
	OccurredAt          time.Time  `json:"occurred_at"`
	ErrorReason         string     `json:"error_reason,omitempty"`
	EvidenceArtifactID  *uuid.UUID `json:"evidence_artifact_id,omitempty"`
	ExternalReferenceID *uuid.UUID `json:"external_reference_id,omitempty"`
}

type Run struct {
	ID                   uuid.UUID  `json:"id"`
	ProjectID            uuid.UUID  `json:"project_id"`
	WorkItemID           uuid.UUID  `json:"work_item_id"`
	Source               string     `json:"source"`
	ExternalRunID        string     `json:"external_run_id"`
	Host                 string     `json:"host"`
	ReporterPrincipalID  uuid.UUID  `json:"reporter_principal_id"`
	Status               Status     `json:"source_status"`
	Version              int64      `json:"version"`
	ReportSequence       int64      `json:"report_sequence"`
	LastStatusOccurredAt *time.Time `json:"last_status_occurred_at"`
	LastSeenAt           *time.Time `json:"last_seen_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	Observability        string     `json:"observability"`
}

type Report struct {
	ID                  uuid.UUID  `json:"id"`
	AgentRunID          uuid.UUID  `json:"agent_run_id"`
	Sequence            int64      `json:"sequence"`
	SourceReportID      string     `json:"source_report_id"`
	Kind                ReportKind `json:"kind"`
	Status              Status     `json:"status,omitempty"`
	OccurredAt          time.Time  `json:"occurred_at"`
	ReceivedAt          time.Time  `json:"received_at"`
	Applied             bool       `json:"applied"`
	RunVersion          int64      `json:"run_version"`
	ReporterPrincipalID uuid.UUID  `json:"reporter_principal_id"`
	ErrorReason         string     `json:"error_reason,omitempty"`
	EvidenceArtifactID  *uuid.UUID `json:"evidence_artifact_id,omitempty"`
	ExternalReferenceID *uuid.UUID `json:"external_reference_id,omitempty"`
}

type Receipt struct {
	Run    Run    `json:"run"`
	Report Report `json:"report"`
}

func safeID(s string, max int) bool {
	if len(s) == 0 || len(s) > max || strings.TrimSpace(s) != s || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func (r Registration) Validate() error {
	if r.WorkItemID == uuid.Nil || !safeID(r.Source, 64) || !safeID(r.ExternalRunID, 160) ||
		len(r.Host) < 1 || len(r.Host) > 200 || strings.TrimSpace(r.Host) != r.Host || !utf8.ValidString(r.Host) {
		return ErrValidation
	}
	// PostgreSQL TEXT rejects NUL, and control bytes in external Host labels
	// otherwise create HTTP 500s or forge log lines. Reject before persistence.
	for _, ch := range r.Host {
		if unicode.IsControl(ch) {
			return ErrValidation
		}
	}
	return nil
}

func ValidStatus(s Status) bool {
	switch s {
	case Queued, Running, Blocked, Succeeded, Failed, Cancelled:
		return true
	}
	return false
}
func Terminal(s Status) bool { return s == Succeeded || s == Failed || s == Cancelled }

func (r ReportInput) Validate(now time.Time) error {
	if !safeID(r.SourceReportID, 160) || r.OccurredAt.IsZero() ||
		r.OccurredAt.After(now.Add(5*time.Minute)) || !utf8.ValidString(r.ErrorReason) ||
		len(r.ErrorReason) > 500 || strings.TrimSpace(r.ErrorReason) != r.ErrorReason {
		return ErrValidation
	}
	if (r.EvidenceArtifactID != nil && *r.EvidenceArtifactID == uuid.Nil) ||
		(r.ExternalReferenceID != nil && *r.ExternalReferenceID == uuid.Nil) {
		return ErrValidation
	}
	if r.Kind == Heartbeat {
		if r.Status != "" || r.ErrorReason != "" || r.EvidenceArtifactID != nil || r.ExternalReferenceID != nil {
			return ErrValidation
		}
	} else if r.Kind != StatusChange || !ValidStatus(r.Status) {
		return ErrValidation
	}
	return nil
}

// A new status cannot revise a terminal result. An older observation may be
// archived but never projected over a more recent one.
func CanProject(previous, next Status) bool {
	if !ValidStatus(next) || Terminal(previous) {
		return false
	}
	switch previous {
	case Queued:
		return true
	case Running, Blocked:
		return next != Queued
	default:
		return false
	}
}

func Observe(status Status, lastSeen *time.Time, now time.Time, timeout time.Duration) string {
	if Terminal(status) {
		return "terminal"
	}
	if lastSeen == nil {
		return "unknown"
	}
	if timeout > 0 && now.Sub(*lastSeen) > timeout {
		return "stale"
	}
	return "current"
}
