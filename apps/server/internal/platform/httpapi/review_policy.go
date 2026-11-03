package httpapi

import (
	"net/http"

	"qual/internal/work/application"
)

// reviewPolicy exposes project-wide review governance. Read is work:read;
// changing it requires effective access:manage, CAS, audit and idempotency.
// Loosening Owner/self-acceptance additionally requires Human Workspace Admin.
func (a workAPI) reviewPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		value, err := a.service.GetProjectReviewPolicy(r.Context(), actor, projectID)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input application.ProjectReviewPolicyChange
	if !readJSON(w, r, &input) {
		return
	}
	value, err := a.service.ChangeProjectReviewPolicy(r.Context(), actor, projectID, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}
