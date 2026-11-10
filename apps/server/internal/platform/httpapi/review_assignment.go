package httpapi

import (
	"net/http"

	"qual/internal/work/domain"
)

// Review claiming and release are distinct from work execution assignment.
// Only authenticated review candidates can request them; the server computes
// all active policy, history and relation facts in the locked transaction.
func (a workAPI) reviewClaim(w http.ResponseWriter, r *http.Request) {
	a.reviewAssignmentChange(w, r, true)
}

func (a workAPI) reviewRelease(w http.ResponseWriter, r *http.Request) {
	a.reviewAssignmentChange(w, r, false)
}

func (a workAPI) reviewAssignmentChange(w http.ResponseWriter, r *http.Request, claim bool) {
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
	var input versionRequest
	if !readJSON(w, r, &input) {
		return
	}
	var item domain.WorkItem
	var err error
	if claim {
		item, err = a.service.ClaimReview(r.Context(), actor, projectID, itemID, input.ExpectedVersion, meta)
	} else {
		item, err = a.service.ReleaseReview(r.Context(), actor, projectID, itemID, input.ExpectedVersion, meta)
	}
	if err != nil {
		handleError(w, r, err)
		return
	}
	// Successful items use the same WorkItem response body as Claim/Release.
	respondItem(w, r, http.StatusOK, item)
}
