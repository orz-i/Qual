"""Offline verification of queue probe's authentic resubmission semantics."""
import importlib.util
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "qual-review-lane-smoke.py"
SPEC = importlib.util.spec_from_file_location("qual_review_lane_smoke", SCRIPT)
assert SPEC and SPEC.loader
smoke = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(smoke)


def queue_row(key, state, outcome, lane, resubmitted=False):
    return {
        "key": key, "status": state, "review_lane": lane,
        "resubmitted_after_review": resubmitted,
        "latest_review": {"outcome": outcome, "self_review": False},
    }


class ReviewQueueResubmissionProbeTests(unittest.TestCase):
    def test_current_remediation_stays_needs_work(self):
        rows = [
            queue_row("QUAL-4", "in_progress", "needs_work", "needs_work"),
            queue_row("QUAL-13", "in_review", "failed", "needs_work"),
        ]
        self.assertEqual(smoke.validate_review_queue({"items": rows}), [])

    def test_real_re_review_requires_awaiting_without_rewriting_old_verdict(self):
        row = queue_row("QUAL-20", "in_review", "needs_work", "awaiting", True)
        self.assertEqual(smoke.validate_review_queue({"items": [row]}), [])
        self.assertEqual(row["latest_review"]["outcome"], "needs_work")

    def test_past_passed_after_resubmission_is_not_current_approval(self):
        correct = queue_row("QUAL-17", "in_review", "passed", "awaiting", True)
        stale = queue_row("QUAL-17", "in_review", "passed", "ready_to_close", True)
        self.assertEqual(smoke.validate_review_queue({"items": [correct]}), [])
        self.assertTrue(smoke.validate_review_queue({"items": [stale]}))

    def test_no_real_transition_is_not_resubmission(self):
        stale = queue_row("QUAL-20", "in_review", "needs_work", "awaiting")
        self.assertTrue(smoke.validate_review_queue({"items": [stale]}))

    def test_release_gate_requires_explicit_resubmission_and_rejects_old_server(self):
        ready = queue_row("QUAL-20", "in_review", "needs_work", "awaiting", True)
        self.assertEqual(smoke.validate_required_resubmission({"items": [ready]}, "QUAL-20"), [])
        old = {k: v for k, v in ready.items() if k != "resubmitted_after_review"}
        old["review_lane"] = "needs_work"
        self.assertTrue(smoke.validate_required_resubmission({"items": [old]}, "QUAL-20"))
        self.assertTrue(smoke.validate_required_resubmission({"items": []}, "QUAL-20"))
        self.assertTrue(smoke.validate_required_resubmission({"items": [ready, ready]}, "QUAL-20"))

    def test_release_gate_does_not_accept_old_passed_as_new_review(self):
        old_approved = queue_row("QUAL-20", "in_review", "passed", "awaiting", True)
        self.assertTrue(smoke.validate_required_resubmission({"items": [old_approved]}, "QUAL-20"))
        old_done = queue_row("QUAL-20", "done", "needs_work", "awaiting", True)
        self.assertTrue(smoke.validate_required_resubmission({"items": [old_done]}, "QUAL-20"))

    def test_invalid_resubmitted_marker_fails_closed(self):
        missing_reviewer = {
            "key": "QUAL-4", "status": "in_review", "review_lane": "awaiting",
            "resubmitted_after_review": True,
        }
        wrong_status = queue_row("QUAL-4", "in_progress", "needs_work", "awaiting", True)
        for row in (missing_reviewer, wrong_status):
            with self.subTest(row=row):
                self.assertTrue(smoke.validate_review_queue({"items": [row]}))


if __name__ == "__main__":
    unittest.main()
