package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	eventapp "qual/internal/externalevent/application"
	eventdomain "qual/internal/externalevent/domain"
)

type eventAPI struct {
	work    workAPI
	service *eventapp.Service
}

func (a eventAPI) register(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input eventdomain.Input
	if !readJSON(w, r, &input) {
		return
	}
	result, err := a.service.Record(r.Context(), actor, project, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/external-events/%s", project, result.ID))
	writeJSON(w, http.StatusCreated, result)
}

func eventPageQuery(w http.ResponseWriter, r *http.Request) (int, uuid.UUID, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Invalid Event timeline query")
		return 0, uuid.Nil, false
	}
	for k, vs := range query {
		if (k != "limit" && k != "cursor") || len(vs) != 1 {
			writeError(w, r, 400, "VALIDATION_ERROR", "Invalid Event timeline query")
			return 0, uuid.Nil, false
		}
	}
	limit := 30
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, r, 400, "VALIDATION_ERROR", "limit must be 1..100")
			return 0, uuid.Nil, false
		}
		limit = n
	}
	cursor := uuid.Nil
	if raw := query.Get("cursor"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			writeError(w, r, 400, "VALIDATION_ERROR", "cursor must be an existing scoped event UUID")
			return 0, uuid.Nil, false
		}
		cursor = id
	}
	return limit, cursor, true
}

func (a eventAPI) timeline(w http.ResponseWriter, r *http.Request, byThread bool) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	p, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	key := "itemID"
	if byThread {
		key = "threadID"
	}
	target, ok := pathID(w, r, key)
	if !ok {
		return
	}
	limit, cursor, ok := eventPageQuery(w, r)
	if !ok {
		return
	}
	page, err := a.service.List(r.Context(), actor, p, target, byThread, limit, cursor)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}
func (a eventAPI) workTimeline(w http.ResponseWriter, r *http.Request)   { a.timeline(w, r, false) }
func (a eventAPI) threadTimeline(w http.ResponseWriter, r *http.Request) { a.timeline(w, r, true) }
