package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommentTouchInvariants(t *testing.T) {
	for _, comment := range []string{"", "  ", strings.Repeat("a", 10001)} {
		if err := ValidateComment(comment); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid comment accepted: %q", comment[:min(len(comment), 10)])
		}
	}
	w := newItem(t)
	if err := w.Touch(2, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale touch %v", err)
	}
	if err := w.Touch(1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w.Version != 2 {
		t.Fatalf("touch version %d", w.Version)
	}
}
