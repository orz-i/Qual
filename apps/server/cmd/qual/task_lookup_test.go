package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskDisplayKeyLookupIsAuthorizedAndPaged(t *testing.T) {
	project := "11111111-1111-4111-8111-111111111111"
	itemID := "22222222-2222-4222-8222-222222222222"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer qual66-test-token" || r.Method != http.MethodGet {
			t.Errorf("not a scoped, read-only request: %s", r.Method)
		}
		base := "/api/v1/projects/" + project + "/work-items"
		switch calls {
		case 1:
			if r.URL.Path != base || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("cursor") != "" {
				t.Errorf("unexpected first list request: %s", r.URL.String())
			}
			fmt.Fprint(w, `{"items":[{"key":"QUAL-67","id":"33333333-3333-4333-8333-333333333333"}],"next_cursor":"second-page"}`)
		case 2:
			if r.URL.Path != base || r.URL.Query().Get("cursor") != "second-page" {
				t.Errorf("unexpected second list request: %s", r.URL.String())
			}
			fmt.Fprintf(w, `{"items":[{"key":"QUAL-66","id":"%s"}]}`, itemID)
		case 3:
			if r.URL.Path != base+"/"+itemID {
				t.Errorf("show was not resolved to the UUID: %s", r.URL.Path)
			}
			fmt.Fprintf(w, `{"id":"%s","key":"QUAL-66","version":4}`, itemID)
		default:
			t.Error("unexpected extra API call")
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual66-test-token")
	if err := run([]string{"show", "-project", project, "-item", "QUAL-66"}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected 2 authorized pages + 1 GET, got %d", calls)
	}
}

func TestTaskDisplayKeyLookupRejectsMalformedAndDoesNotGuess(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("cursor") != "" {
			fmt.Fprint(w, `{"items":[{"key":"QUAL-66","id":"22222222-2222-4222-8222-222222222222"}]}`)
		} else {
			fmt.Fprint(w, `{"items":[{"key":"QUAL-67","id":"33333333-3333-4333-8333-333333333333"}],"next_cursor":"next"}`)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual66-test-token")
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "QUAL-", " QUAL-66", "QUAL-0", "../QUAL-66", "00000000-0000-0000-0000-000000000000"} {
		if _, err := resolveTaskID(context.Background(), c, "11111111-1111-4111-8111-111111111111", key); err == nil {
			t.Errorf("invalid key accepted: %q", key)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid keys contacted backend: %d", calls)
	}
	// Direct UUID does not trigger any index/list lookup.
	id := "22222222-2222-4222-8222-222222222222"
	if actual, err := resolveTaskID(context.Background(), c, "11111111-1111-4111-8111-111111111111", id); err != nil || actual != id {
		t.Fatalf("UUID resolution changed: %v %v", actual, err)
	}
	if calls != 0 {
		t.Fatal("UUID should not list WorkItems")
	}
	_, err = resolveTaskID(context.Background(), c, "11111111-1111-4111-8111-111111111111", "QUAL-999")
	if err == nil || !strings.Contains(err.Error(), "not found") || calls != 2 {
		t.Fatalf("expected bounded not-found, got %v with %d calls", err, calls)
	}
}

func TestTaskDisplayKeyLookupFailsClosedOnCursorsAndAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"empty page with cursor", `{"items":[],"next_cursor":"loop"}`, 200},
		{"repeating cursor", `{"items":[{"key":"QUAL-67","id":"33333333-3333-4333-8333-333333333333"}],"next_cursor":"loop"}`, 200},
		{"unauthorized", `{"error":{"code":"PERMISSION_DENIED","message":"blocked"}}`, 403},
		{"invalid-json", "{", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			t.Setenv("QUAL_API_URL", server.URL)
			t.Setenv("QUAL_TOKEN", "test-only")
			c, err := newClient()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolveTaskID(context.Background(), c, "11111111-1111-4111-8111-111111111111", "QUAL-66"); err == nil {
				t.Fatal("lookup silently proceeded on invalid/forbidden source")
			}
			if calls > 2 {
				t.Errorf("unbounded retry: %d", calls)
			}
		})
	}
}
