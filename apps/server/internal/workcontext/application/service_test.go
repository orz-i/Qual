package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	identity "qual/internal/identity/application"
	permission "qual/internal/identity/domain"
	work "qual/internal/work/application"
	domain "qual/internal/work/domain"
)

type fakeAuth struct {
	project        uuid.UUID
	denied         map[permission.Permission]bool
	authorizations []permission.Permission
}

func (a *fakeAuth) Authenticate(context.Context, string) (identity.Actor, error) {
	return identity.Actor{}, identity.ErrUnauthenticated
}

func (a *fakeAuth) AuthorizeProject(_ context.Context, _ identity.Actor, project uuid.UUID, p permission.Permission) (identity.Project, error) {
	a.authorizations = append(a.authorizations, p)
	if project != a.project {
		return identity.Project{}, identity.ErrNotFound
	}
	if a.denied[p] {
		return identity.Project{}, identity.ErrForbidden
	}
	return identity.Project{ID: project}, nil
}

type fakeWork struct {
	item            domain.WorkItem
	history         work.AcceptanceHistory
	acceptanceCalls int
}

func (w *fakeWork) Get(_ context.Context, _ identity.Actor, project, item uuid.UUID) (domain.WorkItem, error) {
	if w.item.ProjectID != project || w.item.ID != item {
		return domain.WorkItem{}, work.ErrNotFound
	}
	return w.item, nil
}
func (w *fakeWork) ReadAcceptanceHistory(context.Context, identity.Actor, uuid.UUID, uuid.UUID) (work.AcceptanceHistory, error) {
	w.acceptanceCalls++
	return w.history, nil
}

func (r *fakePages) TaskMode(context.Context, uuid.UUID) (domain.TaskAuthorizationMode, error) {
	return domain.LegacyAuthorization, nil
}
func (r *fakePages) AuthorizeTaskContextRead(context.Context, identity.Actor, identity.Project, uuid.UUID, Section) error {
	return nil
}

type fakePages struct {
	pages map[Section][]json.RawMessage
	calls []Section
}

