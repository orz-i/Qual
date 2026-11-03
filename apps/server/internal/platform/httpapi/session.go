package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	identity "qual/internal/identity/application"
)

const sessionCookieName = "qual_session"

type loginAttempt struct {
	Window time.Time
	Count  int
}
type sessionAPI struct {
	repo          identity.BrowserAuth
	allowedOrigin string
	csrfKey       [32]byte
	mu            sync.Mutex
	attempts      map[string]loginAttempt
}

func newSessionAPI(repo identity.BrowserAuth, origin string) (*sessionAPI, error) {
	if origin == "" {
		origin = "http://127.0.0.1:5173"
	}
	// Local-only Phase 3 design: never trust an arbitrary origin for cookie auth.
	if origin != "http://127.0.0.1:5173" && origin != "http://localhost:5173" &&
		origin != "http://127.0.0.1:4173" && origin != "http://localhost:4173" {
		return nil, errors.New("Phase 3 browser sessions only allow loopback Vite origins")
	}
	a := &sessionAPI{repo: repo, allowedOrigin: origin, attempts: make(map[string]loginAttempt)}
	if _, err := rand.Read(a.csrfKey[:]); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *sessionAPI) csrf(token string) string {
	mac := hmac.New(sha256.New, a.csrfKey[:])
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *sessionAPI) originOK(r *http.Request) bool {
	return r.Header.Get("Origin") == a.allowedOrigin
}
func (a *sessionAPI) mutationOK(r *http.Request, sessionToken string) bool {
	if !a.originOK(r) {
		return false
	}
	csrf := r.Header.Get("X-CSRF-Token")
	expected := a.csrf(sessionToken)
	return len(csrf) == len(expected) && hmac.Equal([]byte(csrf), []byte(expected))
}
func (a *sessionAPI) limit(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.attempts) > 4096 {
		for key, v := range a.attempts {
			if now.Sub(v.Window) > time.Minute {
				delete(a.attempts, key)
			}
		}
	}
	v := a.attempts[host]
	if now.Sub(v.Window) >= time.Minute {
		v = loginAttempt{Window: now}
	}
	v.Count++
	a.attempts[host] = v
	return v.Count <= 8
}

func (a *sessionAPI) token(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	return cookie.Value, true
}
func (a *sessionAPI) authenticate(w http.ResponseWriter, r *http.Request) (identity.Actor, string, bool) {
	token, ok := a.token(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Browser session required")
		return identity.Actor{}, "", false
	}
	actor, err := a.repo.AuthenticateSession(r.Context(), token)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Browser session expired or revoked")
		} else {
			writeError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Session lookup unavailable")
		}
		return identity.Actor{}, "", false
	}
	return actor, token, true
}
func (a *sessionAPI) setCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/api/v1", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
		Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
	})
}
func (a *sessionAPI) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/api/v1", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1,
	})
}
func (a *sessionAPI) response(w http.ResponseWriter, r *http.Request, actor identity.Actor, token string) {
	principal, err := a.repo.Principal(r.Context(), actor)
	if err != nil {
		handleError(w, r, err)
		return
	}
	projects, err := a.repo.Projects(r.Context(), actor)
	if err != nil {
		handleError(w, r, err)
		return
	}
	w.Header().Set("Vary", "Cookie")
	writeJSON(w, http.StatusOK, map[string]any{
		"principal": principal, "projects": projects, "csrf_token": a.csrf(token),
	})
}
func (a *sessionAPI) login(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		writeError(w, r, http.StatusForbidden, "ORIGIN_DENIED", "Local browser origin required")
		return
	}
	if !a.limit(r) {
		writeError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many login attempts")
		return
	}
	var payload struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &payload) {
		return
	}
	payload.Code = strings.TrimSpace(payload.Code)
	session, err := a.repo.RedeemCode(r.Context(), payload.Code)
	if err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Code invalid, used or expired")
		} else {
			handleError(w, r, err)
		}
		return
	}
	a.setCookie(w, r, session.Token, session.ExpiresAt)
	a.response(w, r, session.Actor, session.Token)
}
func (a *sessionAPI) current(w http.ResponseWriter, r *http.Request) {
	actor, token, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	a.response(w, r, actor, token)
}
func (a *sessionAPI) logout(w http.ResponseWriter, r *http.Request) {
	_, token, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	if !a.mutationOK(r, token) {
		writeError(w, r, http.StatusForbidden, "CSRF_DENIED", "Origin and CSRF token required")
		return
	}
	if err := a.repo.LogoutSession(r.Context(), token); err != nil {
		handleError(w, r, err)
		return
	}
	a.clearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}
