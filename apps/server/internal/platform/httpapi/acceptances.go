package httpapi

import (
	"net/http"
)

// acceptances is a read-only verified acceptance projection: legacy Done
// remains legacy_unclassified rather than receiving a fabricated review.
func (a workAPI) acceptances(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	item, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	history, err := a.service.ReadAcceptanceHistory(r.Context(), actor, project, item)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}
