package httpapi

import (
	"net/http"

	"qual/internal/work/domain"
)

// GET and POST share the existing credential/Project/CSRF framework.
// The client cannot nominate a different Principal, Credential or self_review.
func (a workAPI) roleEntries(w http.ResponseWriter, r *http.Request) {
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
	if r.Method == http.MethodGet {
		list, err := a.service.ListRoles(r.Context(), actor, project, item)
		if err != nil {
			handleError(w, r, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": list})
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var input domain.RoleEntryInput
	if !readJSON(w, r, &input) {
		return
	}
	entry, err := a.service.RecordRole(r.Context(), actor, project, item, input, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}
