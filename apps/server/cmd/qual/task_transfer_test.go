package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQual56CLITransferUsesAuthenticatedRESTAndVersion(t *testing.T) {
	project := "11111111-1111-4111-8111-111111111111"
	item := "22222222-2222-4222-8222-222222222222"
	target := "33333333-3333-4333-8333-333333333333"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer qual56-test-token" || r.Method != "POST" {
			t.Errorf("incorrect credentials/method %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		switch calls {
		case 1:
			if r.URL.Path != "/api/v1/projects/"+project+"/work-items/"+item+"/release" || r.Header.Get("Idempotency-Key") != "qual56-cli-release" || payload["expected_version"] != float64(3) || len(payload) != 1 {
				t.Errorf("invalid release call %s %v", r.URL.Path, payload)
			}
		case 2:
			if r.URL.Path != "/api/v1/projects/"+project+"/work-items/"+item+"/reassign" || r.Header.Get("Idempotency-Key") != "qual56-cli-reassign" || payload["expected_version"] != float64(4) || payload["new_assignee_id"] != target || len(payload) != 2 {
				t.Errorf("invalid reassign call %s %v", r.URL.Path, payload)
			}
		default:
			t.Errorf("unexpected extra call")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222","version":5}`))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual56-test-token")
	if err := run([]string{"release", "-project", project, "-item", item, "-version", "3", "-request-key", "qual56-cli-release"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"reassign", "-project", project, "-item", item, "-version", "4", "-new-assignee", target, "-request-key", "qual56-cli-reassign"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 authenticated writes, got %d", calls)
	}
	for _, args := range [][]string{
		{"reassign", "-project", project, "-item", item, "-version", "4"},
		{"reassign", "-project", project, "-item", item, "-version", "4", "-new-assignee", "bogus"},
		{"reassign", "-project", project, "-item", item, "-version", "4", "-new-assignee", "00000000-0000-0000-0000-000000000000"},
		{"release", "-project", project, "-item", item, "-version", "0"},
	} {
		if err := run(args); err == nil {
			t.Errorf("invalid CLI args accepted: %v", args)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid CLI invocation contacted backend: %d", calls)
	}
}
