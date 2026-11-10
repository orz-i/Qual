// Package domain contains the WorkItem aggregate and its business invariants.
// It must not depend on HTTP, MCP, database or event delivery details.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrValidation = errors.New("invalid work item")
	ErrConflict   = errors.New("version conflict")
	ErrClaimed    = errors.New("work item already claimed")
	ErrTransition = errors.New("invalid workflow transition")
	ErrReview     = errors.New("human maintainer review required")
	ErrDependency = errors.New("invalid or cyclic dependency")
)

type Kind string
type Status string
type Priority string

const (
	Task Kind = "task"
	Bug  Kind = "bug"

	Backlog    Status = "backlog"
	Todo       Status = "todo"
	InProgress Status = "in_progress"
	InReview   Status = "in_review"
	Done       Status = "done"
	Cancelled  Status = "cancelled"

	Urgent Priority = "urgent"
	High   Priority = "high"
	Medium Priority = "medium"
	Low    Priority = "low"
)

type BugDetails struct {
	Severity         string `json:"severity"`
	Reproduction     string `json:"reproduction_steps"`
	ExpectedBehavior string `json:"expected_behavior"`
	ActualBehavior   string `json:"actual_behavior"`
	Environment      string `json:"environment"`
}

type WorkItem struct {
	ID                 uuid.UUID   `json:"id"`
	WorkspaceID        uuid.UUID   `json:"workspace_id"`
	ProjectID          uuid.UUID   `json:"project_id"`
	Key                string      `json:"key"`
	Kind               Kind        `json:"kind"`
	Title              string      `json:"title"`
	Description        string      `json:"description"`
	AcceptanceCriteria []string    `json:"acceptance_criteria"`
	Status             Status      `json:"status"`
	Priority           Priority    `json:"priority"`
	AssigneeID         *uuid.UUID  `json:"assignee_id"`
	AccountableOwnerID *uuid.UUID  `json:"accountable_owner_id"`
	CreatedBy          uuid.UUID   `json:"created_by"`
	Version            int64       `json:"version"`
	BugDetails         *BugDetails `json:"bug_details,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

type Draft struct {
	Kind               Kind        `json:"kind"`
	Title              string      `json:"title"`
	Description        string      `json:"description"`
	AcceptanceCriteria []string    `json:"acceptance_criteria"`
	Priority           Priority    `json:"priority"`
	BugDetails         *BugDetails `json:"bug_details,omitempty"`
}

type Changes struct {
	Title              *string   `json:"title,omitempty"`
	Description        *string   `json:"description,omitempty"`
	AcceptanceCriteria *[]string `json:"acceptance_criteria,omitempty"`
	Priority           *Priority `json:"priority,omitempty"`
}

func ValidPriority(p Priority) bool {
	return p == Urgent || p == High || p == Medium || p == Low
}

func ValidateDraft(d Draft) error {
	if d.Kind != Task && d.Kind != Bug {
		return ErrValidation
	}
	if err := validateText(d.Title, d.Description, d.AcceptanceCriteria, d.Priority); err != nil {
		return err
	}
	if d.Kind == Task && d.BugDetails != nil {
		return ErrValidation
	}
	if d.Kind == Bug {
		if d.BugDetails == nil || !validSeverity(d.BugDetails.Severity) {
			return ErrValidation
		}
		if utf8.RuneCountInString(d.BugDetails.Reproduction) > 20000 ||
			utf8.RuneCountInString(d.BugDetails.ActualBehavior) > 20000 ||
			utf8.RuneCountInString(d.BugDetails.ExpectedBehavior) > 20000 ||
			utf8.RuneCountInString(d.BugDetails.Environment) > 2000 {
			return ErrValidation
		}
	}
	return nil
}

func validSeverity(s string) bool {
	switch s {
	case "blocker", "critical", "major", "minor", "trivial":
		return true
	}
	return false
}

func validateText(title, description string, criteria []string, priority Priority) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 240 || utf8.RuneCountInString(description) > 20000 ||
		!ValidPriority(priority) || len(criteria) > 30 {
		return ErrValidation
	}
	for _, criterion := range criteria {
		if strings.TrimSpace(criterion) == "" || utf8.RuneCountInString(criterion) > 2000 {
			return ErrValidation
		}
	}
	return nil
}

func New(id, workspaceID, projectID, actorID uuid.UUID, key string, draft Draft, owner *uuid.UUID, now time.Time) (WorkItem, error) {
	if id == uuid.Nil || workspaceID == uuid.Nil || projectID == uuid.Nil || actorID == uuid.Nil || key == "" {
		return WorkItem{}, ErrValidation
	}
	if draft.Priority == "" {
		draft.Priority = Medium
	}
	if err := ValidateDraft(draft); err != nil {
		return WorkItem{}, err
	}
	if draft.AcceptanceCriteria == nil {
		draft.AcceptanceCriteria = []string{}
	}
	return WorkItem{
		ID: id, WorkspaceID: workspaceID, ProjectID: projectID, Key: key,
		Kind: draft.Kind, Title: strings.TrimSpace(draft.Title), Description: draft.Description,
		AcceptanceCriteria: append([]string{}, draft.AcceptanceCriteria...),
		Status:             Backlog, Priority: draft.Priority, AccountableOwnerID: owner,
		CreatedBy: actorID, Version: 1, BugDetails: draft.BugDetails,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (w *WorkItem) Update(ch Changes, expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	if ch.Title == nil && ch.Description == nil && ch.AcceptanceCriteria == nil && ch.Priority == nil {
		return ErrValidation
	}
	copy := *w
	if ch.Title != nil {
		copy.Title = strings.TrimSpace(*ch.Title)
	}
	if ch.Description != nil {
		copy.Description = *ch.Description
	}
	if ch.AcceptanceCriteria != nil {
		copy.AcceptanceCriteria = append([]string{}, (*ch.AcceptanceCriteria)...)
	}
	if ch.Priority != nil {
		copy.Priority = *ch.Priority
	}
	if err := validateText(copy.Title, copy.Description, copy.AcceptanceCriteria, copy.Priority); err != nil {
		return err
	}
	copy.Version++
	copy.UpdatedAt = now.UTC()
	*w = copy
	return nil
}

func (w *WorkItem) Claim(principalID uuid.UUID, expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	if principalID == uuid.Nil {
		return ErrValidation
	}
	if w.AssigneeID != nil {
		return ErrClaimed
	}
	if w.Status != Backlog && w.Status != Todo {
		return ErrTransition
	}
	w.AssigneeID = &principalID
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

// ClaimResumed permits a new eligible participant to continue an explicitly
// released in_progress WorkItem in task_scoped mode. The existing Claim method
// remains unchanged for legacy projects and backlog/todo transitions.
func (w *WorkItem) ClaimResumed(principalID uuid.UUID, expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	if principalID == uuid.Nil {
		return ErrValidation
	}
	if w.AssigneeID != nil {
		return ErrClaimed
	}
	if w.Status != InProgress {
		return ErrTransition
	}
	w.AssigneeID = &principalID
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

// Release detaches the current executor without rewriting any of their
// immutable audit/test history. Who may release is checked by the server.
func (w *WorkItem) Release(expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	if w.Status != Backlog && w.Status != Todo && w.Status != InProgress {
		return ErrTransition
	}
	if w.AssigneeID == nil {
		return ErrTransition
	}
	w.AssigneeID = nil
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

// Reassign requires an existing assignee except for explicit manager recovery
// of an unassigned in_progress task. The server verifies both the initiating
// actor's eligibility/manager privilege and the receiving Principal's scope.
func (w *WorkItem) Reassign(principalID uuid.UUID, expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	if principalID == uuid.Nil {
		return ErrValidation
	}
	if w.Status != Backlog && w.Status != Todo && w.Status != InProgress {
		return ErrTransition
	}
	if w.AssigneeID == nil && w.Status != InProgress {
		return ErrTransition
	}
	if w.AssigneeID != nil && *w.AssigneeID == principalID {
		return ErrValidation
	}
	w.AssigneeID = &principalID
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}

var allowed = map[Status][]Status{
	Backlog:    {Todo, Cancelled},
	Todo:       {InProgress, Cancelled},
	InProgress: {InReview, Cancelled},
	InReview:   {InProgress, Done},
}

func (w *WorkItem) Transition(next Status, expected int64, isAuthorizedReviewer bool, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	canMove := false
	for _, candidate := range allowed[w.Status] {
		if candidate == next {
			canMove = true
			break
		}
	}
	if !canMove {
		return ErrTransition
	}
	if next == Done && !isAuthorizedReviewer {
		return ErrReview
	}
	w.Status = next
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}
