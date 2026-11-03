"""Offline checks for QUAL-6 preflight; no credential or network access."""
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))

import phase7_ops_preflight as ops  # noqa: E402


class OperationalPreflightTests(unittest.TestCase):
    def test_health_and_ready_require_200_json_and_no_store(self):
        self.assertEqual(ops.validate_health("/healthz", (200, b'{"status":"ok"}', "no-store")), "ok")
        self.assertEqual(ops.validate_health("/readyz", (200, b'{"status":"ready"}', "no-store")), "ready")
        for result in ((503, b'{"status":"unavailable"}', "no-store"),
                       (200, b'{"status":"ready"}', ""),
                       (200, b'{"secret":"do-not-log"}', "no-store"),
                       (200, b"not-json", "no-store")):
            with self.subTest(result=result[0:1]), self.assertRaises(RuntimeError):
                ops.validate_health("/readyz", result)

    def test_disabled_operator_metrics_does_not_claim_configured_auth(self):
        sample = {k:(404, b"404 page not found", "no-store")
                  for k in ("anonymous", "agent_pat", "bogus_token")}
        result = ops.validate_metrics(sample, "private-agent-token", "bogus-token")
        self.assertEqual(result["mode"], "operator_token_not_configured_or_route_absent")
        self.assertFalse(result["configured_operator_access_verified"])
        self.assertTrue(result["ordinary_agent_metrics_access_denied"])

    def test_configured_operator_metrics_require_separate_token(self):
        sample = {k:(401, b"unauthorized", "no-store")
                  for k in ("anonymous", "agent_pat", "bogus_token")}
        result = ops.validate_metrics(sample, "private-agent-token", "bogus-token")
        self.assertEqual(result["mode"], "operator_token_required_for_access")
        self.assertFalse(result["configured_operator_access_verified"])

    def test_metrics_unexpected_access_fails_closed(self):
        for status in (200, 302, 403, 500):
            sample = {k:(401, b"unauthorized", "no-store")
                      for k in ("anonymous", "agent_pat", "bogus_token")}
            sample["agent_pat"] = (status, b"safe", "no-store")
            with self.subTest(status=status), self.assertRaises(RuntimeError):
                ops.validate_metrics(sample, "private-agent-token", "bogus-token")

    def test_metrics_mixed_modes_incomplete_or_cacheable_rejected(self):
        correct = {k:(404, b"no route", "no-store")
                   for k in ("anonymous", "agent_pat", "bogus_token")}
        for changed in (
            {**correct, "agent_pat": (401, b"unauthorized", "no-store")},
            {**correct, "anonymous": (404, b"no route", "public")},
            {"anonymous":correct["anonymous"], "agent_pat":correct["agent_pat"]},
        ):
            with self.subTest(changed=changed), self.assertRaises(RuntimeError):
                ops.validate_metrics(changed, "private-agent-token", "bogus-token")

    def test_metrics_rejection_must_not_echo_credentials(self):
        for leaked in (b"private-agent-token", b"bogus-token"):
            rows = {k:(401, b"unauthorized", "no-store")
                    for k in ("anonymous", "agent_pat", "bogus_token")}
            rows["agent_pat"] = (401, leaked, "no-store")
            with self.subTest(leaked=leaked), self.assertRaises(RuntimeError):
                ops.validate_metrics(rows, "private-agent-token", "bogus-token")

    def test_read_only_routes_are_allowlisted(self):
        self.assertEqual(ops.PATHS, {"/healthz", "/readyz", "/internal/metrics"})
        for path in ("/api/v1/projects", "https://example.org", "/internal/metrics?token=abc",
                     "/internal/metrics/anything"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                ops.fetch(path)

    def test_feedback_template_required_but_not_filled(self):
        text = ("实际完成情况\n可复现步骤\n严重程度\n"
                "试点者是否确认该反馈为真实操作记录")
        self.assertTrue(ops.feedback_template_ready(text))
        self.assertFalse(ops.feedback_template_ready("复现步骤\n建议"))
        self.assertTrue(ops.feedback_template_ready(
            ops.FEEDBACK_TEMPLATE.read_text(encoding="utf-8")
        ))


if __name__ == "__main__":
    unittest.main()
