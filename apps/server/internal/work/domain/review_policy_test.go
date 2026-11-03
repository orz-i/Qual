package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testDoneGateFacts() DoneGateFacts {
	owner, actor := uuid.New(), uuid.New()
	item := WorkItem{
		ID: uuid.New(), ProjectID: uuid.New(), WorkspaceID: uuid.New(),
		CreatedBy: owner, AccountableOwnerID: &owner,
		Status: InReview, Version: 12,
	}
	linkedRun := uuid.New().String()
	reviewer := RoleEntry{
		ID: uuid.New(), ProjectID: item.ProjectID, WorkItemID: item.ID,
		ActorID: actor, Capacity: Reviewer, Outcome: Passed,
		WorkVersion: 10, WorkStatus: InReview, SelfReview: false,
		ReferenceKind: TestRunReference, ReferenceValue: linkedRun,
		RecordedAt: time.Date(2026, time.November, 1, 10, 0, 0, 0, time.UTC),
	}
	return DoneGateFacts{
		WorkItem: item,
		Policy: EffectiveReviewPolicy{
			Mode:                 IndependentReview,
			Source:               ProjectReviewPolicySource,
			Version:              1,
			AllowOwnerAcceptance: false,
			ForceIndependent:     true,
		},
		Actor: DoneGateActor{PrincipalID: actor, HasWorkReview: true},
		Decision: DoneDecision{
			Mode: IndependentReview, ExpectedVersion: 12,
			ReferenceKind: TestRunReference, ReferenceValue: linkedRun,
		},
		ReviewHistory:       []RoleEntry{reviewer},
		LastMaterialVersion: 9, ReferenceVerified: true,
	}
}