func (r *fakePages) Page(_ context.Context, _, _ uuid.UUID, s Section, q PageQuery) ([]json.RawMessage, error) {
	r.calls = append(r.calls, s)
	rows := r.pages[s]
	if q.BeforeTime != nil && s == Attempts {
		filtered := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			var fact struct {
				SourceID   uuid.UUID `json:"source_id"`
				IngestedAt time.Time `json:"ingested_at"`
			}
			if err := json.Unmarshal(row, &fact); err != nil {
				return nil, err
			}
			if fact.IngestedAt.Before(*q.BeforeTime) || fact.IngestedAt.Equal(*q.BeforeTime) && fact.SourceID.String() < q.BeforeID.String() {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	} else {
		if q.Offset >= len(rows) {
			return []json.RawMessage{}, nil
		}
		rows = rows[q.Offset:]
	}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func sampleService() (Service, *fakeAuth, *fakeWork, *fakePages, uuid.UUID, uuid.UUID) {
	p, i := uuid.New(), uuid.New()
	a := &fakeAuth{project: p, denied: map[permission.Permission]bool{}}
	w := &fakeWork{item: domain.WorkItem{ID: i, ProjectID: p,
		Key: "QUAL-27", Title: "Recover real facts", Kind: domain.Task,
		Status: domain.InProgress, Priority: domain.High, Version: 5}}
	r := &fakePages{pages: map[Section][]json.RawMessage{}}
	return Service{Identity: a, Work: w, Reader: r,
		Clock: func() time.Time { return time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) }}, a, w, r, p, i
}

func TestTaskContextCarriesAuthoritativeExecutableDescriptionAndAcceptance(t *testing.T) {
	s, _, w, _, projectID, itemID := sampleService()
	w.item.Description = "用户入口：项目→任务。执行：qual show -item QUAL-67；验收：拒绝403，刷新后持久化。"
	w.item.AcceptanceCriteria = []string{"中文用户路径和权限检查必须可观察", "每次实际测试引用不可变证据，不推断 AgentRun 通过"}
	for _, section := range []Section{Overview, Comments} {
		page, err := s.Get(context.Background(), identity.Actor{}, projectID, itemID, Input{Section: section})
		if err != nil {
			t.Fatal(err)
		}
		if page.SchemaVersion != 2 || page.ContextRevision != 5 || page.WorkItem.Description != w.item.Description ||
			len(page.WorkItem.AcceptanceCriteria) != 2 || page.WorkItem.AcceptanceCriteria[0] != w.item.AcceptanceCriteria[0] {
			t.Fatalf("Task contract lost in %s: %+v", section, page.WorkItem)
		}
		page.WorkItem.AcceptanceCriteria[0] = "mutated local copy"
		if w.item.AcceptanceCriteria[0] != "中文用户路径和权限检查必须可观察" {
			t.Fatal("Summary aliases authoritative WorkItem acceptance slice")
		}
	}
	w.item.AcceptanceCriteria = nil
	page, err := s.Get(context.Background(), identity.Actor{}, projectID, itemID, Input{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(page.WorkItem)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(payload, &decoded) != nil || decoded["description"] != w.item.Description {
		t.Fatal("lost source fields")
	}
	if values, ok := decoded["acceptance_criteria"].([]any); !ok || len(values) != 0 {
		t.Fatalf("expected [] acceptance_criteria, got %v", decoded["acceptance_criteria"])
	}
}

func TestOverviewIsBoundedAndNoAutomaticApproval(t *testing.T) {
	s, _, w, r, p, i := sampleService()
	r.pages[Reviews] = []json.RawMessage{json.RawMessage(`{"source":"work_role_entries","source_id":"9e108fc9-74c9-4090-8709-93cb1a16b69d","outcome":"needs_work","self_review":true}`)}
	v, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if v.SchemaVersion != 2 || v.ContextRevision != 5 || v.WorkItem.Key != "QUAL-27" ||
		v.WorkItem.Version != 5 || v.Consistency != "best_effort" || v.Section != Overview ||
		len(v.Items) != 0 || len(v.AvailableSections) != 8 || v.HasMore ||
		v.Acceptance != nil || w.acceptanceCalls != 0 {
		t.Fatalf("unexpected overview %+v", v)
	}
	var review map[string]any
	if err := json.Unmarshal(v.LatestReview, &review); err != nil || review["outcome"] != "needs_work" {
		t.Fatalf("review history lost: %v / %v", review, err)
	}
	if len(r.calls) != 1 || r.calls[0] != Reviews {
		t.Fatalf("overview loaded forbidden/unbounded domains %v", r.calls)
	}
}

func TestPagedAttemptsPreserveEarlierFailuresAndBindVersion(t *testing.T) {
	s, _, w, r, p, i := sampleService()
	r.pages[Attempts] = []json.RawMessage{
		json.RawMessage(`{"source":"test_attempts","source_id":"9e108fc9-74c9-4090-8709-93cb1a16b69d","status":"passed","ingested_at":"2026-11-03T03:00:00Z"}`),
		json.RawMessage(`{"source":"test_attempts","source_id":"08e4e919-4058-4a59-99a2-6c227a0ab285","status":"failed","ingested_at":"2026-11-03T02:00:00Z"}`),
		json.RawMessage(`{"source":"test_attempts","source_id":"c07cac25-1c61-4599-88a7-ac2592e4405f","status":"blocked","ingested_at":"2026-11-03T01:00:00Z"}`),
	}
	v, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Limit: 2})
	if err != nil || !v.HasMore || v.NextCursor == "" || len(v.Items) != 2 {
		t.Fatalf("first page %v: %+v", err, v)
	}
	if string(v.Items[1]) != string(r.pages[Attempts][1]) {
		t.Fatalf("old Failed Attempt was discarded: %s", v.Items[1])
	}
	next, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Limit: 2, Cursor: v.NextCursor})
	if err != nil || next.HasMore || next.NextCursor != "" || len(next.Items) != 1 ||
		string(next.Items[0]) != string(r.pages[Attempts][2]) {
		t.Fatalf("second page %v: %+v", err, next)
	}
	w.item.Version++
	if _, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Limit: 2, Cursor: v.NextCursor}); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("expected 409 after WorkItem change, got %v", err)
	}
}

func TestSectionPermissionsAndCrossProjectNeverQueryRecords(t *testing.T) {
	for _, test := range []struct {
		section    Section
		permission permission.Permission
	}{
		{Attempts, permission.TestRead}, {Coverage, permission.TestRead}, {Artifacts, permission.ArtifactRead},
	} {
		t.Run(string(test.section), func(t *testing.T) {
			s, a, _, r, p, i := sampleService()
			a.denied[test.permission] = true
			if _, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: test.section}); !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("expected permission denial, got %v", err)
			}
			if len(r.calls) != 0 {
				t.Fatalf("read happened after denial: %v", r.calls)
			}
			if _, err := s.Get(context.Background(), identity.Actor{}, uuid.New(), i, Input{Section: test.section}); !errors.Is(err, work.ErrNotFound) {
				t.Fatalf("cross-project WorkItem existence leak: %v", err)
			}
			if len(r.calls) != 0 {
				t.Fatalf("cross-project read happened: %v", r.calls)
			}
		})
	}
}

