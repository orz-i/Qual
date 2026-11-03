package httpapi

import (
	"net/http"

	"github.com/google/uuid"
)

func projectAndItem(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	item, ok := pathID(w, r, "itemID")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	return project, item, true
}

func (a workAPI) addComment(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Body            string `json:"body"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	item, err := a.service.AddComment(r.Context(), actor, projectID, itemID, body.Body, body.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusCreated, item)
}

func (a workAPI) comments(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	items, err := a.service.Comments(r.Context(), actor, projectID, itemID)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a workAPI) linkDependency(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		ToItemID        uuid.UUID `json:"to_item_id"`
		ExpectedVersion int64     `json:"expected_version"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	item, err := a.service.LinkDependency(r.Context(), actor, projectID, itemID, body.ToItemID, body.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusCreated, item)
}

func (a workAPI) dependencies(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	items, err := a.service.Dependencies(r.Context(), actor, projectID, itemID)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
