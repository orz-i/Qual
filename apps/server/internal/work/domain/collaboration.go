package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Comments are persisted as separate append-only facts, but touching the
// WorkItem increments its aggregate version to prevent invisible writes.
type Comment struct {
	ID         uuid.UUID `json:"id"`
	WorkItemID uuid.UUID `json:"work_item_id"`
	AuthorID   uuid.UUID `json:"author_id"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type Dependency struct {
	FromItemID uuid.UUID `json:"from_item_id"`
	ToItemID   uuid.UUID `json:"to_item_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func ValidateComment(body string) error {
	if strings.TrimSpace(body) == "" || utf8.RuneCountInString(body) > 10000 {
		return ErrValidation
	}
	return nil
}

func (w *WorkItem) Touch(expected int64, now time.Time) error {
	if w.Version != expected {
		return ErrConflict
	}
	w.Version++
	w.UpdatedAt = now.UTC()
	return nil
}
