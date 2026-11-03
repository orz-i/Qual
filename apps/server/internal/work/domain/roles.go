package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Capacity describes one operation by a real Principal; it is not a new identity.
type Capacity string
type Outcome string
type ReferenceKind string

const (
	Executor Capacity = "executor"
	Tester   Capacity = "tester"
	Reviewer Capacity = "reviewer"
	Auditor  Capacity = "auditor"

	Observed  Outcome = "observed"
	Passed    Outcome = "passed"
	Failed    Outcome = "failed"
	NeedsWork Outcome = "needs_work"

	NoReference       ReferenceKind = "none"
	GitCommit         ReferenceKind = "git_commit"
	TestRunReference  ReferenceKind = "test_run"
	ArtifactReference ReferenceKind = "artifact"
)

var gitCommitRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

type RoleEntryInput struct {
	Capacity        Capacity      `json:"capacity"`
	Outcome         Outcome       `json:"outcome"`
	Summary         string        `json:"summary"`
	ReferenceKind   ReferenceKind `json:"reference_kind"`
	ReferenceValue  string        `json:"reference_value"`
	ExpectedVersion int64         `json:"expected_version"`
}

func (in RoleEntryInput) Validate() error {
	if in.Capacity != Executor && in.Capacity != Tester && in.Capacity != Reviewer && in.Capacity != Auditor {
		return ErrValidation
	}
	if in.Outcome != Observed && in.Outcome != Passed && in.Outcome != Failed && in.Outcome != NeedsWork {
		return ErrValidation
	}
	if in.ExpectedVersion < 1 || strings.TrimSpace(in.Summary) == "" || strings.TrimSpace(in.Summary) != in.Summary ||
		utf8.RuneCountInString(in.Summary) > 5000 {
		return ErrValidation
	}
	if utf8.RuneCountInString(in.ReferenceValue) > 128 {
		return ErrValidation
	}
	switch in.ReferenceKind {
	case NoReference:
		if in.ReferenceValue != "" {
			return ErrValidation
		}
	case GitCommit:
		if !gitCommitRE.MatchString(in.ReferenceValue) {
			return ErrValidation
		}
	case TestRunReference, ArtifactReference:
		if id, err := uuid.Parse(in.ReferenceValue); err != nil || id == uuid.Nil || id.String() != in.ReferenceValue {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	// A positive/negative tester or reviewer verdict must cite evidence.
	if (in.Capacity == Tester || in.Capacity == Reviewer) && (in.Outcome == Passed || in.Outcome == Failed) && in.ReferenceKind == NoReference {
		return ErrValidation
	}
	return nil
}

// RoleEntry is append-only and uses the true server-derived Actor.
// self_review is computed by the server; it cannot imply independent approval.
type RoleEntry struct {
	ID             uuid.UUID     `json:"id"`
	ProjectID      uuid.UUID     `json:"project_id"`
	WorkItemID     uuid.UUID     `json:"work_item_id"`
	ActorID        uuid.UUID     `json:"actor_id"`
	Capacity       Capacity      `json:"capacity"`
	Outcome        Outcome       `json:"outcome"`
	Summary        string        `json:"summary"`
	ReferenceKind  ReferenceKind `json:"reference_kind"`
	ReferenceValue string        `json:"reference_value"`
	WorkVersion    int64         `json:"work_version"`
	WorkStatus     Status        `json:"work_status"`
	SelfReview     bool          `json:"self_review"`
	RecordedAt     time.Time     `json:"recorded_at"`
}
