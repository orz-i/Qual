package httpapi

import (
	"net/http"
	"net/url"

	"qual/internal/work/application"
	"qual/internal/work/domain"
)

// Only a currently authorized project access:manage actor may read the
// impact preview or update policy. No UI hint or supplied flag is a grant.
func (a workAPI) taskAuthorizationPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		if r.URL.RawQuery != "" {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Use the separate policy preview endpoint")
			return
		}
		view, err := a.service.GetTaskAuthorizationPolicy(r.Context(), actor, pid)
		if err != nil {
			handleError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, view)
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Unexpected policy query")
		return
	}
	var change application.TaskAuthorizationPolicyChange
	if !readJSON(w, r, &change) {
		return
	}
	view, err := a.service.ChangeTaskAuthorizationPolicy(r.Context(), actor, pid, change, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}
func (a workAPI) taskAuthorizationPreview(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["mode"]) != 1 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Preview requires one mode=legacy|task_scoped")
		return
	}
	target := domain.TaskAuthorizationMode(query.Get("mode"))
	preview, err := a.service.PreviewTaskAuthorizationPolicy(r.Context(), actor, pid, target)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preview)
}
