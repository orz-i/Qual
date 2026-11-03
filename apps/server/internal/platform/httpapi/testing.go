package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"qual/internal/testing/application"
	"qual/internal/testing/domain"
)

type testingAPI struct {
	work  workAPI
	tests application.Service
}

func testingMeta(w http.ResponseWriter, r *http.Request) (application.Meta, bool) {
	meta, ok := requireIdempotency(w, r)
	return application.Meta{RequestID: meta.RequestID, IdempotencyKey: meta.IdempotencyKey}, ok
}
func (t testingAPI) suites(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := t.tests.ListSuites(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	s, err := t.tests.CreateSuite(r.Context(), actor, pid, input.Name, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, s)
}
func (t testingAPI) cases(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := t.tests.ListCases(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input application.CaseInput
	if !readJSON(w, r, &input) {
		return
	}
	c, err := t.tests.CreateCase(r.Context(), actor, pid, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, c)
}
func (t testingAPI) caseDetail(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	cid, ok := pathID(w, r, "caseID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		c, err := t.tests.GetCase(r.Context(), actor, pid, cid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", c.Revision))
		writeJSON(w, 200, c)
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	revision, ok := getVersion(w, r)
	if !ok {
		return
	}
	var input application.CaseInput
	if !readJSON(w, r, &input) {
		return
	}
	c, err := t.tests.UpdateCase(r.Context(), actor, pid, cid, application.EditCaseInput{
		Specification: input.Specification, SuiteID: input.SuiteID, ExpectedRevision: revision}, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", c.Revision))
	writeJSON(w, 200, c)
}
func (t testingAPI) versions(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	cid, ok := pathID(w, r, "caseID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := t.tests.CaseVersions(r.Context(), actor, pid, cid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input application.PublishInput
	if !readJSON(w, r, &input) {
		return
	}
	v, err := t.tests.PublishCase(r.Context(), actor, pid, cid, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, v)
}
func (t testingAPI) coverage(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	cid, ok := pathID(w, r, "caseID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		records, err := t.tests.Coverage(r.Context(), actor, pid, cid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": records})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input application.LinkCoverageInput
	if !readJSON(w, r, &input) {
		return
	}
	c, err := t.tests.LinkCoverage(r.Context(), actor, pid, cid, input.WorkItemID, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, c)
}
func (t testingAPI) runs(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := t.tests.ListRuns(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input domain.RunInput
	if !readJSON(w, r, &input) {
		return
	}
	run, err := t.tests.CreateRun(r.Context(), actor, pid, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, run)
}
func (t testingAPI) runDetail(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	rid, ok := pathID(w, r, "runID")
	if !ok {
		return
	}
	run, err := t.tests.GetRun(r.Context(), actor, pid, rid)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, run)
}
func (t testingAPI) attempts(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := t.tests.Attempts(r.Context(), actor, pid, id)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": rows})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input domain.AttemptInput
	if !readJSON(w, r, &input) {
		return
	}
	a, err := t.tests.RecordAttempt(r.Context(), actor, pid, id, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, a)
}
func (t testingAPI) defect(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	aid, ok := pathID(w, r, "attemptID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		links, err := t.tests.AttemptDefects(r.Context(), actor, pid, aid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": links})
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	var input domain.BugLinkInput
	if !readJSON(w, r, &input) {
		return
	}
	link, err := t.tests.LinkDefect(r.Context(), actor, pid, aid, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, link)
}
func (t testingAPI) importJUnit(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	rid, ok := pathID(w, r, "runID")
	if !ok {
		return
	}
	meta, ok := testingMeta(w, r)
	if !ok {
		return
	}
	if r.Header.Get("Content-Type") != "application/xml" && r.Header.Get("Content-Type") != "text/xml" {
		writeError(w, r, 415, "UNSUPPORTED_MEDIA_TYPE", "JUnit XML content type required")
		return
	}
	defer r.Body.Close()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, domain.MaxJUnitXMLBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, r, 413, "PAYLOAD_TOO_LARGE", "JUnit report must not exceed 1 MiB")
		} else {
			writeError(w, r, 400, "VALIDATION_ERROR", "Invalid XML body")
		}
		return
	}
	filename := strings.TrimSpace(r.Header.Get("X-Report-Filename"))
	summary, err := t.tests.ImportJUnit(r.Context(), actor, pid, rid, application.JUnitInput{Filename: filename, Raw: raw}, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 201, summary)
}
