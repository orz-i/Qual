package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const resumeTestProject = "11111111-1111-4111-8111-111111111111"
const resumeTestItem = "22222222-2222-4222-8222-222222222222"
const resumeTestPath = "/api/v1/projects/" + resumeTestProject + "/work-items/" + resumeTestItem

// Capture only this CLI's ordinary JSON stdout, not any authentication material.
func captureResume(t *testing.T, work func() error) (map[string]any, error) {
	t.Helper()
	old := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = old; read.Close() }()
	runErr := work()
	write.Close()
	body, readErr := io.ReadAll(read)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		return nil, runErr
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("invalid resume output: %v (%s)", err, body)
	}
	return data, nil
}

func TestQual67ResumeOldContextHydratesTaskAndPagesComments(t *testing.T) {
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer qual67-unit-test" {
			t.Errorf("unauthorized/mutating request %s", r.URL)
		}
		section := r.URL.Query().Get("section")
		switch {
		case r.URL.Path == resumeTestPath+"/context" && section == "overview":
			fmt.Fprintf(w, `{"schema_version":2,"context_revision":7,"work_item":{"id":"%s","key":"QUAL-67","title":"Task navigation","kind":"task","status":"in_progress","priority":"high","version":7,"assignee_id":null,"accountable_owner_id":null},"capabilities":null}`, resumeTestItem)
			seen["overview"]++
		case r.URL.Path == resumeTestPath:
			fmt.Fprintf(w, `{"id":"%s","key":"QUAL-67","version":7,"description":"用户 POV：菜单→详情；失败后读取同一个版本","acceptance_criteria":["中文验收：刷新持久","403 不越权"]}`, resumeTestItem)
			seen["public"]++
		case r.URL.Path == resumeTestPath+"/context":
			seen[section]++
			if section == "comments" {
				fmt.Fprint(w, `{"context_revision":7,"items":[{"source":"work_comments","source_id":"33333333-3333-4333-8333-333333333333","excerpt":"A noted partial side effect"}],"has_more":true,"paging_limited":false,"next_cursor":"versioned-comment-cursor"}`)
			} else {
				fmt.Fprint(w, `{"context_revision":7,"items":[],"has_more":false,"paging_limited":false}`)
			}
		case r.URL.Path == resumeTestPath+"/threads":
			seen["threads"]++
			fmt.Fprint(w, `{"items":[]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual67-unit-test")
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	result, err := captureResume(t, func() error { return runResume(context.Background(), c, resumeTestPath, 2) })
	if err != nil {
		t.Fatal(err)
	}
	work := result["work_item"].(map[string]any)
	if work["description"] != "用户 POV：菜单→详情；失败后读取同一个版本" ||
		len(work["acceptance_criteria"].([]any)) != 2 || result["context_revision"] != float64(7) {
		t.Fatalf("missing authoritative Task fields: %v", work)
	}
	comments := result["sections"].(map[string]any)["comments"].(map[string]any)
	if comments["has_more"] != true || comments["next_cursor"] != "versioned-comment-cursor" ||
		len(comments["items"].([]any)) != 1 {
		t.Fatalf("lost bounded checkpoint paging: %v", comments)
	}
	for _, key := range []string{"overview", "public", "dependencies", "comments", "reviews", "coverage", "attempts", "artifacts", "references", "threads"} {
		if seen[key] != 1 {
			t.Errorf("missing/duplicate read %s: %d", key, seen[key])
		}
	}
}

func TestQual67CandidateGetsOnlyPublicTaskWhenPrivateOverviewDenied(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.String())
		if r.Header.Get("Authorization") != "Bearer qual67-candidate" {
			t.Fatal("no scoped credential")
		}
		switch {
		case r.URL.Path == resumeTestPath+"/context":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":"PERMISSION_DENIED"}}`)
		case r.URL.Path == resumeTestPath:
			fmt.Fprintf(w, `{"id":"%s","key":"QUAL-67","version":7,"description":"任务目标、操作路径及失败恢复","acceptance_criteria":["实际可测验收"]}`, resumeTestItem)
		case r.URL.Path == resumeTestPath+"/capabilities":
			fmt.Fprintf(w, `{"work_item_id":"%s","work_version":7,"my_capacity":["candidate"],"allowed_actions":["task.claim"]}`, resumeTestItem)
		default:
			t.Errorf("attempted private section read: %s", r.URL)
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	t.Setenv("QUAL_API_URL", server.URL)
	t.Setenv("QUAL_TOKEN", "qual67-candidate")
	c, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	result, err := captureResume(t, func() error { return runResume(context.Background(), c, resumeTestPath, 5) })
	if err != nil {
		t.Fatal(err)
	}
	if result["basic_only"] != true || result["context_revision"] != float64(7) ||
		result["work_item"].(map[string]any)["description"] != "任务目标、操作路径及失败恢复" ||
		len(result["sections"].(map[string]any)) != 0 ||
		result["omitted_sections"].(map[string]any)["artifacts"] != "not_authorized" ||
		result["omitted_sections"].(map[string]any)["attempts"] != "not_authorized" ||
		result["capabilities"].(map[string]any)["work_version"] != float64(7) {
		t.Fatalf("candidate leaked private facts or lost goal: %+v", result)
	}
	if len(calls) != 3 {
		t.Fatalf("unnecessary private GET after 403: %v", calls)
	}
}

