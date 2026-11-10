package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTaskReleaseReassignDomainLifecycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	for _, status := range []Status{Backlog, Todo, InProgress} {
		t.Run(string(status), func(t *testing.T) {
			w := newItem(t)
			w.Status = status
			w.AssigneeID = &a
			if err := w.Release(2, time.Now()); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale release=%v", err)
			}
			if err := w.Release(1, time.Now()); err != nil {
				t.Fatal(err)
			}
			if w.AssigneeID != nil || w.Version != 2 || w.Status != status {
				t.Fatalf("release state=%+v", w)
			}
			if err := w.Release(2, time.Now()); !errors.Is(err, ErrTransition) {
				t.Fatalf("second release=%v", err)
			}
			if status == InProgress {
				// Legacy Claim stays forbidden on an unassigned in_progress task.
				if err := w.Claim(b, 2, time.Now()); !errors.Is(err, ErrTransition) {
					t.Fatalf("legacy claim changed: %v", err)
				}
				if err := w.ClaimResumed(b, 2, time.Now()); err != nil {
					t.Fatal(err)
				}
				if w.AssigneeID == nil || *w.AssigneeID != b || w.Status != InProgress {
					t.Fatalf("resume=%+v", w)
				}
			} else {
				if err := w.Claim(b, 2, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, status := range []Status{InReview, Done, Cancelled} {
		w := newItem(t)
		w.Status = status
		w.AssigneeID = &a
		if err := w.Release(1, time.Now()); !errors.Is(err, ErrTransition) {
			t.Errorf("release %s: %v", status, err)
		}
		if err := w.Reassign(b, 1, time.Now()); !errors.Is(err, ErrTransition) {
			t.Errorf("reassign %s: %v", status, err)
		}
	}
	t.Run("reassign existing owner", func(t *testing.T) {
		w := newItem(t)
		w.AssigneeID = &a
		if err := w.Reassign(a, 1, time.Now()); !errors.Is(err, ErrValidation) {
			t.Fatalf("no-op reassign=%v", err)
		}
		if err := w.Reassign(uuid.Nil, 1, time.Now()); !errors.Is(err, ErrValidation) {
			t.Fatalf("nil target=%v", err)
		}
		if err := w.Reassign(b, 2, time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale=%v", err)
		}
		if err := w.Reassign(b, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
		if w.Version != 2 || w.AssigneeID == nil || *w.AssigneeID != b {
			t.Fatal(w)
		}
	})
	t.Run("unassigned manager recovery in progress only", func(t *testing.T) {
		w := newItem(t)
		w.Status = InProgress
		if err := w.Reassign(b, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
		if w.AssigneeID == nil || *w.AssigneeID != b {
			t.Fatal(w)
		}
		w2 := newItem(t)
		w2.Status = Todo
		if err := w2.Reassign(b, 1, time.Now()); !errors.Is(err, ErrTransition) {
			t.Fatalf("unclaimed todo reassign=%v", err)
		}
	})
	t.Run("resumed claim prerequisites", func(t *testing.T) {
		w := newItem(t)
		if err := w.ClaimResumed(b, 1, time.Now()); !errors.Is(err, ErrTransition) {
			t.Fatalf("backlog resumed=%v", err)
		}
		w.Status = InProgress
		if err := w.ClaimResumed(uuid.Nil, 1, time.Now()); !errors.Is(err, ErrValidation) {
			t.Fatalf("nil claim=%v", err)
		}
		if err := w.ClaimResumed(b, 1, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := w.ClaimResumed(a, 2, time.Now()); !errors.Is(err, ErrClaimed) {
			t.Fatalf("already claimed=%v", err)
		}
	})
}
func TestReleasedInProgressClaimPolicy(t *testing.T) {
	a := uuid.New()
	w := newItem(t)
	w.Status = InProgress
	f := TaskAccessFacts{ActorID: a, WorkItem: w, Action: TaskClaim, ProjectMode: TaskScopedAuthorization, Participant: true}
	if !f.Allowed() {
		t.Fatal("scoped resumption must be allowed on unassigned in_progress")
	}
	w.AssigneeID = &a
	f.WorkItem = w
	if f.Allowed() {
		t.Fatal("must not claim already assigned in_progress")
	}
}
