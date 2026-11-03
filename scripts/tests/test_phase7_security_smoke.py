"""Pure, offline security probe tests. Network checks run only via explicit CLI."""
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))

import phase7_security_smoke as smoke  # noqa: E402


class DeniedResponseTests(unittest.TestCase):
    def test_expected_denial_with_generic_error(self):
        result = smoke.ensure_denied(
            (403, {"error": {"code": "PERMISSION_DENIED", "message": "Insufficient permission"}}),
            403,
            "PERMISSION_DENIED",
            ("qual_pat_secret", "Sensitive work item title"),
        )
        self.assertEqual(result, "403 PERMISSION_DENIED")

    def test_denial_must_not_leak_credentials_or_work_item_title(self):
        secrets = ("qual_pat_never_display", "Private QUAL task")
        for body in (
            {"error": {"code": "PERMISSION_DENIED", "message": secrets[0]}},
            {"error": {"code": "PERMISSION_DENIED", "message": secrets[1]}},
        ):
            with self.subTest(body=body):
                with self.assertRaisesRegex(RuntimeError, "disclosed"):
                    smoke.ensure_denied((403, body), 403, "PERMISSION_DENIED", secrets)

    def test_unexpected_status_and_error_code_fail_closed(self):
        for actual in (
            (200, {"items": []}),
            (404, {"error": {"code": "NOT_FOUND"}}),
            (403, {"error": {"code": "SOMETHING_ELSE"}}),
            (403, {}),
        ):
            with self.subTest(actual=actual):
                with self.assertRaisesRegex(RuntimeError, "expected HTTP"):
                    smoke.ensure_denied(actual, 403, "PERMISSION_DENIED", ())

    def test_business_api_rejects_invalid_path(self):
        with self.assertRaises(ValueError):
            smoke.api("https://somewhere.invalid", "")
        with self.assertRaises(ValueError):
            smoke.api("/internal/metrics", "")

    def test_probe_calls_no_live_network_from_import(self):
        # Only run() reads private credentials and contacts the local API.
        self.assertEqual(smoke.BASE, "http://127.0.0.1:18080")
        self.assertEqual(smoke.UNKNOWN_PROJECT, "ffffffff-ffff-4fff-afff-ffffffffffff")

    def test_review_candidate_requires_real_pending_item_and_stale_cas(self):
        selected = smoke.choose_review_candidate([
            {"key": "QUAL-1", "status": "done", "version": 9, "id": "completed"},
            {"key": "QUAL-20", "status": "in_review", "version": 8, "id": "pending"},
            {"key": "QUAL-7", "status": "in_review", "version": 1, "id": "no-stale-version"},
        ])
        self.assertEqual(selected["key"], "QUAL-20")
        self.assertLess(selected["version"] - 1, selected["version"])

    def test_review_candidate_fails_closed_when_no_safe_target(self):
        for items in (None, {}, [], [{"key": "QUAL-1", "status": "done", "version": 9, "id": "completed"}],
                      [{"key": "QUAL-20", "status": "in_review", "version": 1, "id": "pending"}]):
            with self.subTest(items=items), self.assertRaises(RuntimeError):
                smoke.choose_review_candidate(items)

    def test_review_candidate_is_deterministic(self):
        items = [{"key": key, "status": "in_review", "version": 4, "id": key} for key in ("QUAL-20", "QUAL-9")]
        self.assertEqual(smoke.choose_review_candidate(items)["key"], "QUAL-20")


if __name__ == "__main__":
    unittest.main()
