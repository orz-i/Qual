package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	runapp "qual/internal/agentrun/application"
	rundomain "qual/internal/agentrun/domain"
)

type agentRunAPI struct {
	work    workAPI
	service runapp.Service
}

func (a agentRunAPI) register(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input rundomain.Registration
	if !readJSON(w, r, &input) {
		return
	}
	run, err := a.service.Register(r.Context(), actor, pid, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/agent-runs/%s", pid, run.ID))
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", run.Version))
	writeJSON(w, http.StatusCreated, run)
}
func (a agentRunAPI) report(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	runID, ok := pathID(w, r, "runID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	version, ok := getVersion(w, r)
	if !ok {
		return
	}
	var input rundomain.ReportInput
	if !readJSON(w, r, &input) {
		return
	}
	saved, err := a.service.Report(r.Context(), actor, pid, runID, input, version, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", saved.Run.Version))
	writeJSON(w, http.StatusCreated, saved)
}
func (a agentRunAPI) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "runID")
	if !ok {
		return
	}
	run, err := a.service.Get(r.Context(), actor, pid, id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", run.Version))
	writeJSON(w, 200, run)
}
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, r, 400, "VALIDATION_ERROR", "limit must be 1..100")
			return 0, false
		}
		return n, true
	}
	return 30, true
}
func (a agentRunAPI) byWorkItem(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	wid, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 10000 {
			writeError(w, r, 400, "VALIDATION_ERROR", "offset must be 0..10000")
			return
		}
		offset = n
	}
	list, err := a.service.ListForWorkItem(r.Context(), actor, pid, wid, limit, offset)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}
func (a agentRunAPI) reports(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	id, ok := pathID(w, r, "runID")
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			writeError(w, r, 400, "VALIDATION_ERROR", "before must be a positive sequence")
			return
		}
		before = n
	}
	list, err := a.service.ListReports(r.Context(), actor, pid, id, before, limit)
	if err != nil {
		handleError(w, r, err)
		return
	}
	var next any
	if len(list) == limit && len(list) > 0 {
		next = list[len(list)-1].Sequence
	}
	writeJSON(w, 200, map[string]any{"items": list, "next_before": next})
}
