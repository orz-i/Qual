// Package domain contains the task-scoped authorization decision used by every
// WorkItem write. It does not authenticate credentials or access the database.
package domain

import "github.com/google/uuid"

// TaskAuthorizationMode is a project-wide opt-in; absent policy is legacy.
type TaskAuthorizationMode string

const (
	LegacyAuthorization     TaskAuthorizationMode = "legacy"
	TaskScopedAuthorization TaskAuthorizationMode = "task_scoped"
)

// TaskAction names an exact WorkItem operation, not a principal role.
type TaskAction string

const (
	TaskClaim               TaskAction = "task.claim"
	TaskUpdate              TaskAction = "task.update"
	TaskComment             TaskAction = "task.comment"
	TaskStart               TaskAction = "task.start"
	TaskSubmitReview        TaskAction = "task.submit_review"
	TaskCancel              TaskAction = "task.cancel"
	TaskRelease             TaskAction = "task.release"
	TaskReassign            TaskAction = "task.reassign"
	TaskReportTest          TaskAction = "task.report_test"
	TaskReadTest            TaskAction = "task.read_test"
	TaskReadEvidence        TaskAction = "task.read_evidence"
	TaskAttachEvidence      TaskAction = "task.attach_evidence"
	TaskRecordExecutor      TaskAction = "task.record_executor"
	TaskRecordTester        TaskAction = "task.record_tester"
	TaskClaimReview         TaskAction = "task.claim_review"
	TaskRecordReview        TaskAction = "task.record_review"
	TaskReturnForWork       TaskAction = "task.return_for_work"
	TaskReleaseReview       TaskAction = "task.release_review"
	TaskCompleteIndependent TaskAction = "task.complete_independent"
	TaskCompleteOwner       TaskAction = "task.complete_owner"
)

// TaskAccessFacts come only from authenticated actor + persisted project,
// credential and WorkItem facts. Never deserialize this type from API input.
type TaskAccessFacts struct {
	ActorID           uuid.UUID
	WorkItem          WorkItem
	Action            TaskAction
	ProjectMode       TaskAuthorizationMode
	Participant       bool       // active Human/Agent membership, credential work:read+work:claim and effective project grant
	ExplicitDeny      bool       // canonical project permission is explicitly denied
	ReviewerID        *uuid.UUID // current review assignment; nil until QUAL-57
	ReviewMode        ReviewPolicyMode
	ForcedIndependent bool
}

func (f TaskAccessFacts) Allowed() bool {
	if f.ProjectMode != TaskScopedAuthorization || !f.Participant || f.ExplicitDeny ||
		f.ActorID == uuid.Nil || f.WorkItem.ID == uuid.Nil || f.WorkItem.ProjectID == uuid.Nil {
		return false
	}
	w := f.WorkItem
	executor := w.AssigneeID != nil && *w.AssigneeID == f.ActorID
	reviewer := f.ReviewerID != nil && *f.ReviewerID == f.ActorID
	owner := (w.AccountableOwnerID != nil && *w.AccountableOwnerID == f.ActorID) ||
		IsAssignedOwnerDelegate(w, f.ActorID)
	switch f.Action {
	case TaskClaim:
		return (w.Status == Backlog || w.Status == Todo || w.Status == InProgress) && w.AssigneeID == nil
	case TaskUpdate, TaskStart, TaskRelease, TaskReassign, TaskRecordExecutor:
		return executor && (w.Status == Backlog || w.Status == Todo || w.Status == InProgress)
	case TaskCancel:
		return executor && w.Status == InProgress
	case TaskComment:
		return (executor && (w.Status == Backlog || w.Status == Todo || w.Status == InProgress || w.Status == InReview)) ||
			(reviewer && w.Status == InReview)
	case TaskSubmitReview, TaskRecordTester:
		return executor && w.Status == InProgress
	case TaskReportTest, TaskAttachEvidence:
		return (executor && w.Status == InProgress) || (reviewer && w.Status == InReview)
	case TaskReadTest, TaskReadEvidence:
		// Read rights follow current task relation even after successful
		// completion, but never follow *previous* assignee/reviewer history.
		return executor || reviewer || owner
	case TaskClaimReview:
		// The persistence layer additionally checks all historical executors,
		// testers, prior assignee changes and task-bound TestAttempts.
		return w.Status == InReview && f.ReviewMode == IndependentReview &&
			f.ReviewerID == nil && !executor && w.CreatedBy != f.ActorID &&
			(w.AccountableOwnerID == nil || *w.AccountableOwnerID != f.ActorID)
	case TaskRecordReview, TaskCompleteIndependent:
		return reviewer && w.Status == InReview && f.ReviewMode == IndependentReview
	case TaskReturnForWork:
		// An authorized Owner may return their own acceptance to implementation
		// without inventing an independent reviewer for a routine WorkItem.
		return w.Status == InReview && ((reviewer && f.ReviewMode == IndependentReview) ||
			(owner && f.ReviewMode == OwnerAcceptance && !f.ForcedIndependent))
	case TaskReleaseReview:
		// A reviewer may relinquish the relation even after policy changes.
		return reviewer && w.Status == InReview
	case TaskCompleteOwner:
		return owner && w.Status == InReview && f.ReviewMode == OwnerAcceptance && !f.ForcedIndependent
	default:
		return false // including auditor and any future/unregistered action
	}
}

// TaskTransitionAction maps the existing status machine to exact task actions.
// Returning empty rejects any unknown/new transition (the domain state machine
// still validates source/target after authorization).
func TaskTransitionAction(current, next Status) TaskAction {
	switch {
	case current == Backlog && next == Todo, current == Todo && next == InProgress:
		return TaskStart
	case current == InProgress && next == InReview:
		return TaskSubmitReview
	case current == InProgress && next == Cancelled:
		return TaskCancel
	case current == InReview && next == InProgress:
		return TaskReturnForWork
	default:
		return ""
	}
}
