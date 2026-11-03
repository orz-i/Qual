"""Offline assertions for historical Human/PI handoff; never call live Qual."""

import copy
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))

import phase7_collaboration_verify as collaboration  # noqa: E402

AGENT = "3f438142-9b23-4eb9-b5b9-10e740ff96d0"
HUMAN = "7f4e7c05-8348-4dda-af25-ad41bf582b58"
WORK = "886fb51d-d3bd-4dab-b920-2bb1469d7c0a"
RUN = "e8d7a3a9-91ea-4a58-adf3-fb583ba11019"
COMMIT = "c1b031a97a019f1e9fe35de3ffebabda77f3f76a"


def sample():
    item = {"id": WORK, "key": "QUAL-19", "status": "done",
            "version": 11, "created_by": AGENT, "assignee_id": AGENT}
    entries = [
        {"id": "executor", "capacity": "executor", "actor_id": AGENT,
         "reference_kind": "git_commit", "reference_value": COMMIT,
         "work_status": "in_progress", "work_version": 5,
         "recorded_at": "2026-11-01T13:22:00+08:00"},
        {"id": "tester", "capacity": "tester", "actor_id": AGENT, "outcome": "passed",
         "reference_kind": "test_run", "reference_value": RUN,
         "work_status": "in_progress", "work_version": 6,
         "recorded_at": "2026-11-01T13:23:00+08:00"},
        {"id": "reviewer", "capacity": "reviewer", "actor_id": HUMAN,
         "outcome": "passed", "self_review": False,
         "reference_kind": "test_run", "reference_value": RUN,
         "work_status": "in_review", "work_version": 10,
         "recorded_at": "2026-11-01T13:39:00+08:00"},
    ]
    return item, entries, {AGENT: "agent", HUMAN: "human"}


def attempts():
    run = {"source_system": "external-pi-go-integration", "items": [{
        "id": "runitem", "latest_status": "passed",
        "attempt_count": 2, "test_case_key": "QUAL-TC-9",
        "case_version_id": "caseversion"}]}
    rows = [
        {"id": "passed", "attempt_no": 2, "status": "passed",
         "reported_by": AGENT, "source_kind": "external"},
        {"id": "failed", "attempt_no": 1, "status": "failed",
         "reported_by": AGENT, "source_kind": "external"},
    ]
    return run, rows


class HandoffRoleTests(unittest.TestCase):
    def test_independent_human_handoff_is_provable(self):
        item, roles, kinds = sample()
        proof = collaboration.choose_handoff(item, roles, kinds)
        self.assertEqual(proof["executor_commit"], COMMIT)
        self.assertEqual(proof["test_run_id"], RUN)
        self.assertEqual(proof["reviewer_id"], HUMAN)
        self.assertFalse(proof["review_self_review"])

    def test_wrong_creator_or_not_done_rejected(self):
        item, roles, kinds = sample()
        for patch in ({"status": "in_review"}, {"created_by": HUMAN},
                      {"assignee_id": HUMAN}, {"version": 10}):
            with self.subTest(patch=patch):
                changed = {**item, **patch}
                with self.assertRaises(RuntimeError):
                    collaboration.choose_handoff(changed, roles, kinds)

    def test_self_review_and_fake_human_rejected(self):
        for key, val in (("self_review", True), ("actor_id", AGENT),
                         ("work_status", "in_progress")):
            item, roles, kinds = sample()
            roles[-1][key] = val
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                collaboration.choose_handoff(item, roles, kinds)
        item, roles, kinds = sample()
        kinds[HUMAN] = "agent"
        with self.assertRaises(RuntimeError):
            collaboration.choose_handoff(item, roles, kinds)

    def test_newer_needs_work_cannot_be_hidden_by_old_pass(self):
        item, roles, kinds = sample()
        roles.insert(0, {**roles[-1], "id": "recent",
                         "outcome": "needs_work",
                         "recorded_at": "2026-11-01T13:50:00+08:00"})
        with self.assertRaisesRegex(RuntimeError, "latest reviewer"):
            collaboration.choose_handoff(item, roles, kinds)

    def test_missing_executor_or_tester_rejected(self):
        item, roles, kinds = sample()
        for remove in ("executor", "tester"):
            with self.subTest(remove=remove), self.assertRaises(RuntimeError):
                collaboration.choose_handoff(
                    item, [r for r in roles if r["capacity"] != remove], kinds)

    def test_invalid_commit_and_role_order_rejected(self):
        item, roles, kinds = sample()
        invalid = copy.deepcopy(roles)
        invalid[0]["reference_value"] = "f" * 8
        with self.assertRaises(RuntimeError):
            collaboration.choose_handoff(item, invalid, kinds)
        invalid = copy.deepcopy(roles)
        invalid[1]["recorded_at"] = "2026-11-01T13:21:00+08:00"
        with self.assertRaises(RuntimeError):
            collaboration.choose_handoff(item, invalid, kinds)


class HandoffEvidenceTests(unittest.TestCase):
    def test_external_failed_then_passed_is_immutable(self):
        run, rows = attempts()
        proof = collaboration.verify_attempts(run, rows, AGENT)
        self.assertEqual([a["status"] for a in proof["attempts"]], ["failed", "passed"])
        self.assertEqual(proof["latest_status"], "passed")

    def test_reject_missing_failed_or_disguised_reporter(self):
        run, rows = attempts()
        for changed in (
            [{**rows[0]}, {**rows[0], "id": "other", "attempt_no": 1}],
            [{**rows[0], "reported_by": HUMAN}, rows[1]],
            [rows[0], {**rows[1], "source_kind": "manual"}],
        ):
            with self.subTest(changed=changed), self.assertRaises(RuntimeError):
                collaboration.verify_attempts(run, changed, AGENT)

    def test_reject_duplicate_attempt_sequence(self):
        run, rows = attempts()
        rows[0]["attempt_no"] = 1
        with self.assertRaises(RuntimeError):
            collaboration.verify_attempts(run, rows, AGENT)

    def test_human_activity_must_include_review_and_transition(self):
        actions = [
            {"id": "review", "entity_id": WORK, "actor_id": HUMAN,
             "operation": "work_item.role_recorded",
             "occurred_at": "2026-11-01T13:39:00+08:00"},
            {"id": "done", "entity_id": WORK, "actor_id": HUMAN,
             "operation": "work_item.transition",
             "occurred_at": "2026-11-01T13:40:00+08:00"},
        ]
        proof = collaboration.verify_review_activity(actions, WORK, HUMAN)
        self.assertEqual(proof["human_transition_activity_id"], "done")
        with self.assertRaises(RuntimeError):
            collaboration.verify_review_activity(actions[:1], WORK, HUMAN)
        with self.assertRaises(RuntimeError):
            collaboration.verify_review_activity(
                [{**actions[0], "occurred_at": "2026-11-01T13:50:00+08:00"}, actions[1]], WORK, HUMAN)
        with self.assertRaises(RuntimeError):
            collaboration.verify_review_activity(
                [actions[0], {**actions[1], "actor_id": AGENT}], WORK, HUMAN)

    def test_invalid_git_ref_is_rejected_without_using_git(self):
        self.assertFalse(collaboration.repo_commit_exists("bad-sha"))


if __name__ == "__main__":
    unittest.main()
