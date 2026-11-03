package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type reportHarness struct {
	project         uuid.UUID
	version         uuid.UUID
	testCase        uuid.UUID
	workItem        uuid.UUID
	run             uuid.UUID
	item            uuid.UUID
	attempt         uuid.UUID
	gets            int
	posts           int
	runKeys         []string
	attemptKeys     []string
	coverageKeys    []string
	partial         bool
	badRun          bool
	workItemMissing bool
	coverageFail    bool
	covered         bool
}

func (h *reportHarness) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer fake-scoped-test-credential" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	workPath := "/api/v1/projects/" + h.project.String() + "/work-items/" + h.workItem.String()
	coverageReadPath := workPath + "/test-coverage"
	if r.Method == http.MethodGet {
		h.gets++
		switch r.URL.Path {
		case workPath:
			if h.workItemMissing {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"not found"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"project_id":%q,"status":"in_progress","version":1}`,
				h.workItem, h.project)
		case coverageReadPath:
			items := []any{}
			if h.covered {
				items = append(items, map[string]any{"id": h.testCase})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			panic("unexpected GET " + r.URL.Path)
		}
		return
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	h.posts++
	runPath := "/api/v1/projects/" + h.project.String() + "/test-runs"
	attemptPath := "/api/v1/projects/" + h.project.String() + "/test-run-items/" + h.item.String() + "/attempts"
	coveragePath := "/api/v1/projects/" + h.project.String() + "/test-cases/" + h.testCase.String() + "/coverage"
	switch r.URL.Path {
	case runPath:
		var body struct {
			Name           string      `json:"name"`
			SourceSystem   string      `json:"source_system"`
			CaseVersionIDs []uuid.UUID `json:"case_version_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		if body.Name != "actual external checks" || body.SourceSystem != "external-cli" ||
			len(body.CaseVersionIDs) != 1 || body.CaseVersionIDs[0] != h.version {
			panic("run request changed source or immutable case version")
		}
		h.runKeys = append(h.runKeys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusCreated)
		project := h.project
		if h.badRun {
			project = uuid.New()
		}
		fmt.Fprintf(w, `{"id":%q,"project_id":%q,"items":[{"id":%q,"test_case_id":%q,"case_version_id":%q}]}`,
			h.run, project, h.item, h.testCase, h.version)
	case attemptPath:
		var body struct {
			Status     string `json:"status"`
			Note       string `json:"note"`
			SourceKind string `json:"source_kind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		if body.Status != "passed" || body.Note != "Go test actually passed on host" || body.SourceKind != "external" {
			panic("client fabricated test status or source")
		}
		h.attemptKeys = append(h.attemptKeys, r.Header.Get("Idempotency-Key"))
		if h.partial {
			h.partial = false
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"TEMPORARY_UNAVAILABLE","message":"retry after inspection"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%q,"run_item_id":%q,"status":"passed","source_kind":"external","attempt_no":1}`,
			h.attempt, h.item)
	case coveragePath:
		var body struct {
			WorkItemID uuid.UUID `json:"work_item_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			panic(err)
		}
		if body.WorkItemID != h.workItem {
			panic("coverage was linked to the wrong WorkItem")
		}
		h.coverageKeys = append(h.coverageKeys, r.Header.Get("Idempotency-Key"))
		if h.coverageFail {
			h.coverageFail = false
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"TEMPORARY_UNAVAILABLE","message":"retry after inspection"}}`))
			return
		}
		if h.covered {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"DUPLICATE_TEST_RECORD","message":"already linked"}}`))
			return
		}
		h.covered = true
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%q,"project_id":%q}`, h.testCase, h.project)
	default:
		panic("unexpected POST " + r.URL.Path)
	}
}

func newReportHarness() reportHarness {
	return reportHarness{
		project: uuid.New(), version: uuid.New(), testCase: uuid.New(), workItem: uuid.New(),
		run: uuid.New(), item: uuid.New(), attempt: uuid.New(),
	}
}

func reportArgs(h reportHarness) testReportInput {
	return testReportInput{
		ProjectID: h.project.String(), CaseVersionID: h.version.String(),
		Title: "actual external checks", Status: "passed", Note: "Go test actually passed on host",
		Source: "external-cli", BuildRef: "abcdef1234567", Environment: "Mac PI",
		RequestKey: "qual-real-actual-test-123",
	}
}

func newReportClient(t *testing.T, h *reportHarness) *client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(server.Close)
	return &client{base: server.URL, bearer: "fake-scoped-test-credential", http: server.Client()}
}

func TestTestReportComposesExistingAuthorizedAPIsWithStableSeparatedKeys(t *testing.T) {
	h := newReportHarness()
	c := newReportClient(t, &h)
	args := reportArgs(h)
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if h.posts != 4 || len(h.runKeys) != 2 || len(h.attemptKeys) != 2 {
		t.Fatalf("expected exactly two calls per invocation, posts=%d keys=%v/%v", h.posts, h.runKeys, h.attemptKeys)
	}
	if h.runKeys[0] != h.runKeys[1] || h.attemptKeys[0] != h.attemptKeys[1] ||
		h.runKeys[0] == h.attemptKeys[0] || len(h.runKeys[0]) > 128 || len(h.attemptKeys[0]) > 128 ||
		len(h.runKeys[0]) < 8 || len(h.attemptKeys[0]) < 8 {
		t.Fatal("stage keys must be deterministic, distinct and backend-valid")
	}
	if strings.Contains(h.runKeys[0], args.RequestKey) || strings.Contains(h.attemptKeys[0], args.RequestKey) {
		t.Fatal("client must not forward arbitrary user key in stage key")
	}
}

