package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRefusePlaintextRemote(t *testing.T) {
	t.Setenv("QUAL_TOKEN", "test-placeholder-not-real")
	t.Setenv("QUAL_API_URL", "http://example.com:8080")
	if _, err := newClient(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("plaintext accepted: %v", err)
	}
}

func TestReviewQueueCLIUsesScopedReadOnlyQuery(t *testing.T) {
	projectID := "00000000-0000-4000-8000-000000000016"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-review-token" {
			t.Errorf("reviews must use a scoped read-only GET, got %s", r.Method)
		}
		wantPath := "/api/v1/projects/" + projectID + "/review-queue"
		if r.URL.Path != wantPath || r.URL.Query().Get("limit") != "1" || r.URL.Query().Get("offset") != "2" {
			t.Errorf("wrong review queue request: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"has_more":false}`))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "test-review-token")
	if err := run([]string{"reviews", "-project", projectID, "-limit", "1", "-offset", "2"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one review queue GET, got %d", calls)
	}
	if err := run([]string{"reviews", "-project", projectID, "-limit", "101"}); err == nil {
		t.Fatal("accepted unbounded review queue limit")
	}
	if err := run([]string{"reviews", "-project", projectID, "-offset", "-1"}); err == nil {
		t.Fatal("accepted negative review queue offset")
	}
	if calls != 1 {
		t.Fatal("invalid queue flags triggered a backend request")
	}
}

func TestActivityCLIUsesBoundedScopedRead(t *testing.T) {
	projectID := "00000000-0000-4000-8000-000000000019"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-activity-token" {
			t.Errorf("activity must use a scoped read-only GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/projects/"+projectID+"/activity" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("wrong activity query: %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "test-activity-token")
	if err := run([]string{"activity", "-project", projectID, "-limit", "100"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one activity GET, got %d", calls)
	}
	for _, flag := range []string{"0", "101", "-1"} {
		if err := run([]string{"activity", "-project", projectID, "-limit", flag}); err == nil {
			t.Errorf("accepted invalid activity limit %q", flag)
		}
	}
	if calls != 1 {
		t.Fatal("invalid activity limits triggered a backend request")
	}
}

func TestExplicitReviewCLIAndLegacyHistoryRoutes(t *testing.T) {
	projectID := "00000000-0000-4000-8000-000000000021"
	itemID := "00000000-0000-4000-8000-000000000022"
	runID := "00000000-0000-4000-8000-000000000023"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-review-governance-token" {
			t.Error("missing scoped caller credential")
		}
		switch r.URL.Path {
		case "/api/v1/projects/" + projectID + "/review-policy":
			if r.Method == http.MethodPut {
				if r.Header.Get("Idempotency-Key") != "policy-update-1" {
					t.Error("policy idempotency missing")
				}
				var data map[string]any
				_ = json.NewDecoder(r.Body).Decode(&data)
				if data["expected_version"] != float64(0) || data["owner_acceptance_allowed"] != true ||
					data["default_mode"] != "owner_acceptance" {
					t.Errorf("policy payload %v", data)
				}
			} else if r.Method != http.MethodGet {
				t.Error("wrong project review policy method")
			}
		case "/api/v1/projects/" + projectID + "/work-items/" + itemID + "/acceptances":
			if r.Method != http.MethodGet {
				t.Error("legacy history should only read")
			}
		case "/api/v1/projects/" + projectID + "/work-items/" + itemID + "/complete":
			if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "complete-verified-1" {
				t.Error("not explicit idempotent complete")
			}
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Error(err)
			}
			if data["acceptance_mode"] != "owner_acceptance" || data["expected_version"] != float64(5) ||
				data["reference_value"] != runID || data["acknowledge_self_review"] != true {
				t.Errorf("incomplete owner decision %v", data)
			}
			for _, forbidden := range []string{"actor_id", "credential_id", "policy_source", "self_review"} {
				if _, exists := data[forbidden]; exists {
					t.Errorf("client spoofed %s", forbidden)
				}
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "test-review-governance-token")
	tests := [][]string{
		{"review-policy", "-project", projectID},
		{"review-policy-set", "-project", projectID, "-mode", "owner_acceptance", "-allow-owner=true", "-policy-version", "0", "-reason", "Approved risk exception by authorized Human Workspace Admin", "-request-key", "policy-update-1"},
		{"acceptance", "-project", projectID, "-item", itemID},
		{"complete", "-project", projectID, "-item", itemID, "-version", "5", "-mode", "owner_acceptance",
			"-reference-kind", "test_run", "-reference-value", runID, "-reason", "I accept documented residual risk after evidence review",
			"-acknowledge-self-review=true", "-request-key", "complete-verified-1"},
	}
	for _, a := range tests {
		if err := run(a); err != nil {
			t.Fatalf("%s: %v", a[0], err)
		}
	}
	if calls != len(tests) {
		t.Fatalf("expected %d requests, got %d", len(tests), calls)
	}
	invalid := [][]string{
		{"complete", "-project", projectID, "-item", itemID, "-version", "5", "-mode", "owner_acceptance", "-reference-kind", "test_run", "-reference-value", runID, "-reason", "too short"},
		{"complete", "-project", projectID, "-item", itemID, "-version", "5", "-mode", "independent_review", "-reference-kind", "git_commit", "-reference-value", runID},
		{"review-policy-set", "-project", projectID, "-policy-version", "-1", "-mode", "owner_acceptance", "-reason", "risk"},
	}
	for _, a := range invalid {
		if err := run(a); err == nil {
			t.Errorf("accepted unsafe %s", a[0])
		}
	}
	if calls != len(tests) {
		t.Fatal("invalid CLI governance flags triggered a request")
	}
}

func TestWorkItemReviewPolicyCLIUsesCASAndDoesNotForgeReviewer(t *testing.T) {
	projectID := "00000000-0000-4000-8000-000000000041"
	workID := "00000000-0000-4000-8000-000000000042"
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/projects/"+projectID+"/work-items/"+workID+"/review-policy" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			if r.Header.Get("Idempotency-Key") != "work-policy-cas-1" {
				t.Error("missing idempotency key")
			}
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Fatal(err)
			}
			if data["mode"] != "independent_review" || data["expected_version"] != float64(7) ||
				data["expected_policy_version"] != float64(1) || data["reason"] == "" {
				t.Errorf("bad override payload: %v", data)
			}
			for _, fake := range []string{"actor_id", "self_review", "policy_source", "credential_id"} {
				if _, exists := data[fake]; exists {
					t.Errorf("untrusted identity field %s", fake)
				}
			}
		} else if r.Method != http.MethodGet {
			t.Error("expected read-only GET")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"independent_review"}`))
	}))
	defer srv.Close()
	t.Setenv("QUAL_API_URL", srv.URL)
	t.Setenv("QUAL_TOKEN", "local-fake-pat-test-only")
	if err := run([]string{"work-review-policy", "-project", projectID, "-item", workID}); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"work-review-policy-set", "-project", projectID, "-item", workID,
		"-version", "7", "-policy-version", "1", "-mode", "independent_review",
		"-reason", "Require distinct independent reviewer for this WorkItem",
		"-request-key", "work-policy-cas-1"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("two authorized read/write calls expected, got %d", requests)
	}
	if err := run([]string{"work-review-policy-set", "-project", projectID, "-item", workID,
		"-version", "7", "-policy-version", "-1", "-mode", "owner_acceptance",
		"-reason", "Unauthorized self review must not be submitted"}); err == nil {
		t.Fatal("negative policy version passed")
	}
	if requests != 2 {
		t.Fatal("invalid policy request was sent to server")
	}
}

func TestRequiresCredential(t *testing.T) {
	t.Setenv("QUAL_API_URL", "http://127.0.0.1:18080")
	t.Setenv("QUAL_TOKEN", "")
	if _, err := newClient(); err == nil {
		t.Fatal("missing credential accepted")
	}
}
func TestClientBearerAndVersion(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("If-Match") != "\"3\"" ||
			r.Header.Get("Idempotency-Key") != "test-retry-123" {
			t.Errorf("missing auth/concurrency/idempotency: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":4}`))
	}))
	defer s.Close()
	t.Setenv("QUAL_API_URL", s.URL)
	t.Setenv("QUAL_TOKEN", "test-secret")
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	body, code, err := c.do(context.Background(), "PATCH", "/api/v1/work-items/abc", map[string]string{"title": "update"}, 3, "test-retry-123")
	if err != nil || code != 200 || !strings.Contains(string(body), "\"version\":4") {
		t.Fatalf("%d %v %q", code, err, body)
	}
}
func TestAPIErrorDoesNotExposeToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"VERSION_CONFLICT","message":"stale"}}`))
	}))
	defer srv.Close()
	os.Setenv("QUAL_API_URL", srv.URL)
	os.Setenv("QUAL_TOKEN", "test-confidential-token")
	t.Cleanup(func() { os.Unsetenv("QUAL_API_URL"); os.Unsetenv("QUAL_TOKEN") })
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.do(context.Background(), "GET", "/anything", nil, 0, "")
	if err == nil || strings.Contains(err.Error(), "test-confidential-token") {
		t.Fatalf("leaked credential %v", err)
	}
}
