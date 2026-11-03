package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"qual/internal/identity/application"
	"qual/internal/identity/domain"
)

type accessAPI struct {
	work   workAPI
	access application.AccessService
}

func (a accessAPI) self(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	info, err := a.access.Self(r.Context(), actor, project)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, info)
}
func (a accessAPI) principals(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	raw, present := r.URL.Query()["ids"]
	if !present || len(raw) != 1 || len(raw[0]) > 1849 {
		writeError(w, r, 400, "VALIDATION_ERROR", "Provide 1..50 Principal UUIDs")
		return
	}
	stringsIDs := strings.Split(raw[0], ",")
	if len(stringsIDs) < 1 || len(stringsIDs) > 50 {
		writeError(w, r, 400, "VALIDATION_ERROR", "Provide 1..50 Principal UUIDs")
		return
	}
	ids := make([]uuid.UUID, 0, len(stringsIDs))
	for _, value := range stringsIDs {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			writeError(w, r, 400, "VALIDATION_ERROR", "Invalid Principal UUID")
			return
		}
		ids = append(ids, id)
	}
	members, err := a.access.Principals(r.Context(), actor, project, ids)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": members})
}

func (a accessAPI) members(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	items, err := a.access.List(r.Context(), actor, project)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (a accessAPI) change(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.work.getActor(w, r)
	if !ok {
		return
	}
	project, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	principal, ok := pathID(w, r, "principalID")
	if !ok {
		return
	}
	permission := domain.Permission(r.PathValue("permission"))
	if !domain.ValidPermission(permission) {
		writeError(w, r, 400, "VALIDATION_ERROR", "Unknown project permission")
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var req struct {
		Effect          domain.Effect `json:"effect"`
		ExpectedVersion *int64        `json:"expected_version"`
		Reason          string        `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ExpectedVersion == nil {
		writeError(w, r, 428, "PRECONDITION_REQUIRED", "Expected permission version required")
		return
	}
	input := application.PermissionChange{
		PrincipalID: principal, Permission: permission, Effect: req.Effect,
		ExpectedVersion: *req.ExpectedVersion, Reason: req.Reason,
	}
	result, err := a.access.Change(r.Context(), actor, project, input, meta.RequestID, meta.IdempotencyKey)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
