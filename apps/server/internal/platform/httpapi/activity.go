package httpapi

import (
	"net/http"
	"strconv"
)

func boundedLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 50, true
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 100 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "limit must be 1..100")
		return 0, false
	}
	return n, true
}

func (a workAPI) activity(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r)
	if !ok {
		return
	}
	activity, err := a.service.Activity(r.Context(), actor, projectID, limit)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": activity})
}

func (a workAPI) audit(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r)
	if !ok {
		return
	}
	audit, err := a.service.Audit(r.Context(), actor, projectID, limit)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": audit})
}
