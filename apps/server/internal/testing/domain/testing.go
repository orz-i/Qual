// Package domain owns immutable test specifications and appended result facts.
// Qual records reported execution; it never runs a test or a CI job.
package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrValidation       = errors.New("invalid test data")
	ErrConflict         = errors.New("test revision conflict")
	ErrNotPublished     = errors.New("test case has no published version")
	ErrAlreadyPublished = errors.New("draft revision already published")
	ErrNotFailed        = errors.New("bug can only link to a failed attempt")
	ErrDuplicate        = errors.New("duplicate test record")
	ErrNotFound         = errors.New("test resource not found")
)

type Step struct {
	Action   string `json:"action"`
	Expected string `json:"expected"`
}
type Specification struct {
	Title          string `json:"title"`
	Preconditions  string `json:"preconditions"`
	Steps          []Step `json:"steps"`
	ExpectedResult string `json:"expected_result"`
}

func (s Specification) Validate() error {
	if strings.TrimSpace(s.Title) == "" || utf8.RuneCountInString(s.Title) > 240 ||
		utf8.RuneCountInString(s.Preconditions) > 10000 || utf8.RuneCountInString(s.ExpectedResult) > 10000 || len(s.Steps) > 100 {
		return ErrValidation
	}
	for _, step := range s.Steps {
		if strings.TrimSpace(step.Action) == "" || utf8.RuneCountInString(step.Action) > 2000 || utf8.RuneCountInString(step.Expected) > 2000 {
			return ErrValidation
		}
	}
	return nil
}
func (s Specification) Normalize() Specification {
	s.Title = strings.TrimSpace(s.Title)
	if s.Steps == nil {
		s.Steps = []Step{}
	}
	return s
}
func (s Specification) Checksum() ([32]byte, error) {
	b, err := json.Marshal(s.Normalize())
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

type Suite struct {
	ID        uuid.UUID `json:"id"`
	ProjectID uuid.UUID `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}
type TestCase struct {
	ID               uuid.UUID     `json:"id"`
	ProjectID        uuid.UUID     `json:"project_id"`
	Key              string        `json:"key"`
	SuiteID          *uuid.UUID    `json:"suite_id"`
	Draft            Specification `json:"draft"`
	Revision         int64         `json:"revision"`
	PublishedVersion int           `json:"published_version"`
	UpdatedAt        time.Time     `json:"updated_at"`
}
type TestCaseVersion struct {
	ID             uuid.UUID     `json:"id"`
	TestCaseID     uuid.UUID     `json:"test_case_id"`
	Version        int           `json:"version"`
	SourceRevision int64         `json:"source_revision"`
	Specification  Specification `json:"specification"`
	SHA256         string        `json:"sha256"`
	PublishedAt    time.Time     `json:"published_at"`
}
type RunInput struct {
	Name           string      `json:"name"`
	SourceSystem   string      `json:"source_system"`
	ExternalRunKey string      `json:"external_run_key"`
	Environment    string      `json:"environment"`
	BuildRef       string      `json:"build_ref"`
	CaseVersionIDs []uuid.UUID `json:"case_version_ids"`
}

func (r RunInput) Validate() error {
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > 240 || utf8.RuneCountInString(r.SourceSystem) > 80 ||
		utf8.RuneCountInString(r.ExternalRunKey) > 240 || utf8.RuneCountInString(r.Environment) > 2000 ||
		utf8.RuneCountInString(r.BuildRef) > 240 || len(r.CaseVersionIDs) == 0 || len(r.CaseVersionIDs) > 100 {
		return ErrValidation
	}
	seen := make(map[uuid.UUID]bool)
	for _, id := range r.CaseVersionIDs {
		if id == uuid.Nil || seen[id] {
			return ErrValidation
		}
		seen[id] = true
	}
	return nil
}

type RunItem struct {
	ID             uuid.UUID     `json:"id"`
	TestCaseID     uuid.UUID     `json:"test_case_id"`
	TestCaseKey    string        `json:"test_case_key"`
	CaseVersionID  uuid.UUID     `json:"case_version_id"`
	CaseVersion    int           `json:"case_version"`
	SnapshotSHA256 string        `json:"snapshot_sha256"`
	LatestStatus   *ResultStatus `json:"latest_status"`
	AttemptCount   int           `json:"attempt_count"`
}
type TestRun struct {
	ID             uuid.UUID `json:"id"`
	ProjectID      uuid.UUID `json:"project_id"`
	Name           string    `json:"name"`
	SourceSystem   string    `json:"source_system"`
	ExternalRunKey string    `json:"external_run_key,omitempty"`
	Environment    string    `json:"environment"`
	BuildRef       string    `json:"build_ref"`
	CreatedAt      time.Time `json:"created_at"`
	Items          []RunItem `json:"items"`
}
type ResultStatus string

const (
	Passed  ResultStatus = "passed"
	Failed  ResultStatus = "failed"
	Blocked ResultStatus = "blocked"
	Skipped ResultStatus = "skipped"
)

func ValidStatus(s ResultStatus) bool {
	return s == Passed || s == Failed || s == Blocked || s == Skipped
}

type AttemptInput struct {
	Status             ResultStatus `json:"status"`
	Note               string       `json:"note"`
	ObservedAt         *time.Time   `json:"observed_at,omitempty"`
	SourceKind         string       `json:"source_kind"`
	ExternalAttemptKey string       `json:"external_attempt_key,omitempty"`
}

func (r AttemptInput) Validate() error {
	if !ValidStatus(r.Status) || utf8.RuneCountInString(r.Note) > 20000 ||
		utf8.RuneCountInString(r.ExternalAttemptKey) > 240 {
		return ErrValidation
	}
	if r.SourceKind != "" && r.SourceKind != "manual" && r.SourceKind != "external" && r.SourceKind != "junit" {
		return ErrValidation
	}
	if r.ObservedAt != nil && (r.ObservedAt.IsZero() || r.ObservedAt.After(time.Now().Add(10*time.Minute))) {
		return ErrValidation
	}
	return nil
}

type Attempt struct {
	ID                 uuid.UUID    `json:"id"`
	RunItemID          uuid.UUID    `json:"run_item_id"`
	AttemptNo          int          `json:"attempt_no"`
	Status             ResultStatus `json:"status"`
	Note               string       `json:"note"`
	ObservedAt         time.Time    `json:"observed_at"`
	IngestedAt         time.Time    `json:"ingested_at"`
	ReportedBy         uuid.UUID    `json:"reported_by"`
	SourceKind         string       `json:"source_kind"`
	ExternalAttemptKey string       `json:"external_attempt_key,omitempty"`
}
type BugLinkInput struct {
	BugID             *uuid.UUID `json:"bug_id,omitempty"`
	Title             string     `json:"title,omitempty"`
	Severity          string     `json:"severity,omitempty"`
	ReproductionSteps string     `json:"reproduction_steps,omitempty"`
}
type BugLink struct {
	AttemptID uuid.UUID `json:"attempt_id"`
	BugID     uuid.UUID `json:"bug_id"`
	BugKey    string    `json:"bug_key"`
	Created   bool      `json:"created"`
}

// AttemptDefect is a read-only relationship to a Bug already linked to an
// immutable Failed Attempt. Bug status/title are current, link time is historical.
type AttemptDefect struct {
	AttemptID uuid.UUID `json:"attempt_id"`
	BugID     uuid.UUID `json:"bug_id"`
	BugKey    string    `json:"bug_key"`
	BugTitle  string    `json:"bug_title"`
	BugStatus string    `json:"bug_status"`
	LinkedAt  time.Time `json:"linked_at"`
	LinkedBy  uuid.UUID `json:"linked_by"`
}
type ImportSummary struct {
	ID            uuid.UUID `json:"id"`
	RunID         uuid.UUID `json:"run_id"`
	SHA256        string    `json:"sha256"`
	Filename      string    `json:"filename"`
	ImportedCount int       `json:"imported_count"`
	Passed        int       `json:"passed"`
	Failed        int       `json:"failed"`
	Skipped       int       `json:"skipped"`
}
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
