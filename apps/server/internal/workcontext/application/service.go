// Package application composes bounded, authorized WorkItem recovery facts.
// It never stores an Agent session or infers testing/review outcomes.
package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	permissions "qual/internal/identity/domain"
	work "qual/internal/work/application"
	workdomain "qual/internal/work/domain"
)

var (
	ErrInvalidQuery = errors.New("invalid work context query")
	ErrStaleCursor  = errors.New("work context cursor is stale")
	ErrScanBudget   = errors.New("work context source scan budget exceeded")
)

type Section string

const (
	Overview     Section = "overview"
	Dependencies Section = "dependencies"
	Comments     Section = "comments"
	Reviews      Section = "reviews"
	Acceptance   Section = "acceptance"
	Coverage     Section = "coverage"
	Attempts     Section = "attempts"
	Artifacts    Section = "artifacts"
	References   Section = "references"
)

var sections = []Section{Dependencies, Comments, Reviews, Acceptance, Coverage, Attempts, Artifacts, References}

func validSection(s Section) bool {
	if s == Overview {
		return true
	}
	for _, candidate := range sections {
		if s == candidate {
			return true
		}
	}
	return false
}

// Reader is a read-model port. All queries must filter by project+WorkItem and
// return at most limit+1 rows in deterministic order, never whole histories.
// PageQuery never permits arbitrary sorting or SQL. Attempt pages use a stable
// keyset bound; other sections preserve the initial bounded offset contract.
type PageQuery struct {
	Limit      int
	Offset     int
	BeforeTime *time.Time
	BeforeID   uuid.UUID
}

type Reader interface {
	Page(ctx context.Context, project, item uuid.UUID, section Section, query PageQuery) ([]json.RawMessage, error)
}

type WorkReader interface {
	Get(context.Context, identity.Actor, uuid.UUID, uuid.UUID) (workdomain.WorkItem, error)
	ReadAcceptanceHistory(context.Context, identity.Actor, uuid.UUID, uuid.UUID) (work.AcceptanceHistory, error)
}

type Service struct {
	Identity identity.Authenticator
	Work     WorkReader
	Reader   Reader
	Clock    func() time.Time
}

type Input struct {
	Section Section
	Limit   int
	Cursor  string
}

type Summary struct {
	ID                 uuid.UUID           `json:"id"`
	Key                string              `json:"key"`
	Title              string              `json:"title"`
	Kind               workdomain.Kind     `json:"kind"`
	Status             workdomain.Status   `json:"status"`
	Priority           workdomain.Priority `json:"priority"`
	Version            int64               `json:"version"`
	AssigneeID         *uuid.UUID          `json:"assignee_id"`
	AccountableOwnerID *uuid.UUID          `json:"accountable_owner_id"`
}

type Page struct {
	SchemaVersion     int                     `json:"schema_version"`
	ContextRevision   int64                   `json:"context_revision"`
	ProjectID         uuid.UUID               `json:"project_id"`
	GeneratedAt       time.Time               `json:"generated_at"`
	Consistency       string                  `json:"consistency"`
	WorkItem          Summary                 `json:"work_item"`
	Section           Section                 `json:"section"`
	Items             []json.RawMessage       `json:"items"`
	HasMore           bool                    `json:"has_more"`
	PagingLimited     bool                    `json:"paging_limited"`
	NextCursor        string                  `json:"next_cursor,omitempty"`
	AvailableSections []Section               `json:"available_sections,omitempty"`
	LatestReview      json.RawMessage         `json:"latest_review,omitempty"`
	Acceptance        *work.AcceptanceHistory `json:"acceptance,omitempty"`
}

// A cursor only protects the page's WorkItem version and identity, not other
// aggregates. No cross-domain snapshot guarantee is implied.
type cursorData struct {
	Project    uuid.UUID  `json:"p"`
	Item       uuid.UUID  `json:"w"`
	Version    int64      `json:"v"`
	Section    Section    `json:"s"`
	Offset     int        `json:"o"`
	BeforeTime *time.Time `json:"t,omitempty"`
	BeforeID   *uuid.UUID `json:"i,omitempty"`
}

func encodeCursor(c cursorData) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseCursor(raw string) (cursorData, error) {
	var c cursorData
	if len(raw) < 1 || len(raw) > 512 {
		return c, ErrInvalidQuery
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &c) != nil || !validSection(c.Section) ||
		c.Project == uuid.Nil || c.Item == uuid.Nil || c.Version < 1 || c.Offset < 1 || c.Offset > 10000 {
		return cursorData{}, ErrInvalidQuery
	}
	if c.Section == Attempts {
		if c.BeforeTime == nil || c.BeforeTime.IsZero() || c.BeforeID == nil || *c.BeforeID == uuid.Nil {
			return cursorData{}, ErrInvalidQuery
		}
	} else if c.BeforeTime != nil || c.BeforeID != nil {
		return cursorData{}, ErrInvalidQuery
	}
	return c, nil
}

