package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	runapp "qual/internal/agentrun/application"
	rundomain "qual/internal/agentrun/domain"
	evidenceapp "qual/internal/evidence/application"
	evidencedomain "qual/internal/evidence/domain"
	eventapp "qual/internal/externalevent/application"
	eventdomain "qual/internal/externalevent/domain"
	identity "qual/internal/identity/application"
	webhooks "qual/internal/integration/webhooks"
	testingapp "qual/internal/testing/application"
	testingdomain "qual/internal/testing/domain"
	"qual/internal/work/application"
	"qual/internal/work/domain"
)

type requestIDKey struct{}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "request_id": requestID(r.Context()),
	}})
}

func handleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, eventapp.ErrPrecondition):
		writeError(w, r, 428, "PRECONDITION_REQUIRED", "ExternalEvent Idempotency-Key required")
	case errors.Is(err, eventdomain.ErrIdempotency):
		writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", "ExternalEvent request key reused")
	case errors.Is(err, eventdomain.ErrConflict):
		writeError(w, r, 409, "EVENT_IDENTITY_CONFLICT", "ExternalEvent source ID conflicts with persisted fact")
	case errors.Is(err, eventdomain.ErrNotFound):
		writeError(w, r, 404, "NOT_FOUND", "Event target/correlation not found")
	case errors.Is(err, eventdomain.ErrValidation):
		writeError(w, r, 422, "VALIDATION_ERROR", "Invalid ExternalEvent fact")
	case errors.Is(err, runapp.ErrPrecondition):
		writeError(w, r, 428, "PRECONDITION_REQUIRED", "AgentRun idempotency and current version required")
	case errors.Is(err, runapp.ErrIdempotency):
		writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", "AgentRun request key reused")
	case errors.Is(err, rundomain.ErrNotFound):
		writeError(w, r, 404, "NOT_FOUND", "AgentRun or linked resource not found")
	case errors.Is(err, rundomain.ErrConflict):
		writeError(w, r, 409, "VERSION_CONFLICT", "AgentRun report conflicts with existing facts")
	case errors.Is(err, rundomain.ErrValidation):
		writeError(w, r, 422, "VALIDATION_ERROR", "Invalid external AgentRun report")
	case errors.Is(err, rundomain.ErrRateLimited):
		writeError(w, r, 429, "HEARTBEAT_THROTTLED", "Heartbeat already accepted in the configured sample interval")
	case errors.Is(err, identity.ErrAccessValidation):
		writeError(w, r, 422, "VALIDATION_ERROR", "Invalid permission override request")
	case errors.Is(err, identity.ErrAccessConflict):
		writeError(w, r, 409, "VERSION_CONFLICT", "Permission changed; reload and retry")
	case errors.Is(err, identity.ErrAccessIdempotency):
		writeError(w, r, 409, "IDEMPOTENCY_CONFLICT", "Permission idempotency key reused")
	case errors.Is(err, identity.ErrAccessLocked):
		writeError(w, r, 403, "PERMISSION_DENIED", "Protected principal or self-modification")
	case errors.Is(err, evidenceapp.ErrPrecondition):
		writeError(w, r, 428, "PRECONDITION_REQUIRED", "Idempotency-Key required")
	case errors.Is(err, evidenceapp.ErrIdempotency), errors.Is(err, evidencedomain.ErrConflict), errors.Is(err, webhooks.ErrConflict):
		writeError(w, r, 409, "CONFLICT", "Evidence or webhook record already exists or state changed")
	case errors.Is(err, evidencedomain.ErrNotFound), errors.Is(err, webhooks.ErrNotFound):
		writeError(w, r, 404, "NOT_FOUND", "Resource not found")
	case errors.Is(err, evidencedomain.ErrIntegrity):
		writeError(w, r, 422, "INTEGRITY_MISMATCH", "Object content did not match the declared SHA-256 or size")
	case errors.Is(err, evidencedomain.ErrLimit):
		writeError(w, r, 422, "EXPORT_LIMIT_EXCEEDED", "Project export exceeds the local snapshot limits")
	case errors.Is(err, evidencedomain.ErrValidation), errors.Is(err, webhooks.ErrValidation):
		writeError(w, r, 422, "VALIDATION_ERROR", "Invalid evidence or webhook input")
	case errors.Is(err, evidencedomain.ErrUnavailable), errors.Is(err, webhooks.ErrUnavailable):
		writeError(w, r, 503, "DEPENDENCY_UNAVAILABLE", "Object store or webhook configuration unavailable")
	case errors.Is(err, identity.ErrUnauthenticated):
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required")
	case errors.Is(err, identity.ErrNotFound), errors.Is(err, application.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource not found")
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, domain.ErrReview):
		writeError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "Insufficient permission")
	case errors.Is(err, testingapp.ErrPrecondition):
		writeError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "Idempotency key or revision required")
	case errors.Is(err, testingapp.ErrIdempotency):
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key already used")
	case errors.Is(err, testingdomain.ErrNotFound), errors.Is(err, testingdomain.ErrNotPublished):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Test resource not found")
	case errors.Is(err, testingdomain.ErrConflict):
		writeError(w, r, http.StatusConflict, "VERSION_CONFLICT", "Test specification revision changed")
	case errors.Is(err, testingdomain.ErrDuplicate), errors.Is(err, testingdomain.ErrAlreadyPublished):
		writeError(w, r, http.StatusConflict, "DUPLICATE_TEST_RECORD", "Test record already exists")
	case errors.Is(err, testingdomain.ErrNotFailed):
		writeError(w, r, http.StatusUnprocessableEntity, "NOT_FAILED", "Only a failed attempt can link to a defect")
	case errors.Is(err, testingdomain.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Invalid testing data")
	case errors.Is(err, application.ErrPrecondition):
		writeError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "Idempotency key and expected version required")
	case errors.Is(err, application.ErrIdempotency):
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different request")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, r, http.StatusConflict, "VERSION_CONFLICT", "Work item has changed; reload and retry")
	case errors.Is(err, domain.ErrClaimed):
		writeError(w, r, http.StatusConflict, "ALREADY_CLAIMED", "Work item already claimed")
	case errors.Is(err, domain.ErrDependency):
		writeError(w, r, http.StatusUnprocessableEntity, "INVALID_DEPENDENCY", "Invalid or cyclic work dependency")
	case errors.Is(err, domain.ErrDoneGate):
		writeError(w, r, http.StatusUnprocessableEntity, "ACCEPTANCE_REQUIRED", "Explicit server-verified review acceptance is required")
	case errors.Is(err, domain.ErrTransition):
		writeError(w, r, http.StatusUnprocessableEntity, "INVALID_TRANSITION", "Invalid workflow transition")
	case errors.Is(err, domain.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Invalid work item")
	default:
		// Never return PostgreSQL errors, token contents or SQL to callers.
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal service error")
	}
}

func wrapWithRequestID(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store")
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