func TestInvalidQueryAndCursorAreRejected(t *testing.T) {
	s, _, _, r, p, i := sampleService()
	for _, q := range []Input{{Section: "audit"}, {Section: Attempts, Limit: -1},
		{Section: Attempts, Limit: 101}, {Section: Overview, Cursor: "anything"},
		{Section: Attempts, Cursor: "totally-invalid"}} {
		if _, err := s.Get(context.Background(), identity.Actor{}, p, i, q); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("query %v: %v", q, err)
		}
	}
	valid := encodeCursor(cursorData{Project: p, Item: i, Section: Reviews, Version: 5, Offset: 1})
	if _, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Cursor: valid}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("cross-section cursor accepted: %v", err)
	}
	if _, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Reviews, Cursor: encodeCursor(cursorData{
		Project: uuid.New(), Item: i, Section: Reviews, Version: 5, Offset: 1})}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("cross-project cursor accepted: %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("invalid query reached repo: %v", r.calls)
	}
}

func TestPagingCeilingReportsIncompleteHistory(t *testing.T) {
	s, _, _, r, p, i := sampleService()
	row := json.RawMessage(`{"source":"test_attempts","source_id":"9e108fc9-74c9-4090-8709-93cb1a16b69d","status":"failed"}`)
	r.pages[Comments] = make([]json.RawMessage, 10003)
	for j := range r.pages[Comments] {
		r.pages[Comments][j] = row
	}
	cursor := encodeCursor(cursorData{Project: p, Item: i, Section: Comments, Version: 5, Offset: 10000})
	v, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Comments, Limit: 2, Cursor: cursor})
	if err != nil || len(v.Items) != 2 || !v.HasMore || !v.PagingLimited || v.NextCursor != "" {
		t.Fatalf("deep pagination incorrectly claimed full result: %v %+v", err, v)
	}
}

func TestAttemptKeysetIgnoresNewerIndependentlyIngestedFacts(t *testing.T) {
	s, _, _, r, p, i := sampleService()
	r.pages[Attempts] = []json.RawMessage{
		json.RawMessage(`{"source":"test_attempts","source_id":"9e108fc9-74c9-4090-8709-93cb1a16b69d","status":"passed","ingested_at":"2026-11-03T03:00:00Z"}`),
		json.RawMessage(`{"source":"test_attempts","source_id":"08e4e919-4058-4a59-99a2-6c227a0ab285","status":"failed","ingested_at":"2026-11-03T02:00:00Z"}`),
		json.RawMessage(`{"source":"test_attempts","source_id":"c07cac25-1c61-4599-88a7-ac2592e4405f","status":"blocked","ingested_at":"2026-11-03T01:00:00Z"}`),
	}
	first, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first: %v %+v", err, first)
	}
	r.pages[Attempts] = append([]json.RawMessage{json.RawMessage(`{"source":"test_attempts","source_id":"c4faee10-a822-40a4-8311-07409a40c19d","status":"passed","ingested_at":"2026-11-03T04:00:00Z"}`)}, r.pages[Attempts]...)
	second, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0][0] != '{' ||
		string(second.Items[0]) != string(r.pages[Attempts][2]) {
		t.Fatalf("independent insert shifted keyset page: %v %+v", err, second)
	}
	// Legacy offset-only cursors must fail closed; do not silently skip facts.
	legacy := encodeCursor(cursorData{Project: p, Item: i, Section: Attempts, Version: 5, Offset: 1})
	if _, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Attempts, Cursor: legacy}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("legacy attempt offset accepted: %v", err)
	}
}

func TestLegacyDoneNeverBecomesIndependentAcceptance(t *testing.T) {
	s, _, w, r, p, i := sampleService()
	w.item.Status = domain.Done
	w.history = work.AcceptanceHistory{WorkItemID: i, WorkStatus: domain.Done,
		WorkVersion: 9, AcceptanceMode: "legacy_unclassified"}
	v, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{})
	if err != nil || v.Acceptance == nil || v.Acceptance.AcceptanceMode != "legacy_unclassified" ||
		v.Acceptance.Decision != nil {
		t.Fatalf("legacy Done got invented approval: %v %+v", err, v)
	}
	page, err := s.Get(context.Background(), identity.Actor{}, p, i, Input{Section: Acceptance})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("acceptance page: %v %+v", err, page)
	}
	if len(r.calls) != 1 || r.calls[0] != Reviews {
		t.Fatalf("acceptance page should reuse canonical acceptance service: %v", r.calls)
	}
}
