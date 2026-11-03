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

func browserRequest(t *testing.T, f *fixture, method, path string, body any, cookie *http.Cookie, csrf, origin string) (*http.Response, map[string]any) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode == http.StatusNoContent {
		return res, nil
	}
	var data map[string]any
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatalf("JSON %d: %v", res.StatusCode, err)
	}
	return res, data
}

func TestBrowserOneTimeSessionAndCSRF(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// Human Principal for admin token, previously stored in the fixture.
	var human uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM qual.principals WHERE display_name='Admin'
       AND workspace_id=(SELECT workspace_id FROM qual.projects WHERE id=$1)`, f.project).Scan(&human); err != nil {
		t.Fatal(err)
	}
	code, hash, err := identity.GenerateSecret(identity.LoginCodePrefix)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(ctx, `INSERT INTO qual.browser_login_codes(id,principal_id,code_hash,expires_at)
        VALUES ($1,$2,$3,$4)`, uuid.New(), human, hash[:], time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://127.0.0.1:4173"
	loginPath := "/api/v1/auth/login"
	// Login CSRF: cross-origin attempts must not burn a code.
	response, _ := browserRequest(t, f, "POST", loginPath, map[string]any{"code": code}, nil, "", "https://evil.example")
	if response.StatusCode != 403 {
		t.Fatalf("cross-origin login=%d", response.StatusCode)
	}
	response, data := browserRequest(t, f, "POST", loginPath, map[string]any{"code": code}, nil, "", origin)
	if response.StatusCode != 200 {
		t.Fatalf("login %d: %v", response.StatusCode, data)
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "qual_session" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe cookie config: %#v", cookie)
	}
	csrf, ok := data["csrf_token"].(string)
	if !ok || len(csrf) != 64 {
		t.Fatal("missing CSRF token")
	}
	projects, ok := data["projects"].([]any)
	if !ok || len(projects) < 1 {
		t.Fatalf("missing projects: %v", data)
	}
	firstProject, ok := projects[0].(map[string]any)
	if !ok || firstProject["id"] != f.project.String() || firstProject["workspace_id"] == nil || firstProject["key"] != "ENG" {
		t.Fatalf("project JSON must match public API schema, got %v", projects[0])
	}
	response, _ = browserRequest(t, f, "POST", loginPath, map[string]any{"code": code}, nil, "", origin)
	if response.StatusCode != 401 {
		t.Fatalf("one-time code replay=%d", response.StatusCode)
	}
	response, data = browserRequest(t, f, "GET", "/api/v1/auth/session", nil, cookie, "", "")
	if response.StatusCode != 200 || data["csrf_token"] != csrf {
		t.Fatalf("session read=%d data=%v", response.StatusCode, data)
	}

	workPath := fmt.Sprintf("/api/v1/projects/%s/work-items", f.project)
	response, _ = browserRequest(t, f, "GET", workPath, nil, cookie, "", "")
	if response.StatusCode != 200 {
		t.Fatalf("session list=%d", response.StatusCode)
	}
	payload := map[string]any{"kind": "task", "title": "Created from secure browser"}
	response, _ = browserRequest(t, f, "POST", workPath, payload, cookie, "", origin)
	if response.StatusCode != 403 {
		t.Fatalf("missing CSRF accepted: %d", response.StatusCode)
	}
	response, _ = browserRequest(t, f, "POST", workPath, payload, cookie, "bad-token", origin)
	if response.StatusCode != 403 {
		t.Fatalf("wrong CSRF accepted: %d", response.StatusCode)
	}
	// The standard write transport also requires an idempotency key.
	req, err := http.NewRequest("POST", f.server.URL+workPath, bytes.NewBufferString(`{"kind":"task","title":"Browser task"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "browser-create-1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("browser create=%d", res.StatusCode)
	}

	response, _ = browserRequest(t, f, "POST", "/api/v1/auth/logout", nil, cookie, csrf, origin)
	if response.StatusCode != 204 {
		t.Fatalf("logout=%d", response.StatusCode)
	}
	response, _ = browserRequest(t, f, "GET", "/api/v1/auth/session", nil, cookie, "", "")
	if response.StatusCode != 401 {
		t.Fatalf("revoked session still active: %d", response.StatusCode)
	}
}