func TestDoneGateIndependentValidDecisionAndNonMaterialComment(t *testing.T) {
	f := testDoneGateFacts()
	// Review at v10 remains valid after a harmless comment/RoleEntry touch
	// raised item version to 12; it was never silently reclassified.
	if err := EvaluateDoneGate(f); err != nil {
		t.Fatalf("valid independent review rejected: %v", err)
	}
}
func TestDoneGateIndependentLatestVerdictMustWin(t *testing.T) {
	for _, verdict := range []Outcome{NeedsWork, Failed, Passed} {
		f := testDoneGateFacts()
		recent := f.ReviewHistory[0]
		recent.ID = uuid.New()
		recent.RecordedAt = recent.RecordedAt.Add(time.Minute)
		recent.WorkVersion++
		recent.Outcome = verdict
		recent.SelfReview = verdict == Passed
		f.ReviewHistory = append(f.ReviewHistory, recent)
		if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
			t.Fatalf("latest %s / self=%v accepted older passed: %v", verdict, recent.SelfReview, err)
		}
	}
}
func TestDoneGateRejectsStaleMaterialChanges(t *testing.T) {
	f := testDoneGateFacts()
	f.LastMaterialVersion = 10
	if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
		t.Fatalf("review precedes material edit but passed: %v", err)
	}
	f = testDoneGateFacts()
	f.LastMaterialVersion = 13
	if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
		t.Fatalf("future material version unexpectedly accepted: %v", err)
	}
}
func TestDoneGateRequiresRealReviewEvidence(t *testing.T) {
	for _, mutate := range []func(*DoneGateFacts){
		func(f *DoneGateFacts) { f.ReviewHistory = nil },
		func(f *DoneGateFacts) { f.ReviewHistory[0].WorkStatus = InProgress },
		func(f *DoneGateFacts) { f.ReviewHistory[0].WorkVersion = f.WorkItem.Version + 1 },
		func(f *DoneGateFacts) { f.ReviewHistory[0].ProjectID = uuid.New() },
		func(f *DoneGateFacts) { f.ReviewHistory[0].WorkItemID = uuid.New() },
		func(f *DoneGateFacts) { f.ReviewHistory[0].ReferenceValue = uuid.New().String() },
		func(f *DoneGateFacts) { f.Decision.ReferenceValue = uuid.New().String() },
		func(f *DoneGateFacts) { f.Decision.ReferenceValue = "fake" },
		func(f *DoneGateFacts) { f.ReferenceVerified = false },
		func(f *DoneGateFacts) { f.Decision.AcknowledgeSelfReview = true },
	} {
		f := testDoneGateFacts()
		mutate(&f)
		if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
			t.Fatalf("invalid evidence accepted: %v", err)
		}
	}
}
func TestDoneGateCASAuthorityPolicyAndLegacyFailClosed(t *testing.T) {
	for _, mutate := range []func(*DoneGateFacts){
		func(f *DoneGateFacts) { f.Actor.HasWorkReview = false },
		func(f *DoneGateFacts) { f.Actor.PrincipalID = uuid.Nil },
		func(f *DoneGateFacts) { f.WorkItem.Status = Done }, // Existing historical Done is read-only.
		func(f *DoneGateFacts) { f.Policy.Version = 0 },
		func(f *DoneGateFacts) { f.Policy.Source = "client_claimed" },
		func(f *DoneGateFacts) { f.Policy.Mode = OwnerAcceptance },
		func(f *DoneGateFacts) { f.Decision.Mode = OwnerAcceptance },
	} {
		f := testDoneGateFacts()
		mutate(&f)
		if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
			t.Fatalf("invalid role/policy state accepted: %v", err)
		}
	}
	f := testDoneGateFacts()
	f.Decision.ExpectedVersion = 11
	if err := EvaluateDoneGate(f); !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS mismatch not conflict: %v", err)
	}
}
func TestDoneGateOwnerAcceptanceExplicitAndBoundToRealOwner(t *testing.T) {
	f := testDoneGateFacts()
	f.Policy.Mode = OwnerAcceptance
	f.Policy.ForceIndependent = false
	f.Policy.AllowOwnerAcceptance = true
	f.Actor.PrincipalID = *f.WorkItem.AccountableOwnerID
	f.Decision.Mode = OwnerAcceptance
	f.Decision.Reason = "Explicitly accepting residual accessibility risk after evidence review."
	f.Decision.AcknowledgeSelfReview = true
	f.ReviewHistory = nil // Owner mode does not invent independent reviewer passed.
	if err := EvaluateDoneGate(f); err != nil {
		t.Fatalf("authorized Owner acceptance rejected: %v", err)
	}
}
func TestDoneGateOwnerAcceptanceNeverBypassesProjectLimits(t *testing.T) {
	makeOwner := func() DoneGateFacts {
		f := testDoneGateFacts()
		f.Policy.Mode = OwnerAcceptance
		f.Policy.ForceIndependent = false
		f.Policy.AllowOwnerAcceptance = true
		f.Actor.PrincipalID = *f.WorkItem.AccountableOwnerID
		f.Decision.Mode = OwnerAcceptance
		f.Decision.Reason = "Risk accepted with actual technical testing and explicit Owner signoff."
		f.Decision.AcknowledgeSelfReview = true
		return f
	}
	for _, modify := range []func(*DoneGateFacts){
		func(f *DoneGateFacts) { f.Actor.HasWorkReview = false },
		func(f *DoneGateFacts) { f.Actor.PrincipalID = uuid.New() },
		func(f *DoneGateFacts) { f.Policy.AllowOwnerAcceptance = false },
		func(f *DoneGateFacts) { f.Policy.ForceIndependent = true },
		func(f *DoneGateFacts) { f.Decision.AcknowledgeSelfReview = false },
		func(f *DoneGateFacts) { f.Decision.Reason = "" },
		func(f *DoneGateFacts) { f.Decision.Reason = "too short" },
		func(f *DoneGateFacts) { f.Decision.Reason = " trailing space unacceptable " },
		func(f *DoneGateFacts) { f.Decision.ReferenceKind = NoReference },
		func(f *DoneGateFacts) { f.ReferenceVerified = false },
	} {
		f := makeOwner()
		modify(&f)
		if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
			t.Fatalf("invalid Owner acceptance accepted: %v", err)
		}
	}
}
func TestDoneGateExplicitDelegationDoesNotImplySeparatePrincipal(t *testing.T) {
	f := testDoneGateFacts()
	f.Policy.Mode, f.Policy.AllowOwnerAcceptance, f.Policy.ForceIndependent = OwnerAcceptance, true, false
	f.Decision.Mode, f.Decision.AcknowledgeSelfReview = OwnerAcceptance, true
	f.Decision.Reason = "Delegated accountable signer explicitly accepts residual risk."
	f.Actor.PrincipalID = uuid.New()
	f.Actor.DelegatedOwner = true
	if err := EvaluateDoneGate(f); err != nil {
		t.Fatalf("server-confirmed delegated Owner refused: %v", err)
	}
	// Grant alone never bypasses work:review.
	f.Actor.HasWorkReview = false
	if err := EvaluateDoneGate(f); !errors.Is(err, ErrDoneGate) {
		t.Fatalf("delegated Owner without review Scope accepted: %v", err)
	}
}
func TestDoneGateAssignedAgentMaySelfAcceptOnlyWithExplicitPolicyAndReviewPermission(t *testing.T) {
	f := testDoneGateFacts()
	agent := uuid.New()
	f.WorkItem.AccountableOwnerID = nil
	f.WorkItem.AssigneeID = &agent
	f.Actor.PrincipalID = agent
	f.Actor.DelegatedOwner = IsAssignedOwnerDelegate(f.WorkItem, agent)
	f.Policy.Mode = OwnerAcceptance
	f.Policy.AllowOwnerAcceptance = true
	f.Policy.ForceIndependent = false
	f.Decision.Mode = OwnerAcceptance
	f.Decision.AcknowledgeSelfReview = true
	f.Decision.Reason = "Agent with delegated project review authorization accepts recorded test evidence."
	f.ReviewHistory = nil
	if err := EvaluateDoneGate(f); err != nil {
		t.Fatalf("authorized, assigned Agent self-acceptance rejected: %v", err)
	}
	for _, change := range []func(*DoneGateFacts){
		func(f *DoneGateFacts) { f.Actor.HasWorkReview = false },
		func(f *DoneGateFacts) { f.Policy.AllowOwnerAcceptance = false },
		func(f *DoneGateFacts) { f.Policy.ForceIndependent = true },
		func(f *DoneGateFacts) { f.Actor.DelegatedOwner = false },
		func(f *DoneGateFacts) { f.Decision.AcknowledgeSelfReview = false },
		func(f *DoneGateFacts) { f.ReferenceVerified = false },
	} {
		d := f
		change(&d)
		if err := EvaluateDoneGate(d); !errors.Is(err, ErrDoneGate) {
			t.Fatalf("invalid delegated self-acceptance passed: %v", err)
		}
	}
	other := uuid.New()
	if IsAssignedOwnerDelegate(f.WorkItem, other) || IsAssignedOwnerDelegate(f.WorkItem, uuid.Nil) {
		t.Fatal("unassigned principal was delegated owner")
	}
	owner := uuid.New()
	f.WorkItem.AccountableOwnerID = &owner
	if IsAssignedOwnerDelegate(f.WorkItem, agent) {
		t.Fatal("assignee overrode explicit accountable owner")
	}
}

func TestLatestReviewerEntrySelectsNeedsWorkAfterOldPassed(t *testing.T) {
	f := testDoneGateFacts()
	latest := f.ReviewHistory[0]
	latest.ID = uuid.New()
	latest.RecordedAt = latest.RecordedAt.Add(time.Second)
	latest.Outcome = NeedsWork
	entries := []RoleEntry{latest, {ID: uuid.New(), Capacity: Tester}, f.ReviewHistory[0]}
	if got := LatestReviewerEntry(entries); got == nil || got.ID != latest.ID {
		t.Fatalf("selected stale reviewer %#v", got)
	}
	if got := LatestReviewerEntry([]RoleEntry{{Capacity: Tester}}); got != nil {
		t.Fatal("tester was misidentified as reviewer")
	}
}
