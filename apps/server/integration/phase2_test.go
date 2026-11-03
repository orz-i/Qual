//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	runapp "qual/internal/agentrun/application"
	runpg "qual/internal/agentrun/infrastructure/postgres"
	evidenceapp "qual/internal/evidence/application"
	evidencepg "qual/internal/evidence/infrastructure/postgres"
	s3store "qual/internal/evidence/infrastructure/s3store"
	eventapp "qual/internal/externalevent/application"
	eventpg "qual/internal/externalevent/infrastructure/postgres"
	identity "qual/internal/identity/application"
	idpostgres "qual/internal/identity/infrastructure/postgres"
	webhooks "qual/internal/integration/webhooks"
	"qual/internal/platform/httpapi"
	testingapp "qual/internal/testing/application"
	testingpostgres "qual/internal/testing/infrastructure/postgres"
	workapp "qual/internal/work/application"
	workpostgres "qual/internal/work/infrastructure/postgres"
	contextapp "qual/internal/workcontext/application"
	contextpg "qual/internal/workcontext/infrastructure/postgres"
)

type fixture struct {
	pool            *pgxpool.Pool
	server          *httptest.Server
	project         uuid.UUID
	otherProject    uuid.UUID
	admin           string
	agent           string
	agent2          string
	viewer          string
	agentCredential uuid.UUID
	reporter        string
	objects         *s3store.Store
	hooks           *webhooks.Manager
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("QUAL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("QUAL_TEST_DATABASE_URL not configured; integration tests require a migrated throwaway database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: pool, project: uuid.New(), otherProject: uuid.New()}
	t.Cleanup(func() { f.server.Close(); pool.Close() })
	ws := uuid.New()
	slug := "t" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `INSERT INTO qual.workspaces(id,slug,name) VALUES($1,$2,$3)`, ws, slug, "Integration Test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO qual.projects(id,workspace_id,key,name) VALUES($1,$2,'ENG','Engineering'),($3,$2,'OTHER','Other Project')`, f.project, ws, f.otherProject); err != nil {
		t.Fatal(err)
	}

	makePrincipal := func(kind, name, role string, project *uuid.UUID, scopes []string) (string, uuid.UUID) {
		t.Helper()
		principal, member, credential := uuid.New(), uuid.New(), uuid.New()
		tok, hash, err := identity.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO qual.principals(id,workspace_id,kind,display_name) VALUES($1,$2,$3,$4)`, principal, ws, kind, name); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO qual.memberships(id,workspace_id,principal_id,project_id,role) VALUES($1,$2,$3,$4,$5)`, member, ws, principal, project, role); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
			VALUES($1,$2,$3,$4,$5,$6)`, credential, ws, principal, hash[:], scopes, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return tok, credential
	}
	f.admin, _ = makePrincipal("human", "Admin", "workspace_admin", nil, []string{"work:read", "work:write", "work:claim", "work:review", "audit:read", "test:read", "test:write", "artifact:read", "artifact:write", "archive:export", "webhook:manage", "access:manage"})
	f.agent, f.agentCredential = makePrincipal("agent", "Agent A", "project_member", &f.project, []string{"work:read", "work:write", "work:claim", "test:read", "test:write", "artifact:read", "artifact:write"})
	f.agent2, _ = makePrincipal("agent", "Agent B", "project_member", &f.project, []string{"work:read", "work:write", "work:claim", "test:read", "test:write", "artifact:read", "artifact:write"})
	f.viewer, _ = makePrincipal("human", "Viewer", "project_viewer", &f.project, []string{"work:read", "work:write", "test:read", "artifact:read"})
	f.reporter, _ = makePrincipal("integration", "JUnit Reporter", "project_member", &f.project, []string{"test:read", "test:write", "artifact:read", "artifact:write"})

	auth := idpostgres.Repository{Pool: pool}
	objects, err := s3store.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.objects = objects
	var encryptionKey [32]byte
	if _, err := rand.Read(encryptionKey[:]); err != nil {
		t.Fatal(err)
	}
	f.hooks = &webhooks.Manager{Pool: pool, Identity: auth, Config: webhooks.Config{
		Key: encryptionKey, HasKey: true, AllowedHosts: []string{"example.com"}, AllowLoopbackTest: true,
	}}
	workSvc := workapp.Service{Identity: auth, Work: workpostgres.Repository{Pool: pool}, Roles: workpostgres.Repository{Pool: pool}, Completion: workpostgres.Repository{Pool: pool}, ReviewPolicy: workpostgres.Repository{Pool: pool}, AcceptanceHistory: workpostgres.Repository{Pool: pool}, WorkReviewPolicy: workpostgres.Repository{Pool: pool}}
	var eventService *eventapp.Service
	var eventSchema bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('qual.external_events') IS NOT NULL").Scan(&eventSchema); err != nil {
		t.Fatal(err)
	}
	if eventSchema {
		eventService = &eventapp.Service{Identity: auth, Events: eventpg.Repository{Pool: pool}}
	}
	f.server = httptest.NewServer(httpapi.NewHandler(&httpapi.Dependencies{
		Identity: auth, Browser: auth, WebOrigin: "http://127.0.0.1:4173",
		Work:         workSvc,
		Context:      &contextapp.Service{Identity: auth, Work: workSvc, Reader: contextpg.Repository{Pool: pool}},
		Runs:         runapp.Service{Identity: auth, Runs: runpg.Repository{Pool: pool}},
		Events:       eventService,
		Access:       identity.AccessService{Identity: auth, Repository: auth},
		Tests:        testingapp.Service{Identity: auth, Tests: testingpostgres.Repository{Pool: pool}},
		Evidence:     evidenceapp.Service{Identity: auth, Records: evidencepg.Repository{Pool: pool, Objects: objects}, Threads: evidencepg.Repository{Pool: pool}, Objects: objects},
		Webhooks:     f.hooks,
		MetricsToken: strings.Repeat("m", 40),
		Ready: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return err
			}
			var v int
			if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version
             WHERE is_applied=true`).Scan(&v); err != nil {
				return err
			}
			if v < 12 || v > 17 {
				return errors.New("schema version mismatch")
			}
			if objects != nil {
				return objects.Check(ctx)
			}
			return nil
		},
	}))
	return f
}

