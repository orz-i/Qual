package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()
	NewHandler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if contentType := res.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q", contentType)
	}
	if body := res.Body.String(); body != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q", body)
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Error("health response must not be cached")
	}
}

func TestMissingAndWrongMethod(t *testing.T) {
	handler := NewHandler()
	cases := []struct {
		name, method, path string
		status             int
	}{
		{"missing route", http.MethodGet, "/api/v1/work-items", http.StatusNotFound},
		{"health method not allowed", http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d", res.Code, tc.status)
			}
		})
	}
}
