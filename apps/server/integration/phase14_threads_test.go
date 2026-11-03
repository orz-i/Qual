//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"

	evidencepg "qual/internal/evidence/infrastructure/postgres"
)

func threadBase(f *fixture, itemID string) string {
	return fmt.Sprintf("%s/api/v1/projects/%s/work-items/%s/threads", f.server.URL, f.project, itemID)
}
func threadInput(id string, version int) map[string]any {
	return map[string]any{
		"provider": "github", "thread_kind": "pull_request",
		"external_thread_id": id,
		"url":                "https://github.com/example/project/pull/91",
		"expected_version":   version,
	}
}
func assertCode(t *testing.T, got, want int, detail map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP got=%d want=%d body=%v", got, want, detail)
	}
}

func TestThreadLinkLegacyCompatibilityAuthorizationAndHistory(t *testing.T) {
	f := setup(t)
	work := create(t, f, f.agent, "thread-first-work")
	second := create(t, f, f.agent, "thread-second-work")
	third := create(t, f, f.agent, "thread-third-work")
	workID, secondID, thirdID := work["id"].(string), second["id"].(string), third["id"].(string)
	base := threadBase(f, workID)
	threadKey := "example/project/pull/91"
	input := threadInput(threadKey, 1)
	oldRef := map[string]any{
		"target_kind": "work_item", "target_id": workID,
		"provider": "github", "kind": "pull_request", "external_id": threadKey,
		"url": input["url"],
	}
	st, ref, _ := request(t, "POST",
		fmt.Sprintf("%s/api/v1/projects/%s/external-references", f.server.URL, f.project),
		f.agent, "thread-old-real-reference", "", oldRef)
	assertCode(t, st, 201, ref)
	input["source_reference_id"] = ref["id"]

	st, data, _ := request(t, "POST", base, "", "thread-no-auth", "", input)
	assertCode(t, st, 401, data)
	st, data, _ = request(t, "POST", base, f.viewer, "thread-viewer-denied", "", input)
	assertCode(t, st, 403, data)
	st, data, _ = request(t, "POST", base, f.reporter, "thread-integration-denied", "", input)
	assertCode(t, st, 403, data)
	st, data, _ = request(t, "POST", base, f.agent, "", "", input)
	assertCode(t, st, 428, data)
	invalid := threadInput(threadKey, 1)
	invalid["url"] = "https://127.0.0.1/private"
	st, data, _ = request(t, "POST", base, f.agent, "thread-bad-url", "", invalid)
	assertCode(t, st, 422, data)
	for i, badURL := range []string{
		"https://service.corp/path", "https://somehost.lan/thread",
		"https://hidden.onion/thread", "https://internal.example/thread",
	} {
		invalid := threadInput(threadKey, 1)
		invalid["url"] = badURL
		st, data, _ = request(t, "POST", base, f.agent, fmt.Sprintf("thread-untrusted-public-url-%d", i), "", invalid)
		assertCode(t, st, 422, data)
	}
	for i, bareOrInvalid := range []string{"15", "example/project/15", "example//pull/91", "example/project/issues/91"} {
		invalid := threadInput(bareOrInvalid, 1)
		st, data, _ = request(t, "POST", base, f.agent, fmt.Sprintf("thread-unscoped-external-id-%d", i), "", invalid)
		assertCode(t, st, 422, data)
	}

	st, linked, header := request(t, "POST", base, f.agent, "thread-link-old-source", "", input)
	assertCode(t, st, 201, linked)
	if linked["work_item_version"] != float64(2) || header.Get("ETag") != "\"2\"" {
		t.Fatalf("CAS version not advanced: %v %v", linked, header)
	}
	thread := linked["thread"].(map[string]any)
	link := linked["link"].(map[string]any)
	if link["source_reference_id"] != ref["id"] || link["created_by"] == "" || thread["external_thread_id"] != threadKey {
		t.Fatalf("source provenance missing: %v", linked)
	}
	threadID, firstLinkID := thread["id"].(string), link["id"].(string)

	st, replay, _ := request(t, "POST", base, f.agent, "thread-link-old-source", "", input)
	assertCode(t, st, 201, replay)
	if replay["link"].(map[string]any)["id"] != firstLinkID {
		t.Fatalf("replay appended link: %v", replay)
	}
	input["expected_version"] = 2
	st, data, _ = request(t, "POST", base, f.agent, "thread-link-old-source", "", input)
	assertCode(t, st, 409, data) // Same idempotency key with distinct payload.
	input["expected_version"] = 1
	st, data, _ = request(t, "POST", base, f.agent, "thread-stale-link", "", input)
	assertCode(t, st, 409, data)

	st, page, _ := request(t, "GET", base, f.viewer, "", "", nil)
	assertCode(t, st, 200, page)
	if len(page["items"].([]any)) != 1 {
		t.Fatalf("work item link not readable: %v", page)
	}
	reverse := fmt.Sprintf("%s/api/v1/projects/%s/threads/%s/work-items", f.server.URL, f.project, threadID)
	st, page, _ = request(t, "GET", reverse, f.viewer, "", "", nil)
	assertCode(t, st, 200, page)
	if len(page["items"].([]any)) != 1 {
		t.Fatalf("reverse lookup missing: %v", page)
	}

	// A second real WorkItem may explicitly link to the same external Thread.
	secondInput := threadInput(threadKey, 1)
	delete(secondInput, "url") // Omitting an optional URL must not rebind an existing Thread.
	st, secondLinked, _ := request(t, "POST", threadBase(f, secondID), f.agent, "thread-second-link", "", secondInput)
	assertCode(t, st, 201, secondLinked)
	if secondLinked["thread"].(map[string]any)["id"] != threadID {
		t.Fatalf("source uniqueness lost: %v", secondLinked)
	}
	st, page, _ = request(t, "GET", reverse+"?limit=1", f.viewer, "", "", nil)
	assertCode(t, st, 200, page)
	if len(page["items"].([]any)) != 1 || page["next_cursor"] == nil {
		t.Fatalf("first pagination page incorrect: %v", page)
	}
	st, page, _ = request(t, "GET", reverse+"?limit=1&cursor="+page["next_cursor"].(string), f.viewer, "", "", nil)
	assertCode(t, st, 200, page)
	if len(page["items"].([]any)) != 1 || page["next_cursor"] != nil {
		t.Fatalf("second pagination page incorrect: %v", page)
	}
	st, data, _ = request(t, "GET", reverse+"?limit=101", f.viewer, "", "", nil)
	assertCode(t, st, 400, data)

	// Same ID and conflicting external URL is rejected; old identities never change.
	conflict := threadInput(threadKey, 1)
	conflict["url"] = "https://github.com/malicious/other/pull/91"
	st, data, _ = request(t, "POST", threadBase(f, thirdID), f.agent, "thread-source-conflict", "", conflict)
	assertCode(t, st, 409, data)
	// A saved old reference cannot be reassigned to a different WorkItem.
	mismatch := threadInput(threadKey, 2)
	mismatch["source_reference_id"] = ref["id"]
	st, data, _ = request(t, "POST", threadBase(f, secondID), f.agent, "thread-source-spoof", "", mismatch)
	assertCode(t, st, 409, data)

	otherBase := fmt.Sprintf("%s/api/v1/projects/%s/work-items", f.server.URL, f.otherProject)
	st, otherWork, _ := request(t, "POST", otherBase, f.admin, "thread-cross-real-item", "", map[string]any{
		"kind": "task", "title": "Other project thread test",
	})
	assertCode(t, st, 201, otherWork)
	st, data, _ = request(t, "POST", threadBase(f, otherWork["id"].(string)), f.agent, "thread-other-workitem-spoof", "", input)
	assertCode(t, st, 404, data)
	st, data, _ = request(t, "GET", fmt.Sprintf("%s/api/v1/projects/%s/threads/%s/work-items", f.server.URL, f.otherProject, threadID), f.agent, "", "", nil)
	assertCode(t, st, 404, data)
	st, data, _ = request(t, "GET", fmt.Sprintf("%s/api/v1/projects/%s/threads/%s/work-items", f.server.URL, f.otherProject, threadID), f.admin, "", "", nil)
	assertCode(t, st, 404, data)

	unlinkURL := base + "/" + threadID
	st, data, _ = request(t, http.MethodDelete, unlinkURL, f.agent, "thread-unlink-once", "", nil)
	assertCode(t, st, 428, data)
	st, unlinked, _ := request(t, http.MethodDelete, unlinkURL, f.agent, "thread-unlink-once", "\"2\"", nil)
	assertCode(t, st, 200, unlinked)
	if unlinked["work_item_version"] != float64(3) ||
		unlinked["link"].(map[string]any)["removed_at"] == nil ||
		unlinked["link"].(map[string]any)["removed_by"] == nil {
		t.Fatalf("unlink history missing: %v", unlinked)
	}
	st, repeat, _ := request(t, http.MethodDelete, unlinkURL, f.agent, "thread-unlink-once", "\"2\"", nil)
	assertCode(t, st, 200, repeat)
	if repeat["link"].(map[string]any)["id"] != firstLinkID {
		t.Fatalf("unlink replay changed history: %v", repeat)
	}
	st, page, _ = request(t, "GET", base, f.viewer, "", "", nil)
	assertCode(t, st, 200, page)
	if len(page["items"].([]any)) != 0 {
		t.Fatalf("removed link remained active: %v", page)
	}

	relink := threadInput(threadKey, 3)
	relink["source_reference_id"] = ref["id"]
	st, relinked, _ := request(t, "POST", base, f.agent, "thread-relink-historical", "", relink)
	assertCode(t, st, 201, relinked)
	if relinked["link"].(map[string]any)["id"] == firstLinkID || relinked["work_item_version"] != float64(4) {
		t.Fatalf("not a new historical link: %v", relinked)
	}
	var history, active, priorReferences, auditEvents, outboxEvents int
	ctx := context.Background()
	err := f.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE removed_at IS NULL)
 FROM qual.work_thread_links WHERE project_id=$1 AND thread_id=$2 AND work_item_id=$3`,
		f.project, threadID, workID).Scan(&history, &active)
	if err != nil || history != 2 || active != 1 {
		t.Fatalf("link history lost: %d %d %v", history, active, err)
	}
	err = f.pool.QueryRow(ctx, `SELECT count(*) FROM qual.external_references WHERE id=$1 AND project_id=$2 AND target_id=$3`,
		ref["id"], f.project, workID).Scan(&priorReferences)
	if err != nil || priorReferences != 1 {
		t.Fatalf("legacy ExternalReference changed: %d %v", priorReferences, err)
	}
	err = f.pool.QueryRow(ctx, `SELECT count(*), count(o.id)
 FROM qual.audit_records a JOIN qual.outbox_events o ON o.audit_id=a.id
 WHERE a.project_id=$1 AND a.entity_id=$2 AND a.operation IN ('work_thread.linked','work_thread.unlinked')`,
		f.project, workID).Scan(&auditEvents, &outboxEvents)
	if err != nil || auditEvents != 3 || outboxEvents != 3 {
		t.Fatalf("non-atomic/audited writes count=%d outbox=%d err=%v", auditEvents, outboxEvents, err)
	}
	// Direct SQL writes must not bypass the generic scoped-ID or GitHub rules.
	for _, rawID := range []string{"15", "example/project/15", "example//pull/91"} {
		_, err := f.pool.Exec(ctx, `INSERT INTO qual.external_threads