func (f *fixture) url(project uuid.UUID) string {
	return fmt.Sprintf("%s/api/v1/projects/%s/work-items", f.server.URL, project)
}

func request(t *testing.T, method, url, token, key, ifMatch string, body any) (int, map[string]any, http.Header) {
	t.Helper()
	var input io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, input)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatalf("JSON response (%d): %v", res.StatusCode, err)
	}
	if res.Header.Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID")
	}
	return res.StatusCode, data, res.Header
}

func create(t *testing.T, f *fixture, token, key string) map[string]any {
	t.Helper()
	status, data, _ := request(t, "POST", f.url(f.project), token, key, "", map[string]any{
		"kind": "task", "title": "Implement authentication", "priority": "high", "acceptance_criteria": []string{"No access without token"},
	})
	if status != 201 {
		t.Fatalf("create status=%d, response=%v", status, data)
	}
	return data
}

func TestWorkItemSecurityIdempotencyAndReview(t *testing.T) {
	f := setup(t)
	if status, _, _ := request(t, "GET", f.url(f.project), "", "", "", nil); status != 401 {
		t.Fatalf("anonymous=%d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject), f.agent, "", "", nil); status != 404 {
		t.Fatalf("cross-project=%d", status)
	}
	if status, _, _ := request(t, "POST", f.url(f.project), f.viewer, "forbidden-viewer-write", "", map[string]any{"kind": "task", "title": "Should fail"}); status != 403 {
		t.Fatalf("viewer write=%d", status)
	}
	item := create(t, f, f.agent, "unique-create-token")
	id, err := uuid.Parse(item["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if item["version"].(float64) != 1 {
		t.Fatalf("initial version %v", item["version"])
	}
	status, replayed, _ := request(t, "POST", f.url(f.project), f.agent, "unique-create-token", "", map[string]any{
		"kind": "task", "title": "Implement authentication", "priority": "high", "acceptance_criteria": []string{"No access without token"},
	})
	if status != 201 || replayed["id"] != item["id"] {
		t.Fatalf("idempotent replay status=%d result=%v", status, replayed)
	}
	if status, _, _ := request(t, "POST", f.url(f.project), f.agent, "unique-create-token", "", map[string]any{"kind": "task", "title": "Different data"}); status != 409 {
		t.Fatalf("idempotency collision=%d", status)
	}
	workURL := f.url(f.project) + "/" + id.String()
	if status, _, _ := request(t, "GET", workURL, f.viewer, "", "", nil); status != 200 {
		t.Fatalf("viewer read=%d", status)
	}
	if status, _, _ := request(t, "GET", workURL, f.agent, "", "", nil); status != 200 {
		t.Fatalf("agent read=%d", status)
	}
	if status, _, _ := request(t, "PATCH", workURL, f.agent, "stale-version-patch", "\"2\"", map[string]any{"title": "Changed"}); status != 409 {
		t.Fatalf("stale PATCH=%d", status)
	}
	if status, _, _ := request(t, "PATCH", workURL, f.agent, "missing-precondition", "", map[string]any{"title": "Changed"}); status != 428 {
		t.Fatalf("no If-Match=%d", status)
	}
	if status, _, _ := request(t, "GET", f.url(f.otherProject)+"/"+id.String(), f.agent, "", "", nil); status != 404 {
		t.Fatalf("cross-project IDOR=%d", status)
	}
	current := int64(1)
	for i, next := range []string{"todo", "in_progress", "in_review"} {
		status, updated, _ := request(t, "POST", workURL+"/transitions", f.agent, fmt.Sprintf("transition-%d", i), "", map[string]any{
			"status": next, "expected_version": current,
		})
		if status != 200 {
			t.Fatalf("transition %s: status=%d data=%v", next, status, updated)
		}
		current++
	}
	if status, _, _ := request(t, "POST", workURL+"/transitions", f.agent, "agent-approves", "", map[string]any{"status": "done", "expected_version": current}); status != 403 {
		t.Fatalf("agent reviewed=%d", status)
	}
	// Goose13 refuses even privileged Human attempts to bypass explicit acceptance.
	if status, _, _ := request(t, "POST", workURL+"/transitions", f.admin, "human-review-legacy-denied", "", map[string]any{"status": "done", "expected_version": current}); status != 422 {
		t.Fatalf("legacy Done Gate bypass should be rejected, got %d", status)
	}
	completeWithIndependentEvidence(t, f, id.String(), f.admin, "phase2-verified-done")
	if status, _, _ := request(t, "GET", workURL, f.agent, "", "", nil); status != 200 {
		t.Fatalf("get after completion=%d", status)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE qual.credentials SET revoked_at=now() WHERE id=$1`, f.agentCredential); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := request(t, "GET", workURL, f.agent, "", "", nil); status != 401 {
		t.Fatalf("revoked agent=%d", status)
	}

	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE entity_id=$1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("audit count %d want 6 (create+3 transitions+Reviewer+acceptance)", count)
	}
	// The verified Done Gate also produces real TestCase/Run/Attempt and
	// Coverage events. Compare complete project audit/activity, not just the
	// six WorkItem-scoped records, while preserving entity-specific checks.
	var projectAuditCount int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE project_id=$1`, f.project).Scan(&projectAuditCount); err != nil {
		t.Fatal(err)
	}
	if projectAuditCount <= count {
		t.Fatalf("missing real test evidence audit: project=%d work=%d", projectAuditCount, count)
	}
	activityURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/activity"
	auditURL := f.server.URL + "/api/v1/projects/" + f.project.String() + "/audit"
	if status, data, _ := request(t, "GET", activityURL, f.viewer, "", "", nil); status != 200 || len(data["items"].([]any)) != projectAuditCount {
		t.Fatalf("activity viewer status=%d data=%v", status, data)
	}
	if status, _, _ := request(t, "GET", auditURL, f.viewer, "", "", nil); status != 403 {
		t.Fatalf("audit viewer must be forbidden, got %d", status)
	}
	if status, data, _ := request(t, "GET", auditURL, f.admin, "", "", nil); status != 200 || len(data["items"].([]any)) != projectAuditCount {
		t.Fatalf("audit admin status=%d data=%v", status, data)
	}
	for _, table := range []string{"activity_records", "outbox_events"} {
		var found int
		// Tables are code-controlled identifiers, not user-supplied.
		err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM qual."+table+" WHERE entity_id=$1", id).Scan(&found)
		if table == "outbox_events" {
			err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.outbox_events WHERE aggregate_id=$1`, id).Scan(&found)
		}
		if err != nil {
			t.Fatal(err)
		}
		if found != count {
			t.Fatalf("%s count=%d audit=%d", table, found, count)
		}
	}
}

func TestConcurrentClaimSingleWinner(t *testing.T) {
	f := setup(t)
	item := create(t, f, f.admin, "create-for-race")
	workURL := f.url(f.project) + "/" + item["id"].(string) + "/claim"
	results := make([]int, 2)
	tokens := []string{f.agent, f.agent2}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range tokens {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			code, _, _ := request(t, "POST", workURL, tokens[index], fmt.Sprintf("claim-race-%d", index), "", map[string]any{"expected_version": 1})
			results[index] = code
		}(i)
	}
	close(start)
	wg.Wait()
	winners, conflicts := 0, 0
	for _, code := range results {
		if code == 200 {
			winners++
		}
		if code == 409 {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("claim race outcomes: %v", results)
	}
	itemID, err := uuid.Parse(item["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := f.pool.QueryRow(context.Background(), `SELECT version FROM qual.work_items WHERE id=$1`, itemID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("version after race=%d", version)
	}
}

func TestCommentsDependenciesAndCycleProtection(t *testing.T) {
	f := setup(t)
	a := create(t, f, f.admin, "relation-source")
	b := create(t, f, f.admin, "relation-target")
	aID := a["id"].(string)
	bID := b["id"].(string)
	aURL := f.url(f.project) + "/" + aID
	bURL := f.url(f.project) + "/" + bID

	status, updated, _ := request(t, "POST", aURL+"/comments", f.agent, "comment-on-source", "", map[string]any{
		"body": "Agent found missing documentation", "expected_version": 1,
	})
	if status != 201 || updated["version"] != float64(2) {
		t.Fatalf("add comment status=%d data=%v", status, updated)
	}
	if status, _, _ = request(t, "POST", aURL+"/comments", f.agent, "comment-on-source", "", map[string]any{
		"body": "Agent found missing documentation", "expected_version": 1,
	}); status != 201 {
		t.Fatalf("comment replay=%d", status)
	}
	status, comments, _ := request(t, "GET", aURL+"/comments", f.agent, "", "", nil)
	if status != 200 || len(comments["items"].([]any)) != 1 {
		t.Fatalf("comments=%d %v", status, comments)
	}
	status, updated, _ = request(t, "POST", aURL+"/dependencies", f.agent, "link-source-target", "", map[string]any{
		"to_item_id": bID, "expected_version": 2,
	})
	if status != 201 || updated["version"] != float64(3) {
		t.Fatalf("link dependency=%d %v", status, updated)
	}
	status, links, _ := request(t, "GET", aURL+"/dependencies", f.viewer, "", "", nil)
	if status != 200 || len(links["items"].([]any)) != 1 {
		t.Fatalf("dependencies=%d %v", status, links)
	}
	if status, _, _ := request(t, "POST", bURL+"/dependencies", f.agent, "reverse-makes-cycle", "", map[string]any{
		"to_item_id": aID, "expected_version": 1,
	}); status != 422 {
		t.Fatalf("reverse cycle=%d", status)
	}
	if status, _, _ := request(t, "POST", aURL+"/dependencies", f.agent, "self-cycle-try", "", map[string]any{
		"to_item_id": aID, "expected_version": 3,
	}); status != 422 {
		t.Fatalf("self link=%d", status)
	}
	if status, _, _ := request(t, "POST", bURL+"/dependencies", f.agent, "duplicate-work-link", "", map[string]any{
		"to_item_id": aID, "expected_version": 1,
	}); status != 422 {
		t.Fatalf("reverse cycle repeated=%d", status)
	}
	if status, _, _ := request(t, "POST", bURL+"/dependencies", f.agent, "cross-project-dependency", "", map[string]any{
		"to_item_id": f.otherProject.String(), "expected_version": 1,
	}); status != 404 {
		t.Fatalf("missing dependency=%d", status)
	}
	var audit, activity, outbox int
	id := uuid.MustParse(aID)
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.audit_records WHERE entity_id=$1`, id).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.activity_records WHERE entity_id=$1`, id).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.outbox_events WHERE aggregate_id=$1`, id).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if audit != 3 || activity != 3 || outbox != 3 {
		t.Fatalf("audit/activity/outbox=%d/%d/%d", audit, activity, outbox)
	}
}

func TestConcurrentReverseDependencies(t *testing.T) {
	f := setup(t)
	a := create(t, f, f.admin, "cycle-race-source")
	b := create(t, f, f.admin, "cycle-race-target")
	aURL := f.url(f.project) + "/" + a["id"].(string) + "/dependencies"
	bURL := f.url(f.project) + "/" + b["id"].(string) + "/dependencies"
	start := make(chan struct{})
	outcomes := make([]int, 2)
	var wg sync.WaitGroup
	urls := []string{aURL, bURL}
	targets := []string{b["id"].(string), a["id"].(string)}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			status, _, _ := request(t, "POST", urls[index], f.agent, fmt.Sprintf("cycle-race-%d", index), "", map[string]any{
				"to_item_id": targets[index], "expected_version": 1,
			})
			outcomes[index] = status
		}(i)
	}
	close(start)
	wg.Wait()
	created, denied := 0, 0
	for _, status := range outcomes {
		if status == 201 {
			created++
		}
		if status == 422 {
			denied++
		}
	}
	if created != 1 || denied != 1 {
		t.Fatalf("reverse link outcomes=%v, want 201 and 422", outcomes)
	}
}