func (s Service) Get(ctx context.Context, actor identity.Actor, projectID, itemID uuid.UUID, in Input) (Page, error) {
	if s.Work == nil || s.Reader == nil || s.Identity == nil {
		return Page{}, errors.New("work context read model is not configured")
	}
	if in.Section == "" {
		in.Section = Overview
	}
	if !validSection(in.Section) || (in.Section == Overview || in.Section == Acceptance) && in.Cursor != "" {
		return Page{}, ErrInvalidQuery
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > 100 || in.Cursor != "" && len(in.Cursor) > 512 {
		return Page{}, ErrInvalidQuery
	}

	// Always authorize and resolve the WorkItem before querying any secondary
	// table. An inaccessible/cross-project ID cannot reveal section contents.
	item, err := s.Work.Get(ctx, actor, projectID, itemID)
	if err != nil {
		return Page{}, err
	}
	permission := permissions.WorkRead
	switch in.Section {
	case Coverage, Attempts:
		permission = permissions.TestRead
	case Artifacts:
		permission = permissions.ArtifactRead
	}
	if _, err := s.Identity.AuthorizeProject(ctx, actor, projectID, permission); err != nil {
		return Page{}, err
	}

	offset := 0
	var beforeTime *time.Time
	beforeID := uuid.Nil
	if in.Cursor != "" {
		c, err := parseCursor(in.Cursor)
		if err != nil || c.Section != in.Section || c.Project != projectID || c.Item != itemID {
			return Page{}, ErrInvalidQuery
		}
		if c.Version != item.Version {
			return Page{}, ErrStaleCursor
		}
		offset = c.Offset
		if in.Section == Attempts {
			beforeTime, beforeID = c.BeforeTime, *c.BeforeID
		}
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	result := Page{
		SchemaVersion: 2, ContextRevision: item.Version, ProjectID: projectID,
		GeneratedAt: now, Consistency: "best_effort",
		WorkItem: Summary{ID: item.ID, Key: item.Key, Title: item.Title,
			Kind: item.Kind, Status: item.Status, Priority: item.Priority, Version: item.Version,
			AssigneeID: item.AssigneeID, AccountableOwnerID: item.AccountableOwnerID},
		Section: in.Section, Items: []json.RawMessage{}, HasMore: false, PagingLimited: false,
	}
	if in.Section == Overview {
		result.AvailableSections = append([]Section{}, sections...)
		reviews, err := s.Reader.Page(ctx, projectID, itemID, Reviews, PageQuery{Limit: 1})
		if err != nil {
			return Page{}, err
		}
		if len(reviews) > 0 {
			result.LatestReview = reviews[0]
		}
		// This is the existing, authoritative Done Gate projection. Legacy
		// completed items remain explicitly unclassified, not auto-approved.
		if item.Status == workdomain.Done {
			h, err := s.Work.ReadAcceptanceHistory(ctx, actor, projectID, itemID)
			if err != nil {
				return Page{}, err
			}
			result.Acceptance = &h
		}
		return result, nil
	}
	if in.Section == Acceptance {
		h, err := s.Work.ReadAcceptanceHistory(ctx, actor, projectID, itemID)
		if err != nil {
			return Page{}, err
		}
		b, err := json.Marshal(h)
		if err != nil {
			return Page{}, err
		}
		result.Items = []json.RawMessage{b}
		return result, nil
	}

	// Request one extra row solely to determine has_more; don't expose it.
	query := PageQuery{Limit: in.Limit + 1, Offset: offset, BeforeTime: beforeTime, BeforeID: beforeID}
	rows, err := s.Reader.Page(ctx, projectID, itemID, in.Section, query)
	if err != nil {
		return Page{}, err
	}
	if len(rows) > in.Limit {
		result.HasMore = true
		rows = rows[:in.Limit]
		next := offset + in.Limit
		if next > 10000 {
			// Never claim history is complete when the deep-paging guard fires.
			result.PagingLimited = true
		} else {
			cursor := cursorData{Project: projectID, Item: itemID,
				Version: item.Version, Section: in.Section, Offset: next}
			if in.Section == Attempts {
				// Use the *last returned* real row. Newly ingested attempts
				// never shift older pages, even without a WorkItem touch.
				var last struct {
					SourceID   uuid.UUID `json:"source_id"`
					IngestedAt time.Time `json:"ingested_at"`
				}
				if err := json.Unmarshal(rows[len(rows)-1], &last); err != nil ||
					last.SourceID == uuid.Nil || last.IngestedAt.IsZero() {
					return Page{}, errors.New("invalid attempt cursor source record")
				}
				cursor.BeforeTime = &last.IngestedAt
				cursor.BeforeID = &last.SourceID
			}
			result.NextCursor = encodeCursor(cursor)
		}
	}
	result.Items = append(result.Items, rows...)
	return result, nil
}
