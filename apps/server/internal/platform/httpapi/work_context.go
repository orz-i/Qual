package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	contextapp "qual/internal/workcontext/application"
)

type contextAPI struct {
	work workAPI
	read *contextapp.Service
}

func (a contextAPI) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
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

	// ParseQuery exposes malformed escaping instead of silently discarding
	// invalid values. Reject duplicates/unknown parameters for stable cursors.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid work context query")
		return
	}
	for key, values := range query {
		if key != "section" && key != "limit" && key != "cursor" || len(values) != 1 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid work context query")
			return
		}
	}
	input := contextapp.Input{Section: contextapp.Section(query.Get("section")), Cursor: query.Get("cursor")}
	if value := query.Get("limit"); value != "" {
		input.Limit, err = strconv.Atoi(value)
		if err != nil || input.Limit < 1 || input.Limit > 100 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "limit must be 1..100")
			return
		}
	}
	result, err := a.read.Get(r.Context(), actor, project, item, input)
	if err != nil {
		switch {
		case errors.Is(err, contextapp.ErrInvalidQuery):
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid work context section or cursor")
		case errors.Is(err, contextapp.ErrStaleCursor):
			writeError(w, r, http.StatusConflict, "CONTEXT_CHANGED", "Work item changed; restart context pagination")
		case errors.Is(err, contextapp.ErrScanBudget):
			// No partial result is returned when we cannot establish has_more.
			writeError(w, r, http.StatusServiceUnavailable, "CONTEXT_QUERY_BUDGET", "Context history exceeds bounded read budget; use an authorized narrower source query")
		default:
			handleError(w, r, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
