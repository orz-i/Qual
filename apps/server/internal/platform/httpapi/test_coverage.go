package httpapi

import "net/http"

func (t testingAPI) workCoverage(w http.ResponseWriter, r *http.Request) {
	actor, ok := t.work.getActor(w, r)
	if !ok {
		return
	}
	pid, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	wid, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	records, err := t.tests.CoveredCases(r.Context(), actor, pid, wid)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": records})
}
