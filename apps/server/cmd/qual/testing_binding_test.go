package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestQual58CLIRunCreateSendsImmutableWorkItemBinding(t *testing.T) {
	project, work, version := uuid.NewString(), uuid.NewString(), uuid.NewString()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/projects/"+project+"/test-runs" ||
			r.Header.Get("Authorization") != "Bearer qual58-cli-fixture" ||
			r.Header.Get("Idempotency-Key") != "qual58-cli-bound-request" {
			t.Errorf("wrong request %s %s", r.Method, r.URL.Path)
		}
		var data map[string]any
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
		}
		ids, ok := data["case_version_ids"].([]any)
		if !ok || len(ids) != 1 || ids[0] != version || data["work_item_id"] != work ||
			data["name"] != "Externally observed run" {
			t.Errorf("missing bound Run identity/CaseVersion: %#v", data)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual58-cli-fixture")
	args := []string{"test", "run-create", "-project", project, "-versions", version,
		"-work-item", work, "-title", "Externally observed run", "-source", "external-cli",
		"-request-key", "qual58-cli-bound-request"}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected only one API request, got %d", calls)
	}
	bad := append([]string(nil), args...)
	for i := range bad {
		if bad[i] == work {
			bad[i] = "not-a-real-uuid"
			break
		}
	}
	if err := run(bad); err == nil {
		t.Fatal("invalid WorkItem UUID permitted")
	}
	if calls != 1 {
		t.Fatalf("invalid CLI command contacted API (%d)", calls)
	}
}
