package httpapi

import (
	"net/http"

	"github.com/google/uuid"
)

// Release/Reassign are WorkItem lifecycle writes, not process operations.
// Both use the real authenticated Principal and idempotent work mutation.
func (a workAPI) release(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input versionRequest
	if !readJSON(w, r, &input) {
		return
	}
	item, err := a.service.Release(r.Context(), actor, projectID, itemID, input.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}

func (a workAPI) reassign(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedVersion int64     `json:"expected_version"`
		NewAssigneeID   uuid.UUID `json:"new_assignee_id"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	item, err := a.service.Reassign(r.Context(), actor, projectID, itemID, input.NewAssigneeID, input.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}
