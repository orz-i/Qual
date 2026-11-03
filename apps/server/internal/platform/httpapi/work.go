package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	runapp "qual/internal/agentrun/application"
	evidenceapp "qual/internal/evidence/application"
	eventapp "qual/internal/externalevent/application"
	identity "qual/internal/identity/application"
	webhook "qual/internal/integration/webhooks"
	testingapp "qual/internal/testing/application"
	"qual/internal/work/application"
	"qual/internal/work/domain"
	contextapp "qual/internal/workcontext/application"
)

// Dependencies are optional so liveness does not require PostgreSQL.
// Protected routes are registered only when both services are supplied.
type Dependencies struct {
	Identity     identity.Authenticator
	Browser      identity.BrowserAuth
	WebOrigin    string
	Work         application.Service
	Context      *contextapp.Service
	Runs         runapp.Service
	Events       *eventapp.Service
	Tests        testingapp.Service
	Access       identity.AccessService
	Evidence     evidenceapp.Service
	Webhooks     *webhook.Manager
	Ready        func(context.Context) error
	MetricsToken string
	Logger       *slog.Logger
}

type workAPI struct {
	auth    identity.Authenticator
	session *sessionAPI
	service application.Service
}

func (a workAPI) getActor(w http.ResponseWriter, r *http.Request) (identity.Actor, bool) {
	if r.Header.Get("Authorization") == "" && a.session != nil {
		actor, token, ok := a.session.authenticate(w, r)
		if !ok {
			return identity.Actor{}, false
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.session.mutationOK(r, token) {
			writeError(w, r, http.StatusForbidden, "CSRF_DENIED", "Origin and CSRF token required")
			return identity.Actor{}, false
		}
		return actor, true
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Bearer token required")
		return identity.Actor{}, false
	}
	actor, err := a.auth.Authenticate(r.Context(), parts[1])
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Invalid or expired credential")
		} else {
			writeError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Authentication unavailable")
		}
		return identity.Actor{}, false
	}
	return actor, true
}

func pathID(w http.ResponseWriter, r *http.Request, key string) (uuid.UUID, bool) {
	value, err := uuid.Parse(r.PathValue(key))
	if err != nil || value == uuid.Nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid resource ID")
		return uuid.Nil, false
	}
	return value, true
}

func requireIdempotency(w http.ResponseWriter, r *http.Request) (application.Meta, bool) {
	meta := application.Meta{RequestID: requestID(r.Context()), IdempotencyKey: r.Header.Get("Idempotency-Key")}
	if len(meta.IdempotencyKey) < 8 || len(meta.IdempotencyKey) > 128 || strings.TrimSpace(meta.IdempotencyKey) != meta.IdempotencyKey {
		writeError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "Valid Idempotency-Key (8–128 characters) required")
		return meta, false
	}
	return meta, true
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mimeType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mimeType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type application/json required")
		return false
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "Body exceeds 64 KiB")
		} else {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON request body")
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Only one JSON object is allowed")
		return false
	}
	return true
}

func getVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := r.Header.Get("If-Match")
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		writeError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "If-Match header with resource version required")
		return 0, false
	}
	version, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || version < 1 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid If-Match version")
		return 0, false
	}
	return version, true
}

func respondItem(w http.ResponseWriter, r *http.Request, code int, item domain.WorkItem) {
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", item.Version))
	writeJSON(w, code, item)
}

func (a workAPI) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var draft domain.Draft
	if !readJSON(w, r, &draft) {
		return
	}
	item, err := a.service.Create(r.Context(), actor, projectID, draft, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/projects/%s/work-items/%s", projectID, item.ID))
	respondItem(w, r, http.StatusCreated, item)
}

func (a workAPI) get(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	item, err := a.service.Get(r.Context(), actor, projectID, itemID)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}

func (a workAPI) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := application.Filter{Status: domain.Status(q.Get("status")), Kind: domain.Kind(q.Get("kind")), Cursor: q.Get("cursor")}
	if value := q.Get("limit"); value != "" {
		num, err := strconv.Atoi(value)
		if err != nil || num < 1 || num > 100 {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "limit must be 1..100")
			return
		}
		filter.Limit = num
	}
	page, err := a.service.List(r.Context(), actor, projectID, filter)
	if err != nil {
		handleError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a workAPI) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	version, ok := getVersion(w, r)
	if !ok {
		return
	}
	var changes domain.Changes
	if !readJSON(w, r, &changes) {
		return
	}
	item, err := a.service.Update(r.Context(), actor, projectID, itemID, changes, version, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (a workAPI) claim(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var data versionRequest
	if !readJSON(w, r, &data) {
		return
	}
	item, err := a.service.Claim(r.Context(), actor, projectID, itemID, data.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}

func (a workAPI) transition(w http.ResponseWriter, r *http.Request) {
	actor, ok := a.getActor(w, r)
	if !ok {
		return
	}
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	meta, ok := requireIdempotency(w, r)
	if !ok {
		return
	}
	var data struct {
		Status          domain.Status `json:"status"`
		ExpectedVersion int64         `json:"expected_version"`
	}
	if !readJSON(w, r, &data) {
		return
	}
	item, err := a.service.Transition(r.Context(), actor, projectID, itemID, data.Status, data.ExpectedVersion, meta)
	if err != nil {
		handleError(w, r, err)
		return
	}
	respondItem(w, r, http.StatusOK, item)
}
