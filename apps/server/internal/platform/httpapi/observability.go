// Package httpapi owns operational transport for the local Qual API.
// Metrics use only compiled route patterns and status classes, never IDs or URLs.
package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var durationBounds = [...]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}

type metricKey struct{ Route, Method, Status string }
type measured struct {
	Count   uint64
	Sum     float64
	Buckets [len(durationBounds)]uint64
}
type metrics struct {
	mu     sync.Mutex
	values map[metricKey]measured
}

func newMetrics() *metrics { return &metrics{values: make(map[metricKey]measured)} }
func safeRoute(value string) string {
	if value == "" {
		return "unmatched"
	}
	// The Go ServeMux assigned this pattern. Never use URL.Path/Query as a label.
	if len(value) > 220 {
		return "unmatched"
	}
	return value
}
func safeMethod(value string) string {
	switch value {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodHead:
		return value
	default:
		return "OTHER"
	}
}
func statusClass(value int) string {
	if value < 100 || value > 599 {
		return "other"
	}
	return fmt.Sprintf("%dxx", value/100)
}
func (m *metrics) observe(route, method string, status int, seconds float64) {
	if m == nil {
		return
	}
	key := metricKey{safeRoute(route), safeMethod(method), statusClass(status)}
	m.mu.Lock()
	stats := m.values[key]
	stats.Count++
	stats.Sum += seconds
	for i, limit := range durationBounds {
		if seconds <= limit {
			stats.Buckets[i]++
		}
	}
	m.values[key] = stats
	m.mu.Unlock()
}
func quoteMetricLabel(s string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(s)
}
func (m *metrics) render() string {
	m.mu.Lock()
	copied := make(map[metricKey]measured, len(m.values))
	for k, v := range m.values {
		copied[k] = v
	}
	m.mu.Unlock()
	keys := make([]metricKey, 0, len(copied))
	for k := range copied {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Status < b.Status
	})
	var out strings.Builder
	out.WriteString("# HELP qual_http_requests_total HTTP requests grouped by route template, method and status class.\n")
	out.WriteString("# TYPE qual_http_requests_total counter\n")
	for _, key := range keys {
		v := copied[key]
		fmt.Fprintf(&out, "qual_http_requests_total{route=\"%s\",method=\"%s\",status_class=\"%s\"} %d\n",
			quoteMetricLabel(key.Route), key.Method, key.Status, v.Count)
	}
	out.WriteString("# HELP qual_http_request_duration_seconds HTTP response latency in seconds.\n")
	out.WriteString("# TYPE qual_http_request_duration_seconds histogram\n")
	for _, key := range keys {
		v := copied[key]
		labels := fmt.Sprintf("route=\"%s\",method=\"%s\",status_class=\"%s\"",
			quoteMetricLabel(key.Route), key.Method, key.Status)
		for i, limit := range durationBounds {
			fmt.Fprintf(&out, "qual_http_request_duration_seconds_bucket{%s,le=\"%g\"} %d\n", labels, limit, v.Buckets[i])
		}
		fmt.Fprintf(&out, "qual_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, v.Count)
		fmt.Fprintf(&out, "qual_http_request_duration_seconds_sum{%s} %.9f\n", labels, v.Sum)
		fmt.Fprintf(&out, "qual_http_request_duration_seconds_count{%s} %d\n", labels, v.Count)
	}
	return out.String()
}
func metricsHandler(stats *metrics, token string) http.HandlerFunc {
	expected := sha256.Sum256([]byte(token))
	return func(w http.ResponseWriter, r *http.Request) {
		// A metrics token is unrelated to any Human/Agent project credential.
		var provided string
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			provided = parts[1]
		}
		candidate := sha256.Sum256([]byte(provided))
		if provided == "" || subtle.ConstantTimeCompare(expected[:], candidate[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(stats.render()))
	}
}
func readinessHandler(check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if check == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"status\":\"unavailable\"}\n"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := check(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"status\":\"unavailable\"}\n"))
			return
		}
		_, _ = w.Write([]byte("{\"status\":\"ready\"}\n"))
	}
}

type statusWriter struct {
	http.ResponseWriter
	Status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.Status != 0 {
		return
	}
	w.Status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.Status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// middleware is opt-in to the application's JSON logger and metrics collector.
// Never include raw URL path, query string, Authorization, Cookie or body.
func observeRequests(handler http.Handler, stats *metrics, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			if recover() != nil {
				// Avoid leaking the panic value: it can include secrets or user input.
				if writer.Status == 0 {
					writeError(writer, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal service error")
				}
				if logger != nil {
					logger.Error("recovered http panic", "request_id", requestID(r.Context()))
				}
			}
			code := writer.Status
			if code == 0 {
				code = http.StatusOK
			}
			elapsed := time.Since(started)
			route := safeRoute(r.Pattern)
			stats.observe(route, r.Method, code, elapsed.Seconds())
			if logger != nil && route != "GET /healthz" && route != "GET /readyz" && route != "GET /internal/metrics" {
				logger.Info("http_request", "request_id", requestID(r.Context()),
					"route", route, "method", safeMethod(r.Method), "status", code,
					"duration_ms", elapsed.Milliseconds())
			}
		}()
		handler.ServeHTTP(writer, r)
	})
}