func TestTestReportLinksCoverageAndReplayDoesNotDuplicate(t *testing.T) {
	h := newReportHarness()
	c := newReportClient(t, &h)
	args := reportArgs(h)
	args.WorkItemID = h.workItem.String()
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if !h.covered || h.posts != 6 || h.gets != 3 || len(h.coverageKeys) != 2 ||
		h.coverageKeys[0] != h.coverageKeys[1] {
		t.Fatalf("coverage/replay not stable: covered=%v posts=%d gets=%d keys=%v",
			h.covered, h.posts, h.gets, h.coverageKeys)
	}
}

func TestTestReportAcceptsExistingCoverageWithoutCreatingDuplicate(t *testing.T) {
	h := newReportHarness()
	h.covered = true
	c := newReportClient(t, &h)
	args := reportArgs(h)
	args.WorkItemID = h.workItem.String()
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if !h.covered || h.posts != 3 || h.gets != 2 || len(h.coverageKeys) != 1 {
		t.Fatalf("existing coverage should be verified, not duplicated: %+v", h)
	}
}

func TestTestReportCoveragePartialFailureCanResumeWithSameKey(t *testing.T) {
	h := newReportHarness()
	h.coverageFail = true
	c := newReportClient(t, &h)
	args := reportArgs(h)
	args.WorkItemID = h.workItem.String()
	err := runTestReport(context.Background(), c, args)
	if err == nil || !strings.Contains(err.Error(), "PARTIAL") ||
		!strings.Contains(err.Error(), h.run.String()) || !strings.Contains(err.Error(), h.attempt.String()) {
		t.Fatalf("coverage failure must report the two completed steps: %v", err)
	}
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if !h.covered || h.posts != 6 || len(h.coverageKeys) != 2 || h.coverageKeys[0] != h.coverageKeys[1] {
		t.Fatalf("coverage retry changed identity or duplicated state: %+v", h)
	}
}

func TestTestReportPreflightsWrongWorkItemBeforeAnyWrite(t *testing.T) {
	h := newReportHarness()
	h.workItemMissing = true
	c := newReportClient(t, &h)
	args := reportArgs(h)
	args.WorkItemID = h.workItem.String()
	err := runTestReport(context.Background(), c, args)
	if err == nil || !strings.Contains(err.Error(), "no TestRun created") || h.posts != 0 {
		t.Fatalf("unknown WorkItem should fail before writes: %v posts=%d", err, h.posts)
	}
}

func TestTestReportPartialRunFailureIsRetryableWithoutChangingInputs(t *testing.T) {
	h := newReportHarness()
	h.partial = true
	c := newReportClient(t, &h)
	args := reportArgs(h)
	err := runTestReport(context.Background(), c, args)
	if err == nil || !strings.Contains(err.Error(), "PARTIAL") || !strings.Contains(err.Error(), h.run.String()) ||
		!strings.Contains(err.Error(), "same -request-key") {
		t.Fatalf("partial run must be visible and retriable, got %v", err)
	}
	if err := runTestReport(context.Background(), c, args); err != nil {
		t.Fatal(err)
	}
	if h.posts != 4 || h.runKeys[0] != h.runKeys[1] || h.attemptKeys[0] != h.attemptKeys[1] {
		t.Fatal("partial recovery changed identities or keys")
	}
}

func TestTestReportRejectsCrossProjectResponseBeforeAttempt(t *testing.T) {
	h := newReportHarness()
	h.badRun = true
	c := newReportClient(t, &h)
	err := runTestReport(context.Background(), c, reportArgs(h))
	if err == nil || !strings.Contains(err.Error(), "attempt NOT submitted") || h.posts != 1 {
		t.Fatalf("mismatched run must fail before attempt: %v posts=%d", err, h.posts)
	}
}

func TestTestReportRejectsInvalidDataBeforeAnyWrite(t *testing.T) {
	h := newReportHarness()
	c := newReportClient(t, &h)
	base := reportArgs(h)
	bad := []struct {
		name   string
		mutate func(*testReportInput)
	}{
		{"version missing", func(i *testReportInput) { i.CaseVersionID = "" }},
		{"nil version", func(i *testReportInput) { i.CaseVersionID = uuid.Nil.String() }},
		{"no real note", func(i *testReportInput) { i.Note = "  " }},
		{"no stable key", func(i *testReportInput) { i.RequestKey = "" }},
		{"manual source", func(i *testReportInput) { i.Source = "manual" }},
		{"junit source", func(i *testReportInput) { i.Source = "junit" }},
		{"whitespace source", func(i *testReportInput) { i.Source = " external-ci " }},
		{"too short key", func(i *testReportInput) { i.RequestKey = "abc" }},
		{"ambiguous status", func(i *testReportInput) { i.Status = "success" }},
		{"title missing", func(i *testReportInput) { i.Title = "" }},
		{"bad project", func(i *testReportInput) { i.ProjectID = uuid.Nil.String() }},
		{"bad WorkItem", func(i *testReportInput) { i.WorkItemID = "not-a-uuid" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.mutate(&v)
			if err := runTestReport(context.Background(), c, v); err == nil {
				t.Fatal("accepted ambiguous report without complete actual evidence")
			}
		})
	}
	if h.posts != 0 || h.gets != 0 {
		t.Fatalf("invalid reports caused backend requests: posts=%d gets=%d", h.posts, h.gets)
	}
}
