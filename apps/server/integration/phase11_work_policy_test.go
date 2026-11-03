//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPhase11WorkItemReviewPolicyOverrideAndOwnerReceipt(t *testing.T) {
	gate13(t)
	f := setup(t)
	projectPolicy := "/api/v1/projects/" + f.project.String() + "/review-policy"
	code, changed, _ := request(t, "PUT", f.server.URL+projectPolicy, f.admin, "work-policy-project-enable", "", map[string]any{
		"default_mode": "independent_review", "owner_acceptance_allowed": true,
		"force_independent_for_high_risk": true, "expected_version": 0,
		"reason": "Human Admin permits explicit WorkItem Owner acceptance only on individual items",
	})
	if code != 200 || changed["version"] != float64(1) {
		t.Fatalf("project policy enable %d %#v", code, changed)
	}
	run := reviewRun(t, f, "work-policy")
	itemID := reviewTarget(t, f, f.admin, "work-policy-owner", run)
	path := f.url(f.project) + "/" + itemID + "/review-policy"
	code, before, _ := request(t, "GET", path, f.agent, "", "", nil)
	if code != 200 || before["mode"] != "independent_review" || before["source"] != "project" ||
		before["override_version"] != float64(0) || before["work_version"] != float64(4) {
		t.Fatalf("effective default policy %d %#v", code, before)
	}
	input := map[string]any{"mode": "owner_acceptance", "expected_version": 4, "expected_policy_version": 0,
		"reason": "Human Admin explicitly chooses non-independent Owner signoff for this WorkItem"}
	if code, _, _ := request(t, "PUT", path, f.agent, "work-policy-agent-no-access", "", input); code != 403 {
		t.Fatalf("ordinary Agent changed WorkItem override %d", code)
	}
	if code, _, _ := request(t, "GET", f.url(f.otherProject)+"/"+itemID+"/review-policy", f.agent, "", "", nil); code != 404 {
		t.Fatalf("foreign project override read permitted %d", code)
	}
	code, work, _ := request(t, "PUT", path, f.admin, "work-policy-owner-set", "", input)
	if code != 200 || work["status"] != "in_review" || work["version"] != float64(5) {
		t.Fatalf("Owner override failed %d %#v", code, work)
	}
	code, replay, _ := request(t, "PUT", path, f.admin, "work-policy-owner-set", "", input)
	if code != 200 || replay["version"] != float64(5) {
		t.Fatalf("override replay not idempotent %d %#v", code, replay)
	}
	changedInput := map[string]any{"mode": "owner_acceptance", "expected_version": 4, "expected_policy_version": 0,
		"reason": "This changed payload must not reuse the existing idempotency key"}
	if code, _, _ := request(t, "PUT", path, f.admin, "work-policy-owner-set", "", changedInput); code != 409 {
		t.Fatalf("same-key changed WorkItem override accepted %d", code)
	}
	if code, _, _ := request(t, "PUT", path, f.admin, "work-policy-owner-stale", "", input); code != 409 {
		t.Fatalf("WorkItem override stale CAS accepted %d", code)
	}
	code, effective, _ := request(t, "GET", path, f.agent, "", "", nil)
	if code != 200 || effective["mode"] != "owner_acceptance" || effective["source"] != "work_item" ||
		effective["override_version"] != float64(1) || effective["work_version"] != float64(5) {
		t.Fatalf("work-level effective Owner override incorrect %d %#v", code, effective)
	}
	if code, _, _ := request(t, "POST", completeURL(f, itemID), f.admin, "work-policy-older-version", "",
		reviewCompletion(run, 4, "owner_acceptance", true)); code != 409 {
		t.Fatalf("policy mutation did not invalidate former WorkItem CAS %d", code)
	}
	code, done, _ := request(t, "POST", completeURL(f, itemID), f.admin, "work-policy-owner-complete", "",
		reviewCompletion(run, 5, "owner_acceptance", true))
	if code != 200 || done["status"] != "done" {
		t.Fatalf("explicit Owner signoff failed %d %#v", code, done)
	}
	var mode, source string
	var policyVersion int64
	var independent, explicit bool
	err := f.pool.QueryRow(context.Background(), `SELECT acceptance_mode,policy_source,policy_version,independent,explicit_self_review
         FROM qual.work_acceptance_decisions WHERE work_item_id=$1`, itemID).Scan(&mode, &source, &policyVersion, &independent, &explicit)
	if err != nil || mode != "owner_acceptance" || source != "work_item" || policyVersion != 1 || independent || !explicit {
		t.Fatalf("Owner accepted as independent or wrong policy source: %v %q %q %d %v %v", err, mode, source, policyVersion, independent, explicit)
	}
	var count int
	err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM qual.review_policy_changes WHERE work_item_id=$1`, itemID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("policy override history missing %d %v", count, err)
	}
	if _, err = f.pool.Exec(context.Background(), `DELETE FROM qual.review_policy_changes WHERE work_item_id=$1`, itemID); err == nil {
		t.Fatal("policy override history was mutable")
	}
	if code, _, _ := request(t, "PUT", path, f.admin, "work-policy-after-done", "",
		map[string]any{"mode": "independent_review", "expected_version": 6, "expected_policy_version": 1,
			"reason": "Attempt to retroactively change signed-off done policy rejected"}); code != 422 {
		t.Fatalf("retroactive policy override after Done was accepted %d", code)
	}
}

func TestPhase11HighRiskWorkPolicyRemainsIndependent(t *testing.T) {
	gate13(t)
	f := setup(t)
	url := f.server.URL + "/api/v1/projects/" + f.project.String() + "/review-policy"
	code, _, _ := request(t, "PUT", url, f.admin, "highrisk-owner-project-policy", "", map[string]any{
		"default_mode": "owner_acceptance", "owner_acceptance_allowed": true,
		"force_independent_for_high_risk": true, "expected_version": 0,
		"reason": "Actual fixture project allows Owner only for non-high-risk work"})
	if code != 200 {
		t.Fatalf("project Owner policy failed %d", code)
	}
	code, item, _ := request(t, "POST", f.url(f.project), f.admin, "highrisk-explicit-create", "",
		map[string]any{"kind": "task", "title": "Urgent policy forced independent", "priority": "urgent"})
	if code != 201 {
		t.Fatalf("urgent WorkItem create failed %d", code)
	}
	id := item["id"].(string)
	path := f.url(f.project) + "/" + id + "/review-policy"
	code, policy, _ := request(t, "GET", path, f.agent, "", "", nil)
	if code != 200 || policy["mode"] != "independent_review" || policy["forced_independent"] != true || policy["high_risk"] != true {
		t.Fatalf("urgent WorkItem bypassed safety policy %d %#v", code, policy)
	}
	if code, _, _ := request(t, "PUT", path, f.admin, "highrisk-attempt-to-owner", "", map[string]any{
		"mode": "owner_acceptance", "expected_version": 1, "expected_policy_version": 0,
		"reason": "Try to override high-risk project safety with Owner signoff"}); code != 422 {
		t.Fatalf("Human Admin bypassed forced independent urgent policy %d", code)
	}
	if code, _, _ := request(t, "PUT", path, f.admin, "highrisk-untrusted-actor", "", map[string]any{
		"mode": "owner_acceptance", "actor_id": uuid.NewString(), "expected_version": 1, "expected_policy_version": 0,
		"reason": "Try to impersonate another reviewer in WorkItem policy"}); code != 400 {
		t.Fatalf("untrusted actor property accepted %d", code)
	}
}
