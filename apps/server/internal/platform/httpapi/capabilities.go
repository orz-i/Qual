package httpapi

import "net/http"

// A policy projection is a read-only hint for the *authenticated* Principal.
// It never grants a permission, reveals a credential, or substitutes for the
// WorkItem row lock and fresh authorization on mutation.
func (a workAPI) capabilities(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	pid, id, ok := projectAndItem(w, r)
	if !ok {
		return
	}
	caps, err := a.service.Capabilities(r.Context(), actor, pid, id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, caps)
}