func TestQual67ResumeFailsClosedOnVersionAndInvisibleTask(t *testing.T) {
	for _, tc := range []struct {
		name           string
		overviewStatus int
		taskStatus     int
		taskVersion    int
		capVersion     int
	}{
		{"hydration race", 200, 200, 8, 7},
		{"candidate stale projection", 403, 200, 7, 8},
		{"invisible public Task", 403, 404, 0, 0},
		{"invalid credentials", 401, 200, 7, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.URL.String())
				switch {
				case r.URL.Path == resumeTestPath+"/context":
					w.WriteHeader(tc.overviewStatus)
					if tc.overviewStatus == 200 {
						fmt.Fprintf(w, `{"schema_version":2,"context_revision":7,"work_item":{"id":"%s","version":7,"key":"QUAL-67"}}`, resumeTestItem)
					} else {
						fmt.Fprint(w, `{"error":{"code":"DENIED"}}`)
					}
				case r.URL.Path == resumeTestPath:
					w.WriteHeader(tc.taskStatus)
					if tc.taskStatus == 200 {
						fmt.Fprintf(w, `{"id":"%s","key":"QUAL-67","version":%d,"description":"changed"}`, resumeTestItem, tc.taskVersion)
					} else {
						fmt.Fprint(w, `{"error":{"code":"NOT_FOUND"}}`)
					}
				case r.URL.Path == resumeTestPath+"/capabilities":
					fmt.Fprintf(w, `{"work_item_id":"%s","work_version":%d}`, resumeTestItem, tc.capVersion)
				default:
					t.Errorf("private GET after denial: %s", r.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("QUAL_API_URL", server.URL)
			t.Setenv("QUAL_TOKEN", "test-token")
			c, err := newClient()
			if err != nil {
				t.Fatal(err)
			}
			_, err = captureResume(t, func() error { return runResume(context.Background(), c, resumeTestPath, 2) })
			if err == nil {
				t.Fatal("accepted invalid/cross-version source")
			}
			if strings.Contains(tc.name, "race") || strings.Contains(tc.name, "stale") {
				if !strings.Contains(err.Error(), "CONTEXT_CHANGED") {
					t.Fatalf("expected explicit version conflict: %v", err)
				}
			}
			if len(seen) > 3 {
				t.Fatalf("unexpected follow-up after error: %v", seen)
			}
			if tc.overviewStatus == 401 && len(seen) != 1 {
				t.Fatalf("401 wrongly fell back to public Task: %v", seen)
			}
		})
	}
}
