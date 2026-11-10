package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestTaskAccessMatrix(t *testing.T) {
	a, b, project := uuid.New(), uuid.New(), uuid.New()
	base := WorkItem{ID: uuid.New(), ProjectID: project, CreatedBy: b, AssigneeID: &a, Status: InProgress}
	baseFacts := TaskAccessFacts{ActorID: a, WorkItem: base, Action: TaskUpdate,
		ProjectMode: TaskScopedAuthorization, Participant: true}
	check := func(label string, want bool, change func(*TaskAccessFacts)) {
		t.Helper()
		f := baseFacts
		change(&f)
		if got := f.Allowed(); got != want {
			t.Errorf("%s got %v want %v", label, got, want)
		}
	}
	check("assignee can update", true, func(*TaskAccessFacts) {})
	check("no project mode", false, func(f *TaskAccessFacts) { f.ProjectMode = LegacyAuthorization })
	check("no participation", false, func(f *TaskAccessFacts) { f.Participant = false })
	check("project explicit deny", false, func(f *TaskAccessFacts) { f.ExplicitDeny = true })
	check("nil actor", false, func(f *TaskAccessFacts) { f.ActorID = uuid.Nil })
	check("unclaimed", false, func(f *TaskAccessFacts) { f.WorkItem.AssigneeID = nil })
	check("another task", false, func(f *TaskAccessFacts) { f.ActorID = b })
	check("done", false, func(f *TaskAccessFacts) { f.WorkItem.Status = Done })
	check("cancelled", false, func(f *TaskAccessFacts) { f.WorkItem.Status = Cancelled })
	check("review freeze", false, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview })
	check("comment in review", true, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview; f.Action = TaskComment })
	check("submit review", true, func(f *TaskAccessFacts) { f.Action = TaskSubmitReview })
	check("no test after submit", false, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview; f.Action = TaskRecordTester })
	check("test in progress", true, func(f *TaskAccessFacts) { f.Action = TaskReportTest })
	check("evidence in progress", true, func(f *TaskAccessFacts) { f.Action = TaskAttachEvidence })
	check("owner acceptance", true, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewMode = OwnerAcceptance
		f.Action = TaskCompleteOwner
	})
	check("forced independent blocks owner", false, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewMode = OwnerAcceptance
		f.ForcedIndependent = true
		f.Action = TaskCompleteOwner
	})
	check("independent blocks owner", false, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewMode = IndependentReview
		f.Action = TaskCompleteOwner
	})
	check("independent without reviewer", false, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewMode = IndependentReview
		f.Action = TaskRecordReview
	})
	check("reviewer on another task", false, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview; f.ReviewerID = &b; f.Action = TaskRecordReview })
	check("assigned independent reviewer", true, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewerID = &a
		f.ReviewMode = IndependentReview
		f.Action = TaskRecordReview
	})
	check("unknown action denied", false, func(f *TaskAccessFacts) { f.Action = "task.arbitrary" })
	check("auditor not an implicit task role", false, func(f *TaskAccessFacts) { f.Action = "task.record_auditor" })
	check("claim unassigned todo", true, func(f *TaskAccessFacts) { f.WorkItem.AssigneeID = nil; f.WorkItem.Status = Todo; f.Action = TaskClaim })
	check("claim other agent's task", false, func(f *TaskAccessFacts) { f.Action = TaskClaim })
	check("claim done", false, func(f *TaskAccessFacts) { f.WorkItem.AssigneeID = nil; f.WorkItem.Status = Done; f.Action = TaskClaim })
	check("reviewer claim by previous executor denied", false, func(f *TaskAccessFacts) {
		f.WorkItem.Status = InReview
		f.ReviewMode = IndependentReview
		f.Action = TaskClaimReview
	})
	check("reviewer claim candidate only", true, func(f *TaskAccessFacts) {
		f.ActorID = b
		f.WorkItem.CreatedBy = a
		f.WorkItem.Status = InReview
		f.ReviewMode = IndependentReview
		f.Action = TaskClaimReview
	})
	check("reassign only assignee", false, func(f *TaskAccessFacts) { f.ActorID = b; f.Action = TaskReassign })
	check("release only active status", false, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview; f.Action = TaskRelease })
	check("review return only reviewer", false, func(f *TaskAccessFacts) { f.WorkItem.Status = InReview; f.Action = TaskReturnForWork })
}
func TestTaskTransitionAction(t *testing.T) {
	matrix := []struct {
		from, to Status
		expected TaskAction
	}{
		{Backlog, Todo, TaskStart}, {Todo, InProgress, TaskStart},
		{InProgress, InReview, TaskSubmitReview}, {InProgress, Cancelled, TaskCancel},
		{InReview, InProgress, TaskReturnForWork}, {InReview, Done, ""},
		{Backlog, Cancelled, ""}, {Todo, Cancelled, ""}, {Done, InProgress, ""},
		{Todo, Todo, ""}, {Status("bogus"), Todo, ""},
	}
	for _, v := range matrix {
		if got := TaskTransitionAction(v.from, v.to); got != v.expected {
			t.Errorf("%s -> %s: %q != %q", v.from, v.to, got, v.expected)
		}
	}
}

func TestCurrentReviewerAndOwnerReturnCapabilities(t *testing.T) {
	executor, reviewer, project := uuid.New(), uuid.New(), uuid.New()
	item := WorkItem{ID: uuid.New(), ProjectID: project, CreatedBy: executor, AssigneeID: &executor, Status: InReview}
	facts := TaskAccessFacts{ActorID: reviewer, WorkItem: item, ReviewerID: &reviewer,
		Action: TaskRecordReview, ProjectMode: TaskScopedAuthorization, Participant: true, ReviewMode: IndependentReview}
	if !facts.Allowed() {
		t.Fatal("current independent reviewer cannot record review")
	}
	facts.ReviewMode = OwnerAcceptance
	if facts.Allowed() {
		t.Fatal("policy switch left independent reviewer privilege")
	}
	facts.Action = TaskReleaseReview
	if !facts.Allowed() {
		t.Fatal("reviewer cannot release after policy switch")
	}
	facts.ActorID = executor
	facts.ReviewerID = nil
	facts.Action = TaskReturnForWork
	if !facts.Allowed() {
		t.Fatal("routine Owner cannot return reviewed task to implementation")
	}
	facts.ForcedIndependent = true
	if facts.Allowed() {
		t.Fatal("high risk Owner improperly bypassed independent review")
	}
}
