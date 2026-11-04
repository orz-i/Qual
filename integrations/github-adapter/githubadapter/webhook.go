package githubadapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"
)

// VerifySignature checks bytes as sent by GitHub using HMAC-SHA256 and
// constant-time comparison; it never trusts content before verification.
func VerifySignature(raw []byte, provided string, secret []byte) bool {
	if len(provided) != 71 || provided[:7] != "sha256=" || len(secret) == 0 {
		return false
	}
	received, err := hex.DecodeString(provided[7:])
	if err != nil || len(received) != sha256.Size {
		return false
	}
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(raw)
	return subtle.ConstantTimeCompare(received, h.Sum(nil)) == 1
}

type Handler struct {
	Repository Repository
	Secret     []byte
	Spool      *Spool
	Now        func() time.Time
}

func NewHandler(repo Repository, secret []byte, spool *Spool) (Handler, error) {
	if spool == nil || len(secret) < 32 || repo.ID <= 0 || repo.FullName == "" {
		return Handler{}, ErrInvalid
	}
	// Refuse listener construction for any restored historical snapshot,
	// even if its original signed ZIP was fully valid.
	if err := spool.requireActive(); err != nil {
		return Handler{}, err
	}
	// The secret remains in process memory only. Never log the header or key.
	return Handler{Repository: repo, Secret: append([]byte(nil), secret...), Spool: spool}, nil
}
func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "JSON payload required", http.StatusUnsupportedMediaType)
		return
	}
	// A proxy that combines duplicate security headers is not trusted.
	if len(r.Header.Values("X-Hub-Signature-256")) != 1 ||
		len(r.Header.Values("X-GitHub-Delivery")) != 1 ||
		len(r.Header.Values("X-GitHub-Event")) != 1 {
		http.Error(w, "invalid signed delivery headers", http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBytes+1))
	if err != nil {
		http.Error(w, "invalid webhook body", 400)
		return
	}
	if int64(len(raw)) > MaxWebhookBytes {
		http.Error(w, "webhook payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !VerifySignature(raw, r.Header.Get("X-Hub-Signature-256"), h.Secret) {
		http.Error(w, "webhook signature rejected", http.StatusUnauthorized)
		return
	}
	d, err := ParseDelivery(raw, r.Header.Get("X-GitHub-Delivery"),
		r.Header.Get("X-GitHub-Event"), h.Repository, h.now())
	if errors.Is(err, ErrIgnore) {
		// Ignore verified but unsupported event kinds/actions without queuing.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, ErrRepo) {
		http.Error(w, "repository not allowed", http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, "unrecognized delivery", http.StatusBadRequest)
		return
	}
	created, err := h.Spool.Enqueue(d)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "delivery identity conflict", http.StatusConflict)
			return
		}
		// Any local storage failure is a 5xx to help detect missing deliveries.
		// GitHub does NOT automatically retry; human redelivery is still needed.
		http.Error(w, "durable delivery queue unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]bool{"accepted": true, "new": created})
}
