//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPhase10ProjectPrincipalSummariesAreScoped(t *testing.T) {
	f := setup(t)
	admin := principalFor(t, f, f.admin)
	agent := principalFor(t, f, f.agent)
	agent2 := principalFor(t, f, f.agent2)
	viewer := principalFor(t, f, f.viewer)
	other := uuid.New()
	var ws uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT workspace_id FROM qual.projects WHERE id=$1`, f.project).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.principals(id,workspace_id,kind,display_name) VALUES($1,$2,'agent','Other Project Secret')`, other, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO qual.memberships(id,workspace_id,project_id,principal_id,role)
 VALUES($1,$2,$3,$4,'project_member')`, uuid.New(), ws, f.otherProject, other); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("%s/api/v1/projects/%s/principals?ids=", f.server.URL, f.project)
	url := base + strings.Join([]string{agent.String(), admin.String(), agent2.String(), viewer.String(), other.String(), uuid.NewString()}, ",")
	for _, tc := range []struct {
		label, token, url string
		status            int
	}{
		{"anonymous", "", url, 401},
		{"integration without work:read", f.reporter, url, 403},
		{"foreign project", f.agent, strings.Replace(url, f.project.String(), f.otherProject.String(), 1), 404},
		{"invalid uuid", f.agent, base + "not-a-uuid", 400},
		{"no ids", f.agent, strings.TrimSuffix(base, "ids="), 400},
		{"too many", f.agent, base + strings.Repeat(uuid.NewString()+",", 50) + uuid.NewString(), 400},
	} {
		t.Run(tc.label, func(t *testing.T) {
			status, _, _ := request(t, "GET", tc.url, tc.token, "", "", nil)
			if status != tc.status {
				t.Fatalf("%s got %d want %d", tc.label, status, tc.status)
			}
		})
	}
	status, page, headers := request(t, "GET", url, f.viewer, "", "", nil)
	if status != http.StatusOK {
		t.Fatalf("read-only member could not resolve principals: %d %v", status, page)
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("principal summaries must not be stored")
	}
	items, ok := page["items"].([]any)
	if !ok || len(items) != 4 {
		t.Fatalf("expected four active project members, got %#v", page)
	}
	seen := map[string]string{}
	for _, record := range items {
		x := record.(map[string]any)
		if len(x) != 3 {
			t.Fatalf("leaked role, scope or policy in %v", x)
		}
		seen[x["id"].(string)] = x["display_name"].(string) + "|" + x["kind"].(string)
	}
	for id, label := range map[string]string{admin.String(): "Admin|human", agent.String(): "Agent A|agent", agent2.String(): "Agent B|agent", viewer.String(): "Viewer|human"} {
		if seen[id] != label {
			t.Fatalf("identity %s mismatch: %s", id, seen[id])
		}
	}
	if _, yes := seen[other.String()]; yes {
		t.Fatal("cross-project identity leaked")
	}
	if s, _, _ := request(t, "GET", f.server.URL+"/api/v1/projects/"+f.project.String()+"/access", f.viewer, "", "", nil); s != 403 {
		t.Fatalf("read-only member obtained permission metadata: %d", s)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.principals SET disabled_at=now() WHERE id=$1`, agent2); err != nil {
		t.Fatal(err)
	}
	status, page, _ = request(t, "GET", base+agent2.String(), f.viewer, "", "", nil)
	if status != 200 || len(page["items"].([]any)) != 0 {
		t.Fatalf("disabled principal remained visible %d %v", status, page)
	}
}