(id,workspace_id,project_id,provider,thread_kind,external_thread_id,created_by)
SELECT $1,p.workspace_id,p.id,'github','pull_request',$2,$3
FROM qual.projects p WHERE p.id=$4`,
			uuid.New(), rawID, link["created_by"], f.project)
		if err == nil {
			t.Fatalf("unscoped SQL thread ID persisted: %q", rawID)
		}
	}
	// Database-level immutability: a principal cannot overwrite or delete link history.
	if _, err := f.pool.Exec(ctx, `DELETE FROM qual.work_thread_links WHERE id=$1`, firstLinkID); err == nil {
		t.Fatal("historical link was physically deleted")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE qual.work_thread_links SET removed_by=NULL,removed_at=NULL WHERE id=$1`, firstLinkID); err == nil {
		t.Fatal("historical unlink was erased")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE qual.external_threads SET external_thread_id=$2 WHERE id=$1`, threadID, "attacker/rewritten"); err == nil {
		t.Fatal("immutable thread source was rewritten")
	}
}

func TestThreadConcurrentReplayAndProjectSourceUniqueness(t *testing.T) {
	f := setup(t)
	a := create(t, f, f.agent, "thread-concurrent-a")
	b := create(t, f, f.agent, "thread-concurrent-b")
	input := map[string]any{
		"provider": "slack", "thread_kind": "discussion",
		"external_thread_id": "workspace/channel/message-12", "expected_version": 1,
	}
	type outcome struct {
		status int
		data   map[string]any
	}
	results := make([]outcome, 3)
	var group sync.WaitGroup
	group.Add(3)
	for i := range results {
		i := i
		go func() {
			defer group.Done()
			id, key := a["id"].(string), "thread-concurrent-replay"
			if i == 2 {
				id, key = b["id"].(string), "thread-concurrent-other-work"
			}
			status, data, _ := request(t, "POST", threadBase(f, id), f.agent, key, "", input)
			results[i] = outcome{status, data}
		}()
	}
	group.Wait()
	for i, result := range results {
		assertCode(t, result.status, 201, result.data)
		if i > 0 && result.data["thread"].(map[string]any)["id"] != results[0].data["thread"].(map[string]any)["id"] {
			t.Fatalf("source uniqueness failed: %+v", results)
		}
	}
	one := results[0].data["link"].(map[string]any)["id"]
	two := results[1].data["link"].(map[string]any)["id"]
	if one != two {
		t.Fatalf("concurrent replay produced multiple links: %s and %s", one, two)
	}
	var threads, links int
	err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.external_threads WHERE project_id=$1 AND provider='slack' AND thread_kind='discussion'`, f.project).Scan(&threads)
	if err != nil || threads != 1 {
		t.Fatalf("source uniqueness %d %v", threads, err)
	}
	err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.work_thread_links WHERE project_id=$1 AND removed_at IS NULL`, f.project).Scan(&links)
	if err != nil || links != 2 {
		t.Fatalf("duplicate replay wrote extra link %d %v", links, err)
	}
	// Separate idempotency keys racing for the same version: exactly one can advance the WorkItem.
	c := create(t, f, f.agent, "thread-concurrent-c")
	var competing [2]outcome
	group.Add(2)
	for i := range competing {
		i := i
		go func() {
			defer group.Done()
			in := map[string]any{
				"provider": "linear", "thread_kind": "issue",
				"external_thread_id": fmt.Sprintf("project/issue-%d", i), "expected_version": 1,
			}
			st, data, _ := request(t, "POST", threadBase(f, c["id"].(string)),
				f.agent, fmt.Sprintf("thread-competing-cas-%d", i), "", in)
			competing[i] = outcome{st, data}
		}()
	}
	group.Wait()
	success := 0
	conflict := 0
	for _, result := range competing {
		if result.status == 201 {
			success++
		} else if result.status == 409 {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent response %d %v", result.status, result.data)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS not serialized: success %d conflict %d", success, conflict)
	}
}

func TestThreadArchiveV2RealS3PersistsUnlinkHistory(t *testing.T) {
	f := setup(t)
	if f.objects == nil {
		t.Skip("real private S3 store not configured")
	}
	work := create(t, f, f.agent, "thread-archive-real-work")
	itemID := work["id"].(string)
	input := map[string]any{"provider": "github", "thread_kind": "issue",
		"external_thread_id": "sample/repo/issues/200", "expected_version": 1}
	st, linked, _ := request(t, "POST", threadBase(f, itemID), f.agent, "thread-archive-link", "", input)
	assertCode(t, st, 201, linked)
	threadID := linked["thread"].(map[string]any)["id"].(string)
	st, unlinked, _ := request(t, "DELETE", threadBase(f, itemID)+"/"+threadID, f.agent,
		"thread-archive-unlink", "\"2\"", nil)
	assertCode(t, st, 200, unlinked)
	base := evidenceURL(f)
	st, result, _ := request(t, "POST", base+"/exports", f.admin, "thread-archive-export", "", nil)
	assertCode(t, st, 201, result)
	st, raw := rawHTTP(t, "GET", base+"/exports/"+result["id"].(string)+"/content", f.admin, "", nil, "")
	if st != 200 {
		t.Fatalf("private archive download HTTP%d: %s", st, raw)
	}
	manifest, err := evidencepg.VerifyArchive(raw)
	var runSchema bool
	if err := f.pool.QueryRow(context.Background(), `SELECT to_regclass('qual.agent_runs') IS NOT NULL`).Scan(&runSchema); err != nil {
		t.Fatal(err)
	}
	wantVersion := 2
	if runSchema {
		wantVersion = 3 // Goose16 adds Run history without rewriting v2 Thread ZIPs.
	}
	if err != nil || manifest.ArchiveSchemaVersion != wantVersion {
		t.Fatalf("real project ZIP schema=%d expected=%d err=%v", manifest.ArchiveSchemaVersion, wantVersion, err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var seenThread, seenUnlink bool
	for _, file := range reader.File {
		if file.Name != "external_threads.jsonl" && file.Name != "work_thread_links.jsonl" {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		rawJSON, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "external_threads.jsonl" && bytes.Contains(rawJSON, []byte(threadID)) {
			seenThread = true
		}
		if file.Name == "work_thread_links.jsonl" &&
			bytes.Contains(rawJSON, []byte(threadID)) &&
			bytes.Contains(rawJSON, []byte("\"removed_at\":")) &&
			bytes.Contains(rawJSON, []byte("\"removed_by\":")) {
			seenUnlink = true
		}
	}
	if !seenThread || !seenUnlink {
		t.Fatalf("project ZIP omitted real Thread/Unlink provenance: thread=%t unlink=%t", seenThread, seenUnlink)
	}
}
