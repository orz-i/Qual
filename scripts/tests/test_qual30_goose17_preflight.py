"""Read-only release qualification safety tests; no default DB writes."""
import hashlib
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SRC = Path(__file__).resolve().parents[1] / "ops/qual30_goose17_preflight.py"
SPEC = importlib.util.spec_from_file_location("qual30_goose17_preflight", SRC)
mod = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mod)


class Goose17PreflightGuards(unittest.TestCase):
    def test_pinned_source_and_binary_hashes(self):
        for value in (mod.CANDIDATE_SOURCE, mod.REVIEWED_MAIN):
            self.assertEqual(len(value), 40)
        for value in (mod.CANDIDATE_SERVER_SHA, mod.CANDIDATE_OPS_SHA,
                      mod.ACTIVE_GOOSE16_SHA, mod.GOOSE_CLI_SHA, mod.MIGRATION17_SHA,
                      mod.ENGINEERING_LOGS_SHA):
            self.assertEqual(len(value), 64)

    def test_no_privileged_operator_actions_in_code(self):
        contents = SRC.read_text(encoding="utf-8")
        self.assertNotIn("qual-backup-hmac.key", contents)
        self.assertNotIn("QUAL_BACKUP_HMAC_KEY", contents)
        self.assertNotIn("QUAL_TOKEN", contents)
        self.assertNotIn("DATABASE_URL", contents)
        self.assertNotIn("launchctl bootout", contents)
        self.assertNotIn("goose up", contents)
        self.assertNotIn("os.execve", contents)
        self.assertIn('choices=["inspect"]', contents)
        self.assertIn("operator_approval_obtained", contents)

    def test_sha_rejects_unknown_candidate(self):
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "goose17"
            f.write_bytes(b"not a reviewed migration")
            with self.assertRaisesRegex(mod.PreflightGuard, "SHA mismatch"):
                mod.verified_file(f, "a" * 64)
            self.assertEqual(mod.sha(f), hashlib.sha256(f.read_bytes()).hexdigest())

    def test_symlinked_candidate_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            real = p / "old"
            real.write_bytes(b"safe")
            link = p / "candidate"
            link.symlink_to(real)
            with self.assertRaisesRegex(mod.PreflightGuard, "symlinked"):
                mod.verified_file(link, hashlib.sha256(real.read_bytes()).hexdigest())

    def test_source_change_rejected(self):
        with mock.patch.object(mod, "shell", side_effect=[
            mod.CANDIDATE_SOURCE, "apps/server/internal/identity/domain/policy.go"
        ]), mock.patch.object(mod.subprocess, "run") as runner:
            runner.return_value.returncode = 0
            with self.assertRaisesRegex(mod.PreflightGuard, "changed after qualification"):
                mod.verified_source()

    def test_live_default_schema_version_not_16_rejected(self):
        with mock.patch.object(mod, "shell", return_value="17"):
            with self.assertRaisesRegex(mod.PreflightGuard, "not exactly Goose16"):
                mod.version_and_objects()

    def test_live_default_missing_goose16_physical_objects_rejected(self):
        with mock.patch.object(mod, "shell", side_effect=["16", "f"]):
            with self.assertRaisesRegex(mod.PreflightGuard, "physical Goose16"):
                mod.version_and_objects()

    def test_operational_inspect_no_secret_or_write_dependencies(self):
        with mock.patch.object(mod, "verified_source", return_value=(mod.CANDIDATE_SOURCE, mod.REVIEWED_MAIN)), \
             mock.patch.object(mod, "verified_file") as verify, \
             mock.patch.object(mod, "check_receipt", return_value=14), \
             mock.patch.object(mod, "version_and_objects", return_value=16), \
             mock.patch.object(mod, "running_server", return_value=123):
            result = mod.inspect()
            self.assertEqual(result["real_default_goose"], 16)
            self.assertFalse(result["operator_approval_obtained"])
            self.assertFalse(result["operator_key_or_runtime_secrets_read"])
            self.assertFalse(result["goose17_applied_to_default"])
            self.assertEqual(verify.call_count, 4)

    def test_latest_qualification_receipt_invariant(self):
        # This is a non-secret, git-ignored file with SHA rather than Auth data.
        self.assertEqual(mod.check_receipt(), 14)


if __name__ == "__main__":
    unittest.main()
