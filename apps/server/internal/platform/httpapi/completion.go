package httpapi

import (
	"net/http"

	"qual/internal/work/domain"
)

// complete accepts a single explicit, authenticated review decision.
// Actor, CredentialID, independence and project policy are server-derived,
// never accepted from arbitrary client JSON fields.
func (a workAPI) complete(w http.ResponseWriter, r *http.Request) {
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
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var decision domain.DoneDecision
	if !readJSON(w, r, &decision) {
		return
	}
	item, err := a.service.Complete(r.Context(), actor, projectID, itemID, decision, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}
