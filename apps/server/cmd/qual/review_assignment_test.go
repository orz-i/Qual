package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQual57CLIReviewAssignmentUsesAuthorizedREST(t *testing.T) {
	projectID := "11111111-1111-4111-8111-111111111111"
	itemID := "22222222-2222-4222-8222-222222222222"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer qual57-test-token" {
			t.Errorf("unexpected auth/method %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		switch calls {
		case 1:
			if r.URL.Path != "/api/v1/projects/"+projectID+"/work-items/"+itemID+"/review-claim" ||
				r.Header.Get("Idempotency-Key") != "qual57-cli-review-claim" ||
				payload["expected_version"] != float64(4) || len(payload) != 1 {
				t.Errorf("invalid review claim %s %v", r.URL.Path, payload)
			}
		case 2:
			if r.URL.Path != "/api/v1/projects/"+projectID+"/work-items/"+itemID+"/review-release" ||
				r.Header.Get("Idempotency-Key") != "qual57-cli-review-release" ||
				payload["expected_version"] != float64(5) || len(payload) != 1 {
				t.Errorf("invalid review release %s %v", r.URL.Path, payload)
			}
		default:
			t.Error("unexpected extra request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222","version":5}`))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual57-test-token")
	for _, args := range [][]string{
		{"review-claim", "-project", projectID, "-item", itemID, "-version", "4", "-request-key", "qual57-cli-review-claim"},
		{"review-release", "-project", projectID, "-item", itemID, "-version", "5", "-request-key", "qual57-cli-review-release"},
	} {
		if err := run(args); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("review CLI calls=%d, want 2", calls)
	}
	for _, args := range [][]string{
		{"review-claim", "-project", projectID, "-item", itemID, "-version", "0"},
		{"review-release", "-project", projectID, "-item", itemID, "-version", "-1"},
		{"review-claim", "-project", projectID, "-item", "not-a-uuid", "-version", "1"},
	} {
		if err := run(args); err == nil {
			t.Errorf("invalid review CLI invocation accepted %v", args)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid command reached backend (%d)", calls)
	}
}
