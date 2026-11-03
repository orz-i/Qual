package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newItem(t *testing.T) WorkItem {
	t.Helper()
	item, err := New(uuid.New(), uuid.New(), uuid.New(), uuid.New(), "ENG-1",
		Draft{Kind: Task, Title: "Implement authentication", AcceptanceCriteria: []string{"Only authorized users can view data"}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestCreateAndUpdate(t *testing.T) {
	w := newItem(t)
	if w.Version != 1 || w.Status != Backlog || w.Priority != Medium {
		t.Fatalf("invalid initial item: %#v", w)
	}
	title := "Updated authentication"
	if err := w.Update(Changes{Title: &title}, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w.Version != 2 || w.Title != title {
		t.Fatalf("update result unexpected: %#v", w)
	}
	if err := w.Update(Changes{Title: &title}, 1, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
}

func TestInvalidDrafts(t *testing.T) {
	for _, d := range []Draft{
		{Kind: Task, Title: ""},
		{Kind: Bug, Title: "Broken login", BugDetails: nil},
		{Kind: Task, Title: "Task", BugDetails: &BugDetails{Severity: "critical"}},
		{Kind: Bug, Title: "Bug", BugDetails: &BugDetails{Severity: "other"}},
		{Kind: Task, Title: "Task", AcceptanceCriteria: []string{""}},
	} {
		if _, err := New(uuid.New(), uuid.New(), uuid.New(), uuid.New(), "ENG-1", d, nil, time.Now()); !errors.Is(err, ErrValidation) {
			t.Errorf("expected validation error for %#v, got %v", d, err)
		}
	}
}

func TestClaimAndTransitions(t *testing.T) {
	w := newItem(t)
	if err := w.Claim(uuid.New(), 2, time.Now()); !errors.Is(err, ErrConflict) {
		t.Errorf("stale claim: %v", err)
	}
	if err := w.Claim(uuid.New(), 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.Claim(uuid.New(), 2, time.Now()); !errors.Is(err, ErrClaimed) {
		t.Errorf("second claim: %v", err)
	}
	if err := w.Transition(InProgress, 2, true, time.Now()); !errors.Is(err, ErrTransition) {
		t.Errorf("skipped state: %v", err)
	}
	for i, to := range []Status{Todo, InProgress, InReview} {
		if err := w.Transition(to, int64(2+i), true, time.Now()); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	if err := w.Transition(Done, 5, false, time.Now()); !errors.Is(err, ErrReview) {
		t.Fatalf("agent tried to approve: %v", err)
	}
	if err := w.Transition(Done, 5, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.Transition(Todo, 6, true, time.Now()); !errors.Is(err, ErrTransition) {
		t.Fatalf("completed state must not skip reopening: %v", err)
	}
}