func TestAuditHistoryIsAppendOnly(t *testing.T) {
	f := setup(t)
	item := create(t, f, f.agent, "audit-immutable-test")
	id := uuid.MustParse(item["id"].(string))
	_, err := f.pool.Exec(context.Background(), `UPDATE qual.audit_records SET operation='tampered' WHERE entity_id=$1`, id)
	if err == nil {
		t.Fatal("audit UPDATE must be rejected by database trigger")
	}
	_, err = f.pool.Exec(context.Background(), `DELETE FROM qual.audit_records WHERE entity_id=$1`, id)
	if err == nil {
		t.Fatal("audit DELETE must be rejected by database trigger")
	}
}

func TestPatchEtagsAndStrictJSON(t *testing.T) {
	f := setup(t)
	item := create(t, f, f.admin, "patch-safety-creation")
	url := f.url(f.project) + "/" + item["id"].(string)
	status, updated, headers := request(t, "PATCH", url, f.agent, "patch-title-safety", "\"1\"", map[string]any{"title": "Updated title"})
	if status != 200 || updated["version"] != float64(2) || headers.Get("ETag") != "\"2\"" {
		t.Fatalf("PATCH status=%d version=%v ETag=%s", status, updated["version"], headers.Get("ETag"))
	}
	status, replayed, _ := request(t, "PATCH", url, f.agent, "patch-title-safety", "\"1\"", map[string]any{"title": "Updated title"})
	if status != 200 || replayed["version"] != float64(2) {
		t.Fatalf("PATCH replay status=%d data=%v", status, replayed)
	}
	if status, _, _ := request(t, "PATCH", url, f.agent, "patch-title-safety", "\"1\"", map[string]any{"title": "Different title"}); status != 409 {
		t.Fatalf("PATCH idempotency conflict status=%d", status)
	}
	if status, _, _ := request(t, "PATCH", url, f.agent, "stale-new-key-0001", "\"1\"", map[string]any{"title": "Old version"}); status != 409 {
		t.Fatalf("stale version status=%d", status)
	}
	if status, _, _ := request(t, "PATCH", url, f.agent, "unexpected-field", "\"2\"", map[string]any{"actor_id": "fake", "title": "Spoof"}); status != 400 {
		t.Fatalf("unknown JSON field status=%d", status)
	}
	if status, _, _ := request(t, "POST", f.url(f.project), f.admin, "large-payload-attempt", "", map[string]any{
		"kind": "task", "title": "Payload", "description": strings.Repeat("A", 70000),
	}); status != 413 {
		t.Fatalf("oversize body status=%d", status)
	}
}

func TestBugAndPagination(t *testing.T) {
	f := setup(t)
	status, bug, _ := request(t, "POST", f.url(f.project), f.agent, "create-bug-0001", "", map[string]any{
		"kind": "bug", "title": "Login fails", "priority": "urgent",
		"bug_details": map[string]any{"severity": "critical", "reproduction_steps": "Enter expired token"},
	})
	if status != 201 {
		t.Fatalf("bug create=%d, data=%v", status, bug)
	}
	if bug["bug_details"] == nil {
		t.Fatalf("bug metadata missing: %v", bug)
	}
	create(t, f, f.admin, "create-task-pagination")
	pageStatus, page, _ := request(t, "GET", f.url(f.project)+"?limit=1", f.agent, "", "", nil)
	if pageStatus != 200 || page["next_cursor"] == nil {
		t.Fatalf("pagination=%d, data=%v", pageStatus, page)
	}
	if status, _, _ := request(t, "GET", f.url(f.project)+"?limit=1&cursor="+page["next_cursor"].(string), f.agent, "", "", nil); status != 200 {
		t.Fatalf("page two=%d", status)
	}
}
