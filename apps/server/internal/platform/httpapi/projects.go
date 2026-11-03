package httpapi

import "net/http"

func (a workAPI) projects(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projects, err := a.session.repo.Projects(r.Context(), actor)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": projects})
}
