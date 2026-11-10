package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQual67ResumeMarksUnavailableSectionsAndReadTimeWithoutInventingHistory(t *testing.T) {
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-only" || r.Method != "GET" {
			t.Errorf("unprotected/mutating request %s", r.URL)
		}
		section := r.URL.Query().Get("section")
		switch {
		case r.URL.Path == resumeTestPath+"/context" && section == "overview":
			fmt.Fprintf(w, `{"schema_version":2,"context_revision":7,"generated_at":"2026-11-07T06:33:00Z","work_item":{"id":"%s","key":"QUAL-67","title":"Task","description":"完整可执行任务","acceptance_criteria":["验收"],"version":7},"capabilities":null}`, resumeTestItem)
			seen["overview"]++
		case r.URL.Path == resumeTestPath+"/context" && section == "comments":
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":"UNAVAILABLE"}}`)
			seen["comments"]++
		case r.URL.Path == resumeTestPath+"/context":
			fmt.Fprint(w, `{"context_revision":7,"items":[],"has_more":false,"paging_limited":false}`)
			seen[section]++
		case r.URL.Path == resumeTestPath+"/threads":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":"DENIED"}}`)
			seen["threads"]++
		default:
			t.Errorf("unexpected fallback or unauthorized query: %s", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "fixture-only")
	client, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	result, err := captureResume(t, func() error { return runResume(context.Background(), client, resumeTestPath, 3) })
	if err != nil {
		t.Fatal(err)
	}
	if result["generated_at"] != "2026-11-07T06:33:00Z" {
		t.Fatalf("Task source read time omitted: %v", result["generated_at"])
	}
	omitted := result["omitted_sections"].(map[string]any)
	if omitted["comments"] != "bounded_source_read_unavailable" || omitted["threads"] != "not_authorized" {
		t.Fatalf("unavailable source advertised as complete: %v", omitted)
	}
	sections := result["sections"].(map[string]any)
	if _, ok := sections["comments"]; ok {
		t.Fatal("unavailable comments must not be empty verified history")
	}
	if _, ok := sections["attempts"]; !ok {
		t.Fatal("unrelated authorized section was lost")
	}
	if seen["comments"] != 1 || seen["threads"] != 1 {
		t.Fatalf("unexpected extra read %v", seen)
	}
}

func TestQual67ResumeRejectsMixedVersionSections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("section") == "overview":
			fmt.Fprintf(w, `{"schema_version":2,"context_revision":7,"work_item":{"id":"%s","key":"QUAL-67","version":7,"description":"目标","acceptance_criteria":[]}}`, resumeTestItem)
		case r.URL.Query().Get("section") == "dependencies":
			fmt.Fprint(w, `{"context_revision":8,"items":[],"has_more":false}`)
		default:
			t.Errorf("continued after mixed version: %s", r.URL)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "fixture-only")
	client, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureResume(t, func() error { return runResume(context.Background(), client, resumeTestPath, 3) })
	if err == nil || !strings.Contains(err.Error(), "CONTEXT_CHANGED") {
		t.Fatalf("mixed source versions accepted: %v", err)
	}
}

func TestQual67BasicResumeIncludesReadTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == resumeTestPath+"/context" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":"DENIED"}}`)
		} else if r.URL.Path == resumeTestPath {
			fmt.Fprintf(w, `{"id":"%s","key":"QUAL-67","version":7,"description":"可执行入口","acceptance_criteria":["验收"]}`, resumeTestItem)
		} else if r.URL.Path == resumeTestPath+"/capabilities" {
			fmt.Fprintf(w, `{"work_item_id":"%s","work_version":7,"allowed_actions":[]}`, resumeTestItem)
		} else {
			t.Errorf("unexpected protected read %s", r.URL)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "fixture-only")
	client, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	result, err := captureResume(t, func() error { return runResume(context.Background(), client, resumeTestPath, 3) })
	if err != nil {
		t.Fatal(err)
	}
	if result["basic_only"] != true || result["generated_at"] == nil {
		t.Fatalf("unauthorized Task-only read missed scope/time: %v", result)
	}
	if result["omitted_sections"].(map[string]any)["artifacts"] != "not_authorized" {
		t.Fatalf("Task-only read implied private artifacts: %v", result)
	}
}
