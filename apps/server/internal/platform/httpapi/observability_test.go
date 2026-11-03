package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessFailsClosedAndDoesNotExposeDependencies(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ready func(context.Context) error
		want  int
	}{
		{"missing", nil, 503},
		{"healthy", func(context.Context) error { return nil }, 200},
		{"database down", func(context.Context) error { return errors.New("secret database password and DSN") }, 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(&Dependencies{Ready: tt.ready})
			req := httptest.NewRequest("GET", "/readyz", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("readyz status=%d want %d", rec.Code, tt.want)
			}
			if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "password") {
				t.Fatalf("dependency leaked: %s", rec.Body.String())
			}
			live := httptest.NewRecorder()
			handler.ServeHTTP(live, httptest.NewRequest("GET", "/healthz", nil))
			if live.Code != 200 {
				t.Fatal("dependency failure affected liveness")
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("readiness cacheable")
			}
		})
	}
}
func TestMetricsAuthenticationCardinalityAndRequestLogging(t *testing.T) {
	token := strings.Repeat("x", 40)
	var logs bytes.Buffer
	handler := NewHandler(&Dependencies{MetricsToken: token, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	secretPath := "/api/v1/projects/0f389d38-e832-4bce-99e2-fd4c7e8efbba/artifacts?token=not-a-token"
	for i := 0; i < 8; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", secretPath, nil))
		if rec.Code != 404 {
			t.Fatalf("unmatched=%d", rec.Code)
		}
	}
	for _, auth := range []string{"", "Bearer not-the-operator-key", "Bearer " + strings.Repeat("x", 39) + "y"} {
		req := httptest.NewRequest("GET", "/internal/metrics", nil)
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("unauthorized metrics exposure status %d", rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "/internal/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("metrics auth %d", rec.Code)
	}
	text := rec.Body.String()
	if !strings.Contains(text, `qual_http_requests_total{route="unmatched",method="GET",status_class="4xx"} 8`) {
		t.Fatalf("incorrect grouping: %s", text)
	}
	if !strings.Contains(text, `le="+Inf"`) || !strings.Contains(text, "_sum") || !strings.Contains(text, "_count") {
		t.Fatalf("histogram buckets missing: %s", text)
	}
	if strings.Contains(text, "0f389d38") || strings.Contains(text, "not-a-token") ||
		strings.Contains(logs.String(), "0f389d38") || strings.Contains(logs.String(), "not-a-token") ||
		strings.Contains(logs.String(), token) {
		t.Fatal("raw resource path or secret leaked into operational telemetry")
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("security header missing: %q", got)
	}
}
func TestOrdinaryAgentPATNeverUnlocksOperatorMetrics(t *testing.T) {
	// An Agent PAT is a real-shaped credential, but must never be accepted as
	// an independent Operator Metrics token (even when it has broad WorkItem scope).
	agentPAT := "qual_pat_" + strings.Repeat("a", 64)
	operatorToken := strings.Repeat("o", 40) // Synthetic test-only token, not an actual Operator credential.
	for _, tc := range []struct {
		name, operator string
		want           int
	}{
		{"metrics not registered", "", http.StatusNotFound},
		{"metrics configured privately", operatorToken, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler(&Dependencies{MetricsToken: tc.operator})
			for _, bearer := range []string{"", "Bearer " + agentPAT, "Bearer fake-operator-key"} {
				req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
				req.Header.Set("Authorization", bearer)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("metrics status %d, want %d", rec.Code, tc.want)
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("metrics denial must not be cached")
				}
				if strings.Contains(rec.Body.String(), agentPAT) || strings.Contains(rec.Body.String(), operatorToken) {
					t.Fatal("credential leaked in metrics denial")
				}
			}
		})
	}
}

func TestPanicReturnsGenericErrorWithRequestIDAndSafeLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	stats := newMetrics()
	handler := wrapWithRequestID(observeRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive document token never log me")
	}), stats, logger))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("GET", "/private/sensitive", nil))
	if res.Code != 500 || res.Header().Get("X-Request-ID") == "" {
		t.Fatalf("unexpected panic response %d", res.Code)
	}
	if strings.Contains(res.Body.String(), "sensitive") || strings.Contains(logs.String(), "sensitive document token") {
		t.Fatal("panic content leaked")
	}
	if !strings.Contains(stats.render(), `status_class="5xx"`) {
		t.Fatal("panic metric not counted")
	}
}
func TestUnknownMethodsAndRoutesDoNotCreateUnboundedLabels(t *testing.T) {
	stats := newMetrics()
	stats.observe("", "GARBAGE", 404, .1)
	if !strings.Contains(stats.render(), `route="unmatched",method="OTHER",status_class="4xx"`) {
		t.Fatal("untrusted method/route not normalized")
	}
}
