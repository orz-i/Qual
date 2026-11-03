package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"qual/internal/evidence/domain"
)

// Thread endpoints are transport adapters only. All business writes and authorization
// are in the evidence application service and its transactional PostgreSQL adapter.
func threadPage(w http.ResponseWriter, r *http.Request) (int, uuid.UUID, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, r, 400, "VALIDATION_ERROR", "limit must be 1..100")
			return 0, uuid.Nil, false
		}
		limit = parsed
	}
	cursor := uuid.Nil
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		value, err := uuid.Parse(raw)
		if err != nil || value == uuid.Nil {
			writeError(w, r, 400, "VALIDATION_ERROR", "cursor must be a link UUID")
			return 0, uuid.Nil, false
		}
		cursor = value
	}
	return limit, cursor, true
}

func (e evidenceAPI) itemThreads(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		limit, cursor, valid := threadPage(w, r)
		if !valid {
			return
		}
		page, err := e.service.WorkThreads(r.Context(), actor, projectID, itemID, limit, cursor)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, page)
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	var input domain.ThreadLinkInput
	if !readJSON(w, r, &input) {
		return
	}
	result, err := e.service.LinkThread(r.Context(), actor, projectID, itemID, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", result.WorkItemVersion))
	writeJSON(w, 201, result)
}

func (e evidenceAPI) unlinkThread(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	projectID, itemID, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	threadID, ok := pathID(w, r, "threadID")
	if !ok {
		return
	}
	meta, ok := evidenceMeta(w, r)
	if !ok {
		return
	}
	version, ok := getVersion(w, r)
	if !ok {
		return
	}
	result, err := e.service.UnlinkThread(r.Context(), actor, projectID, itemID, threadID, version, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", result.WorkItemVersion))
	writeJSON(w, 200, result)
}

func (e evidenceAPI) threadItems(w http.ResponseWriter, r *http.Request) {
	actor, ok := e.work.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	threadID, ok := pathID(w, r, "threadID")
	if !ok {
		return
	}
	limit, cursor, ok := threadPage(w, r)
	if !ok {
		return
	}
	page, err := e.service.ThreadWorkItems(r.Context(), actor, projectID, threadID, limit, cursor)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, page)
}
