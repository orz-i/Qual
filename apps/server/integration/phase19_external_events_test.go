//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	identity "qual/internal/identity/application"
)

func TestQual30ExternalEventAppendOnlyIdentityScopeAndTimeline(t *testing.T) {
	if !strings.Contains(os.Getenv("QUAL_TEST_DATABASE_URL"), "/qual30_event_") {
		t.Skip("requires disposable qual30_event_* Goose17 PostgreSQL")
	}
	f := setup(t)
	ctx := context.Background()
	work := create(t, f, f.agent, "qual30-event-target")
	itemID := work["id"].(string)
	base := fmt.Sprintf("%s/api/v1/projects/%s", f.server.URL, f.project)
	threadKey := "example/project/pull/91"
	s, linked, _ := request(t, "POST", threadBase(f, itemID), f.agent, "qual30-link-thread", "", threadInput(threadKey, 1))
	if s != 201 {
		t.Fatalf("Thread source link failed %d %v", s, linked)
	}
	threadID := linked["thread"].(map[string]any)["id"].(string)
	eventURL := base + "/external-events"
	digest := sha256.Sum256([]byte("synthetic untrusted event body; kept outside Qual"))
	eventID := "delivery-test-qual30-1"
	// Deliberately retain nanosecond precision to prove PostgreSQL
	// microsecond normalization keeps natural-key replays idempotent.
	ts := time.Now().UTC().Add(-time.Hour)
	event := map[string]any{
		"work_item_id": itemID, "thread_id": threadID, "source": "github",
		"external_event_id": eventID, "event_type": "pull_request.review_submitted",
		"occurred_at": ts, "payload_sha256": hex.EncodeToString(digest[:]),
		"summary": "External review received, not a Qual Reviewer approval",
	}
	if code, _, _ := request(t, "POST", eventURL, f.agent, "qual30-old-scope-denied", "", event); code != 403 {
		t.Fatalf("existing Agent inherited event:write without grant: HTTP %d", code)
	}
	grant := func(token, key string) {
		t.Helper()
		hash, err := identity.HashToken(token)
		if err != nil {
			t.Fatal(err)
		}
		var principal uuid.UUID
		if err := f.pool.QueryRow(ctx, "SELECT principal_id FROM qual.credentials WHERE token_sha256=$1", hash[:]).Scan(&principal); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET scopes=array_append(scopes,'event:write') WHERE token_sha256=$1", hash[:]); err != nil {
			t.Fatal(err)
		}
		code, d, _ := request(t, "PUT", base+"/access/"+principal.String()+"/permissions/event:write",
			f.admin, key, "", map[string]any{"effect": "allow", "expected_version": 0, "reason": "Real synthetic isolated Event adapter grant, not production"})
		if code != 200 {
			t.Fatalf("project Event grant failed %d %v", code, d)
		}
	}
	grant(f.agent, "qual30-grant-agent")
	grant(f.reporter, "qual30-grant-integration")
	if code, _, _ := request(t, "POST", eventURL, f.viewer, "qual30-viewer-no-write", "", event); code != 403 {
		t.Fatalf("viewer wrote event: HTTP%d", code)
	}
	if code, _, _ := request(t, "POST", eventURL, "", "qual30-anonymous", "", event); code != 401 {
		t.Fatalf("anonymous accepted: HTTP%d", code)
	}
	bad := func(key string, mutate func(map[string]any), want int) {
		t.Helper()
		raw, _ := json.Marshal(event)
		data := map[string]any{}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		mutate(data)
		code, resp, _ := request(t, "POST", eventURL, f.agent, key, "", data)
		if code != want {
			t.Fatalf("invalid Event %s HTTP%d want%d data=%v", key, code, want, resp)
		}
	}
	bad("qual30-foreign-work", func(e map[string]any) { e["work_item_id"] = uuid.NewString() }, 404)
	bad("qual30-foreign-thread", func(e map[string]any) { e["thread_id"] = uuid.NewString() }, 404)
	foreignURL := fmt.Sprintf("%s/api/v1/projects/%s/external-events", f.server.URL, f.otherProject)
	if code, _, _ := request(t, "POST", foreignURL, f.agent, "qual30-foreign-project-denied", "", event); code != 404 {
		t.Fatalf("foreign Project Event identity exposed: HTTP%d", code)
	}
	bad("qual30-secret-unsafe", func(e map[string]any) { e["summary"] = "Bearer secret-token" }, 422)
	bad("qual30-html-unsafe", func(e map[string]any) { e["summary"] = "<script>alert(1)</script>" }, 422)
	bad("qual30-NUL", func(e map[string]any) { e["summary"] = "unsafe\u0000" }, 422)
	bad("qual30-invalid-digest", func(e map[string]any) { e["payload_sha256"] = "fake" }, 422)
	bad("qual30-oversize", func(e map[string]any) { e["summary"] = strings.Repeat("a", 241) }, 422)
	bad("qual30-forged-reporter", func(e map[string]any) { e["reporter_principal_id"] = uuid.NewString() }, 400)
	bad("qual30-raw-payload", func(e map[string]any) { e["payload"] = "{\"password\":\"secret\"}" }, 400)
	bad("qual30-malformed-time", func(e map[string]any) { e["occurred_at"] = time.Now().Add(time.Hour) }, 422)

	code, created, _ := request(t, "POST", eventURL, f.agent, "qual30-event-first", "", event)
	if code != 201 || created["event_type"] != event["event_type"] || created["summary"] != event["summary"] {
		t.Fatalf("event create: HTTP%d %v", code, created)
	}
	eid := created["id"].(string)
	code, replayed, _ := request(t, "POST", eventURL, f.agent, "qual30-event-first", "", event)
	if code != 201 || replayed["id"] != eid {
		t.Fatalf("same idempotency key replay lost: HTTP%d %v", code, replayed)
	}
	code, replayed, _ = request(t, "POST", eventURL, f.agent, "qual30-event-second-key", "", event)
	if code != 201 || replayed["id"] != eid {
		t.Fatalf("natural dedup second key lost: HTTP%d %v", code, replayed)
	}
	// The HTTP request ledger and natural Event identity are two independent
	// immutable protections: same key with changed content is always 409.
	sameKeyModified := map[string]any{}
	copyRaw, _ := json.Marshal(event)
	_ = json.Unmarshal(copyRaw, &sameKeyModified)
	sameKeyModified["summary"] = "Changed summary must not replace original"
	if code, _, _ := request(t, "POST", eventURL, f.agent, "qual30-event-first", "", sameKeyModified); code != 409 {
		t.Fatalf("Idempotency-Key reuse with changed event accepted: HTTP%d", code)
	}
	bad("qual30-event-content-conflict", func(e map[string]any) {
		e["payload_sha256"] = strings.Repeat("b", 64)
	}, 409)
	bad("qual30-event-rebind-work", func(e map[string]any) { e["event_type"] = "pull_request.closed" }, 409)
	// Replays may not write an audit/outbox record or bump WorkItem.version.
	code, w, _ := request(t, "GET", f.url(f.project)+"/"+itemID, f.agent, "", "", nil)
	if code != 200 || w["version"] != float64(2) || w["status"] != "backlog" {
		t.Fatalf("external event modified business WorkItem: %d %v", code, w)
	}

	// Store later incoming events but preserve each source timestamp; older
	// occurred_at must never act as a business rollback.
	for j := 2; j <= 4; j++ {
		raw, _ := json.Marshal(event)
		e := map[string]any{}
		_ = json.Unmarshal(raw, &e)
		e["external_event_id"] = fmt.Sprintf("delivery-test-qual30-%d", j)
		e["occurred_at"] = ts.Add(time.Duration(4-j) * time.Hour * -1)
		code, _, _ := request(t, "POST", eventURL, f.agent, fmt.Sprintf("qual30-new-event-%d", j), "", e)
		if code != 201 {
			t.Fatalf("late Event %d was rejected: HTTP%d", j, code)
		}
	}
	pageURL := f.url(f.project) + "/" + itemID + "/external-events?limit=2"
	code, page, _ := request(t, "GET", pageURL, f.viewer, "", "", nil)
	if code != 200 || !page["has_more"].(bool) || len(page["items"].([]any)) != 2 {
		t.Fatalf("WorkItem timeline page1: HTTP%d %v", code, page)
	}
	cursor := page["next_cursor"].(string)
	code, next, _ := request(t, "GET", pageURL+"&cursor="+cursor, f.viewer, "", "", nil)
	if code != 200 || len(next["items"].([]any)) != 2 || next["has_more"].(bool) {
		t.Fatalf("WorkItem timeline page2: HTTP%d %v", code, next)
	}
	code, threadTimeline, _ := request(t, "GET", base+"/threads/"+threadID+"/external-events?limit=10", f.viewer, "", "", nil)
	if code != 200 || len(threadTimeline["items"].([]any)) != 4 {
		t.Fatalf("Thread timeline: HTTP%d %v", code, threadTimeline)
	}
	if code, _, _ := request(t, "GET", base+"/work-items/"+uuid.NewString()+"/external-events", f.agent, "", "", nil); code != 404 {
		t.Fatalf("missing WorkItem exposed: HTTP%d", code)
	}
	if code, _, _ := request(t, "GET", base+"/threads/"+uuid.NewString()+"/external-events", f.agent, "", "", nil); code != 404 {
		t.Fatalf("missing Thread exposed: HTTP%d", code)
	}
	if code, _, _ := request(t, "GET", pageURL+"&cursor="+uuid.NewString(), f.agent, "", "", nil); code != 404 {
		t.Fatalf("foreign cursor exposed: HTTP%d", code)
	}
	if code, _, _ := request(t, "GET", pageURL+"&limit=999", f.agent, "", "", nil); code != 400 {
		t.Fatalf("unbounded Event read accepted: HTTP%d", code)
	}
	if code, _, _ := request(t, "GET", base+"/work-items/"+itemID+"/external-events", f.reporter, "", "", nil); code != 403 {
		t.Fatalf("Event-only integration reader leaked WorkItem: HTTP%d", code)
	}
	// Integration reporter can be separately granted EventWrite, but it must
	// never inherit WorkRead or any authority to complete WorkItems.
	other := map[string]any{}
	raw, _ := json.Marshal(event)
	_ = json.Unmarshal(raw, &other)
	other["external_event_id"] = "integration-reporter-1"
	if code, _, _ := request(t, "POST", eventURL, f.reporter, "qual30-integration-only-granted", "", other); code != 201 {
		t.Fatalf("Integration event:write grant unusable HTTP%d", code)
	}
	if code, _, _ := request(t, "POST", f.url(f.project)+"/"+itemID+"/transitions", f.reporter, "qual30-reporter-cannot-do-work", "",
		map[string]any{"status": "todo", "expected_version": 2}); code != 403 {
		t.Fatalf("Event reporter changed WorkItem: HTTP%d", code)
	}

	var eventAudit, activity, outbox int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.audit_records WHERE project_id=$1 AND operation='external_event.recorded'", f.project).Scan(&eventAudit); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.activity_records WHERE project_id=$1 AND operation='external_event.recorded'", f.project).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM qual.outbox_events WHERE project_id=$1 AND event_type='external_event.recorded'", f.project).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if eventAudit != 5 || activity != 5 || outbox != 5 {
		t.Fatalf("non-atomic event provenance %d/%d/%d", eventAudit, activity, outbox)
	}
	var diff string
	if err := f.pool.QueryRow(ctx, "SELECT diff::text FROM qual.audit_records WHERE project_id=$1 AND operation='external_event.recorded' ORDER BY occurred_at LIMIT 1", f.project).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff, "External review received") || strings.Contains(diff, "synthetic untrusted") {
		t.Fatal("raw payload or display summary leaked to immutable Audit")
	}
	if _, err := f.pool.Exec(ctx, "UPDATE qual.external_events SET summary='forged history' WHERE id=$1", eid); err == nil {
		t.Fatal("Event fact was mutable via SQL")
	}
	// Concurrent distinct requests for one new natural key must serialize.
	raceRaw, _ := json.Marshal(event)
	race := map[string]any{}
	_ = json.Unmarshal(raceRaw, &race)
	race["external_event_id"] = "race-external-1"
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	ids := make([]string, 2)
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			c, r, _ := request(t, "POST", eventURL, f.agent, fmt.Sprintf("qual30-race-%d", k), "", race)
			statuses[k] = c
			if c == 201 {
				ids[k] = r["id"].(string)
			}
		}(k)
	}
	wg.Wait()
	if statuses[0] != 201 || statuses[1] != 201 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("parallel natural replay not serialized: %v %v", statuses, ids)
	}
	// Immutable old facts remain replayable after a legitimate unlink.
	// New events must refuse a formerly valid but now inactive link.
	status, removed, _ := request(t, "DELETE", threadBase(f, itemID)+"/"+threadID,
		f.agent, "qual30-unlink-historical-thread", `"2"`, nil)
	if status != 200 {
		t.Fatalf("removing source association: HTTP%d %v", status, removed)
	}
	status, replayAfterUnlink, _ := request(t, "POST", eventURL, f.agent,
		"qual30-event-replay-after-unlink", "", event)
	if status != 201 || replayAfterUnlink["id"] != eid {
		t.Fatalf("previously persisted event not safely replayable: %d %v", status, replayAfterUnlink)
	}
	bad("qual30-new-event-after-unlink", func(e map[string]any) {
		e["external_event_id"] = "delivery-new-after-unlink"
	}, 404)
	status, retained, _ := request(t, "GET", base+"/threads/"+threadID+"/external-events?limit=20",
		f.agent, "", "", nil)
	if status != 200 || len(retained["items"].([]any)) != 6 {
		t.Fatalf("unlink erased immutable historical external Events: %d %v", status, retained)
	}
	// Membership isolation: no cross-Project event timelines or natural source IDs.
	foreignBase := fmt.Sprintf("%s/api/v1/projects/%s/work-items/%s/external-events",
		f.server.URL, f.otherProject, itemID)
	if code, _, _ := request(t, "GET", foreignBase, f.agent, "", "", nil); code != 404 {
		t.Fatalf("cross-project Event timeline disclosed: HTTP%d", code)
	}
	// Explicit project denial must immediately defeat a still-valid
	// event:write credential; the scope and grant are both mandatory.
	var agentPrincipal uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT principal_id FROM qual.credentials WHERE id=$1", f.agentCredential).Scan(&agentPrincipal); err != nil {
		t.Fatal(err)
	}
	status, disabled, _ := request(t, "PUT", base+"/access/"+agentPrincipal.String()+"/permissions/event:write",
		f.admin, "qual30-deny-explicit-event-scope", "", map[string]any{
			"effect": "deny", "expected_version": 1, "reason": "Revoke one synthetic Event adapter grant"})
	if status != 200 {
		t.Fatalf("Event Project deny failed: HTTP%d %v", status, disabled)
	}
	if code, _, _ := request(t, "POST", eventURL, f.agent, "qual30-event-project-denied", "", event); code != 403 {
		t.Fatalf("denied Project event writer retained EventWrite: HTTP%d", code)
	}
	// Changed permissions/revocation are checked for every HTTP request.
	if _, err := f.pool.Exec(ctx, "UPDATE qual.credentials SET revoked_at=now() WHERE id=$1", f.agentCredential); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := request(t, "POST", eventURL, f.agent, "qual30-revoked-writer", "", event); c != 401 {
		t.Fatalf("revoked Event writer accepted: %d", c)
	}
}
