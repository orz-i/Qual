package httpapi

import (
	"net/http"
	"strconv"

	"qual/internal/work/application"
)

// Read-only attention projection: neither Audit access nor an approval API.
func (a workAPI) attentionInbox(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	f := application.AttentionFilter{Limit: 30}
	for key, values := range r.URL.Query() {
		if (key != "limit" && key != "offset") || len(values) != 1 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid attention inbox query")
			return
		}
		n, err := strconv.Atoi(values[0])
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid attention pagination")
			return
		}
		switch key {
		case "limit":
			if n < 1 || n > 50 {
				writeError(w, r, 400, "VALIDATION_ERROR", "limit must be 1..50")
				return
			}
			f.Limit = n
		case "offset":
			if n < 0 || n > 450 {
				writeError(w, r, 400, "VALIDATION_ERROR", "offset must be 0..450")
				return
			}
			f.Offset = n
		}
	}
	result, err := a.service.AttentionInbox(r.Context(), actor, project, f)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}
