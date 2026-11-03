package httpapi

import (
	"net/http"
	"strconv"

	"qual/internal/work/application"
)

func (a workAPI) reviewQueue(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := application.ReviewQueueFilter{Limit: 50}
	if raw, exists := q["limit"]; exists {
		if len(raw) != 1 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "One limit parameter required")
			return
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 100 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "limit must be 1..100")
			return
		}
		filter.Limit = n
	}
	if raw, exists := q["offset"]; exists {
		if len(raw) != 1 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "One offset parameter required")
			return
		}
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 0 || n > 10000 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "offset must be 0..10000")
			return
		}
		filter.Offset = n
	}
	result, err := a.service.ReviewQueue(r.Context(), actor, pid, filter)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
