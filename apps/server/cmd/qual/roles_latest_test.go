package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRoleRecordLatestVersionReadsOnceAndSubmitsCASOnce(t *testing.T) {
	project, item := uuid.New(), uuid.New()
	gets, posts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-private-pat" {
			t.Error("credential scope was not forwarded to authorized endpoint")
		}
		switch r.Method {
		case "GET":
			gets++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": item.String(), "project_id": project.String(), "version": 7,
			})
		case "POST":
			posts++
			if r.Header.Get("Idempotency-Key") != "qual-role-latest-test-1" {
				t.Error("missing stable idempotency key")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["expected_version"] != float64(7) || body["capacity"] != "executor" {
				t.Errorf("server CAS body changed: %v", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"capacity":"executor","work_version":8}`))
		}
	}))
	defer server.Close()
	c := &client{base: server.URL, bearer: "test-private-pat", http: server.Client()}
	err := runRole(context.Background(), c, []string{"record", "-project", project.String(),
		"-item", item.String(), "-capacity", "executor", "-summary", "real commit",
		"-reference-kind", "git_commit", "-reference", "abcdef1234567",
		"-latest-version", "-request-key", "qual-role-latest-test-1"})
	if err != nil || gets != 1 || posts != 1 {
		t.Fatalf("latest-version should issue exactly one GET and POST: %v, %d/%d", err, gets, posts)
	}
}

func TestRoleRecordLatestVersionNeverRetriesOrAcceptsWrongIdentity(t *testing.T) {
	project, item := uuid.New(), uuid.New()
	for name, wrongIdentity := range map[string]bool{"stale CAS": false, "wrong identity": true} {
		t.Run(name, func(t *testing.T) {
			gets, posts := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					gets++
					responseID := item
					if wrongIdentity {
						responseID = uuid.New()
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"project_id":%q,"version":9}`, responseID.String(), project.String())
					return
				}
				posts++
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":{"code":"VERSION_CONFLICT","message":"outdated item"}}`))
			}))
			defer server.Close()
			c := &client{base: server.URL, bearer: "test-pat", http: server.Client()}
			err := runRole(context.Background(), c, []string{
				"record", "-project", project.String(), "-item", item.String(),
				"-capacity", "tester", "-summary", "actual external test",
				"-reference-kind", "none", "-latest-version"})
			if err == nil || gets != 1 || (wrongIdentity && posts != 0) || (!wrongIdentity && posts != 1) {
				t.Fatalf("expected safe failure with one lookup, no retries: %v, %d/%d", err, gets, posts)
			}
			if !wrongIdentity && !strings.Contains(err.Error(), "VERSION_CONFLICT") {
				t.Fatalf("original CAS conflict should be returned, got %v", err)
			}
		})
	}
}

func TestRoleRecordMustSelectExplicitVersionOrLatestNotBoth(t *testing.T) {
	project, item := uuid.New(), uuid.New()
	c := &client{base: "http://127.0.0.1:1", bearer: "not-used", http: &http.Client{}}
	common := []string{"record", "-project", project.String(), "-item", item.String(),
		"-capacity", "executor", "-summary", "observed"}
	for _, extra := range [][]string{
		{},
		{"-version", "7", "-latest-version"},
		{"-version", "-1"},
	} {
		err := runRole(context.Background(), c, append(append([]string{}, common...), extra...))
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("expected flag validation before any network access: %v", err)
		}
	}
}
