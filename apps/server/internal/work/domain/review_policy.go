package domain

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ReviewPolicyMode is a persisted business policy, never inferred from a
// human/agent label or an arbitrary claim in the request body.
type ReviewPolicyMode string

const (
	IndependentReview ReviewPolicyMode = "independent_review"
	OwnerAcceptance   ReviewPolicyMode = "owner_acceptance"
)

type ReviewPolicySource string

const (
	ProjectReviewPolicySource ReviewPolicySource = "project"
	WorkReviewPolicySource    ReviewPolicySource = "work_item"
)

// EffectiveReviewPolicy is a server-computed snapshot after project and
// WorkItem policy resolution. The client must not supply these fields.
// This type is a pure domain foundation; the currently deployed legacy
// Transition path is NOT automatically protected until the DB gate is wired.
type EffectiveReviewPolicy struct {
	Mode                 ReviewPolicyMode
	Source               ReviewPolicySource
	Version              int64
	AllowOwnerAcceptance bool
	ForceIndependent     bool
}

// DoneDecision is an explicit user decision. AuthN actor/credential and
// self-review status must come from the server, not from this request.
type DoneDecision struct {
	Mode                  ReviewPolicyMode `json:"acceptance_mode"`
	ExpectedVersion       int64            `json:"expected_version"`
	Reason                string           `json:"reason"`
	AcknowledgeSelfReview bool             `json:"acknowledge_self_review"`
	ReferenceKind         ReferenceKind    `json:"reference_kind"`
	ReferenceValue        string           `json:"reference_value"`
}

// DoneGateActor is derived from real AuthN and project authorization. A
// delegated Owner flag is granted only by trusted server policy resolution.
// It is not a new permission: effective work:review, project Owner-acceptance
// policy and verified evidence are still checked independently.
type DoneGateActor struct {
	PrincipalID    uuid.UUID
	HasWorkReview  bool
	DelegatedOwner bool
}

// IsAssignedOwnerDelegate derives a narrowly scoped signing responsibility
// from a persisted WorkItem. When no separate accountable owner is specified,
// the already-claimed assignee (Human or Agent) can act as the responsible
// signer *only if* the project enables Owner acceptance and that principal has
// effective work:review. Neither client input nor the principal's type grants
// this delegation. An explicit accountable owner always takes precedence.
func IsAssignedOwnerDelegate(item WorkItem, principalID uuid.UUID) bool {
	return principalID != uuid.Nil && item.AccountableOwnerID == nil &&
		item.AssigneeID != nil && *item.AssigneeID == principalID
}

// DoneGateFacts must be gathered inside the same DB transaction that
// writes the eventual completion. RoleEntries come from persisted append-only
// history, not request-provided fake reviewer outcomes.
type DoneGateFacts struct {
	WorkItem            WorkItem
	Policy              EffectiveReviewPolicy
	Actor               DoneGateActor
	Decision            DoneDecision
	ReviewHistory       []RoleEntry
	LastMaterialVersion int64
	ReferenceVerified   bool // Server verified a same-project TestRun/Ready Artifact or external attestation.
}

var gitSHA40 = regexp.MustCompile("^[0-9a-f]{40}$")
var ErrDoneGate = errors.New("review acceptance gate not satisfied")

func validDecisionReference(kind ReferenceKind, value string) bool {
	switch kind {
	case GitCommit:
		return gitSHA40.MatchString(value)
	case TestRunReference, ArtifactReference:
		id, err := uuid.Parse(value)
		return err == nil && id != uuid.Nil && id.String() == value
	default:
		return false
	}
}

// LatestReviewerEntry finds the last actual reviewer outcome even if the
// latest result is needs_work/failed/self_review. Calling code must query all
// relevant reviewer entries, not choose an older passed result.
func LatestReviewerEntry(entries []RoleEntry) *RoleEntry {
	var latest *RoleEntry
	for i := range entries {
		entry := &entries[i]
		if entry.Capacity != Reviewer {
			continue
		}
		if latest == nil || entry.RecordedAt.After(latest.RecordedAt) ||
			(entry.RecordedAt.Equal(latest.RecordedAt) && strings.Compare(entry.ID.String(), latest.ID.String()) > 0) {
			latest = entry
		}
	}
	return latest
}

// EvaluateDoneGate is a pure, fail-closed predicate. It DOES NOT persist
// a decision, change WorkItem state, grant permissions or replace the existing
// legacy Transition use case. The Application/PG Done Gate implementation
// must call it under the WorkItem lock and persist everything atomically.
func EvaluateDoneGate(f DoneGateFacts) error {
	w, policy, actor, decision := f.WorkItem, f.Policy, f.Actor, f.Decision
	if w.Version != decision.ExpectedVersion {
		return ErrConflict
	}
	if w.Status != InReview || actor.PrincipalID == uuid.Nil || !actor.HasWorkReview {
		return ErrDoneGate
	}
	if policy.Version < 1 ||
		(policy.Source != ProjectReviewPolicySource && policy.Source != WorkReviewPolicySource) ||
		(policy.Mode != IndependentReview && policy.Mode != OwnerAcceptance) ||
		(decision.Mode != IndependentReview && decision.Mode != OwnerAcceptance) ||
		decision.Mode != policy.Mode ||
		f.LastMaterialVersion < 0 || f.LastMaterialVersion > w.Version {
		return ErrDoneGate
	}
	if !validDecisionReference(decision.ReferenceKind, decision.ReferenceValue) || !f.ReferenceVerified {
		return ErrDoneGate
	}

	switch decision.Mode {
	case IndependentReview:
		latest := LatestReviewerEntry(f.ReviewHistory)
		if latest == nil || latest.ID == uuid.Nil || latest.WorkItemID != w.ID ||
			latest.ProjectID != w.ProjectID || latest.Outcome != Passed ||
			latest.SelfReview || latest.WorkStatus != InReview ||
			latest.WorkVersion > w.Version ||
			latest.WorkVersion <= f.LastMaterialVersion ||
			latest.ReferenceKind != decision.ReferenceKind ||
			latest.ReferenceValue != decision.ReferenceValue {
			return ErrDoneGate
		}
		// Explicit Owner self-review acknowledgment must not be accepted
		// under an independently reviewed decision.
		if decision.AcknowledgeSelfReview {
			return ErrDoneGate
		}
		return nil
	case OwnerAcceptance:
		if policy.ForceIndependent || !policy.AllowOwnerAcceptance ||
			!decision.AcknowledgeSelfReview ||
			!validOwnerReason(decision.Reason) {
			return ErrDoneGate
		}
		owner := w.AccountableOwnerID != nil && *w.AccountableOwnerID == actor.PrincipalID
		if !owner && !actor.DelegatedOwner {
			return ErrDoneGate
		}
		return nil
	default:
		return ErrDoneGate
	}
}

func validOwnerReason(reason string) bool {
	return reason == strings.TrimSpace(reason) &&
		utf8.RuneCountInString(reason) >= 16 &&
		utf8.RuneCountInString(reason) <= 2000
}
