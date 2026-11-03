"""Offline authorization-shape regressions for Phase7/Goose13 dogfood."""
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import phase7_verify as verify  # noqa: E402

PID = "e6560273-31f1-4c3f-b690-3b4586977700"
AGENT = "3f438142-9b23-4eb9-b5b9-10e740ff96d0"
BASE = sorted(verify.BASELINE_SCOPES)


def access(scopes=(), effective=(), **fields):
    return {
        "project_id": PID, "principal_id": AGENT, "kind": "agent",
        "credential_scopes": BASE + list(scopes),
        "effective_permissions": BASE + list(effective),
        **fields,
    }


class DelegatedReviewPermissionTests(unittest.TestCase):
    def test_original_seven_scope_pilot_is_still_valid(self):
        result = verify.inspect_agent_access(access(), PID)
        self.assertFalse(result["work_review_effective"])
        self.assertFalse(result["audit_read_effective"])

    def test_human_delegated_agent_review_reconciles_both_scopes(self):
        result = verify.inspect_agent_access(
            access(scopes=["work:review"], effective=["work:review"]), PID)
        self.assertTrue(result["work_review_scope"])
        self.assertTrue(result["work_review_effective"])
        self.assertFalse(result["audit_read_effective"])

    def test_audit_read_can_be_future_explicit_delegation(self):
        result = verify.inspect_agent_access(
            access(scopes=["work:review", "audit:read"],
                   effective=["work:review", "audit:read"]), PID)
        self.assertTrue(result["audit_read_scope"])
        self.assertTrue(result["audit_read_effective"])
        self.assertNotIn("access:manage", result["effective_scopes"])

    def test_credential_scope_without_project_allow_is_not_effective(self):
        result = verify.inspect_agent_access(access(scopes=["work:review"]), PID)
        self.assertTrue(result["work_review_scope"])
        self.assertFalse(result["work_review_effective"])

    def test_project_override_cannot_escape_credential_scope(self):
        for privilege in ("work:review", "audit:read", "access:manage"):
            with self.subTest(privilege=privilege), self.assertRaises(RuntimeError):
                verify.inspect_agent_access(access(effective=[privilege]), PID)

    def test_any_unplanned_admin_project_grant_or_latent_scope_fails_closed(self):
        for privilege in ("access:manage", "archive:export", "webhook:manage"):
            with self.subTest(privilege=privilege):
                with self.assertRaises(RuntimeError):
                    verify.inspect_agent_access(access(
                        scopes=[privilege], effective=[privilege]), PID)
                # Even a credential whose privilege is currently Project-denied
                # must not silently carry an unrelated elevated capability.
                with self.assertRaises(RuntimeError):
                    verify.inspect_agent_access(access(scopes=[privilege]), PID)

    def test_baseline_permission_cannot_disappear(self):
        sample = access()
        sample["effective_permissions"].remove("work:claim")
        with self.assertRaises(RuntimeError):
            verify.inspect_agent_access(sample, PID)

    def test_invalid_or_mismatched_identity_fails_closed(self):
        cases = (
            access(kind="human"),
            access(project_id="7ade9642-dd5f-4b78-b59e-172699a686bc"),
            access(principal_id="invalid"),
            access(credential_scopes=[]),
            access(effective_permissions="work:read"),
            access(effective_permissions=BASE + ["work:read"]),
        )
        for sample in cases:
            with self.subTest(sample=sample.get("kind")), self.assertRaises(RuntimeError):
                verify.inspect_agent_access(sample, PID)


if __name__ == "__main__":
    unittest.main()
