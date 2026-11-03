//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
)

func TestPhase8BrowserAccessUsesCookieAndCSRF(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var human uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT principal_id FROM qual.credentials
  WHERE token_sha256=$1`, mustTokenDigest(t, f.admin)).Scan(&human); err != nil {
		t.Fatal(err)
	}
	code, hash, err := identity.GenerateSecret(identity.LoginCodePrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO qual.browser_login_codes(id,principal_id,code_hash,expires_at)
  VALUES($1,$2,$3,$4)`, uuid.New(), human, hash[:], time.Now().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	origin := "http://127.0.0.1:4173"
	login, response := browserRequest(t, f, "POST", "/api/v1/auth/login", map[string]any{"code": code}, nil, "", origin)
	if login.StatusCode != 200 {
		t.Fatalf("Human browser login: %d %v", login.StatusCode, response)
	}
	cookie := login.Cookies()[0]
	csrf := response["csrf_token"].(string)
	base := fmt.Sprintf("/api/v1/projects/%s/access", f.project)
	me, info := browserRequest(t, f, "GET", base+"/me", nil, cookie, "", "")
	if me.StatusCode != 200 || info["kind"] != "human" {
		t.Fatalf("Human cookie must authenticate new Access API, got %d: %v", me.StatusCode, info)
	}
	members, listing := browserRequest(t, f, "GET", base, nil, cookie, "", "")
	if members.StatusCode != 200 || len(listing["items"].([]any)) < 2 {
		t.Fatalf("Human Admin access member list failed: %d %v", members.StatusCode, listing)
	}
	principal := principalFor(t, f, f.agent)
	path := base + "/" + principal.String() + "/permissions/work:review"
	input, _ := json.Marshal(map[string]any{
		"effect": "allow", "expected_version": 0,
		"reason": "Human approved delegating a test Agent to review in Phase 8 browser",
	})
	put := func(csrfValue string, originValue string, key string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest("PUT", f.server.URL+path, bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", originValue)
		req.Header.Set("X-CSRF-Token", csrfValue)
		req.Header.Set("Idempotency-Key", key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body
	}
	if status, _ := put("", "http://127.0.0.1:4173", "phase8-browser-missing-csrf"); status != 403 {
		t.Fatalf("missing CSRF accepted=%d", status)
	}
	if status, _ := put(csrf, "http://evil.example", "phase8-browser-bad-origin"); status != 403 {
		t.Fatalf("bad Origin accepted=%d", status)
	}
	if status, body := put(csrf, origin, "phase8-browser-valid-grant"); status != 200 || body["effect"] != "allow" {
		t.Fatalf("browser project grant failed=%d %v", status, body)
	}
	if status, body := put(csrf, origin, "phase8-browser-valid-grant"); status != 200 || body["version"] != float64(1) {
		t.Fatalf("browser idempotent replay failed=%d %v", status, body)
	}
	if logout, _ := browserRequest(t, f, "POST", "/api/v1/auth/logout", nil, cookie, csrf, origin); logout.StatusCode != 204 {
		t.Fatalf("browser logout failed=%d", logout.StatusCode)
	}
	if status, _ := put(csrf, origin, "phase8-browser-revoked-session"); status != 401 {
		t.Fatalf("revoked Human cookie still changed permissions=%d", status)
	}
}

func mustTokenDigest(t *testing.T, token string) []byte {
	t.Helper()
	hash, err := identity.HashToken(token)
	if err != nil {
		t.Fatal(err)
	}
	return hash[:]
}
