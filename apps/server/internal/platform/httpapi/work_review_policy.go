package httpapi

import (
	"net/http"
	"qual/internal/work/application"
)

// workReviewPolicy uses real project authorization: readable by work:read,
// mutable only with access:manage, CAS and idempotency. The server enforces
// Human Workspace Admin for any Owner self-review policy loosening.
func (a workAPI) workReviewPolicy(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		view, err := a.service.GetWorkReviewPolicy(r.Context(), actor, projectID, itemID)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input application.WorkReviewPolicyChange
	if !readJSON(w, r, &input) {
		return
	}
	item, err := a.service.ChangeWorkReviewPolicy(r.Context(), actor, projectID, itemID, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}
