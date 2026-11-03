// Package domain models immutable, externally verified adapter event facts.
// A received Event never changes WorkItem/Review/Test truth or launches Agents.
package domain

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrValidation  = errors.New("invalid external event")
	ErrNotFound    = errors.New("external event association not found")
	ErrConflict    = errors.New("external event natural identity collision")
	ErrIdempotency = errors.New("external event request idempotency conflict")
)

var (
	sourceRE      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	eventTypeRE   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
	sourceEventRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)
)

type Input struct {
	WorkItemID      uuid.UUID `json:"work_item_id"`
	ThreadID        uuid.UUID `json:"thread_id"`
	Source          string    `json:"source"`
	ExternalEventID string    `json:"external_event_id"`
	EventType       string    `json:"event_type"`
	OccurredAt      time.Time `json:"occurred_at"`
	PayloadSHA256   string    `json:"payload_sha256"`
	Summary         string    `json:"summary"`
}

type Event struct {
	ID                  uuid.UUID `json:"id"`
	ProjectID           uuid.UUID `json:"project_id"`
	WorkItemID          uuid.UUID `json:"work_item_id"`
	ThreadID            uuid.UUID `json:"thread_id"`
	Source              string    `json:"source"`
	ExternalEventID     string    `json:"external_event_id"`
	EventType           string    `json:"event_type"`
	OccurredAt          time.Time `json:"occurred_at"`
	ReceivedAt          time.Time `json:"received_at"`
	PayloadSHA256       string    `json:"payload_sha256"`
	Summary             string    `json:"summary"`
	ReporterPrincipalID uuid.UUID `json:"reporter_principal_id"`
}

type Page struct {
	Items      []Event `json:"items"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

func (in Input) Validate(now time.Time) error {
	if in.WorkItemID == uuid.Nil || in.ThreadID == uuid.Nil ||
		!sourceRE.MatchString(in.Source) || !sourceEventRE.MatchString(in.ExternalEventID) ||
		!eventTypeRE.MatchString(in.EventType) || in.OccurredAt.IsZero() ||
		in.OccurredAt.After(now.Add(5*time.Minute)) ||
		len(in.PayloadSHA256) != 64 || strings.ToLower(in.PayloadSHA256) != in.PayloadSHA256 {
		return ErrValidation
	}
	if _, err := hex.DecodeString(in.PayloadSHA256); err != nil {
		return ErrValidation
	}
	if !utf8.ValidString(in.Summary) || len(in.Summary) == 0 || len(in.Summary) > 240 ||
		strings.TrimSpace(in.Summary) != in.Summary {
		return ErrValidation
	}
	// Only a bounded display note is stored. Raw event JSON, headers, PATs,
	// cookies and HTML/prompt bodies are not accepted as event summaries.
	for _, r := range in.Summary {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return ErrValidation
		}
	}
	lower := strings.ToLower(in.Summary)
	for _, unsafe := range []string{"qual_pat_", "qlc_", "bearer ", "authorization:", "password=", "token=", "cookie:", "set-cookie"} {
		if strings.Contains(lower, unsafe) {
			return ErrValidation
		}
	}
	return nil
}
