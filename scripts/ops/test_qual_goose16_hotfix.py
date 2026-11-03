#!/usr/bin/env python3
"""Non-Operator guard tests: never read secrets, stop launchd or touch DB."""
import importlib.util
import os
from pathlib import Path
import plistlib
import tempfile
import types
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qual49_test_target", HERE / "qual_goose16_hotfix.py")
hotfix = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hotfix)


class HotfixGuardTest(unittest.TestCase):
    def test_apply_requires_explicit_human_operator(self):
        with mock.patch.object(hotfix, "preflight") as pre:
            with self.assertRaisesRegex(hotfix.HotfixGuard, "Human Operator"):
                hotfix.apply(False)
            pre.assert_not_called()

    def test_binary_forged_hash_is_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            fake = Path(folder) / "server"
            fake.write_bytes(b"replaced untrusted binary")
            with self.assertRaisesRegex(hotfix.HotfixGuard, "hash mismatch"):
                hotfix.checked_binary(fake, hotfix.EXPECTED_NEW_SHA)

    def test_symlinked_binary_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            real = Path(folder) / "real"
            real.write_bytes(b"x")
            link = Path(folder) / "link"
            link.symlink_to(real)
            with self.assertRaisesRegex(hotfix.HotfixGuard, "absent or not owned"):
                hotfix.checked_binary(link, hotfix.EXPECTED_NEW_SHA)

    def test_preflight_never_reads_operator_key_or_backs_up(self):
        with mock.patch.multiple(hotfix, source_gate=mock.DEFAULT, checked_binary=mock.DEFAULT,
                                 checked_test_logs=mock.DEFAULT, goose16_only=mock.DEFAULT,
                                 active_old=mock.DEFAULT, current_plist_bytes=mock.DEFAULT,
                                 signed_backup_and_restore=mock.DEFAULT) as deps:
            deps["source_gate"].return_value = hotfix.BASE_COMMIT
            deps["active_old"].return_value = 51919
            hotfix.preflight()
            deps["signed_backup_and_restore"].assert_not_called()
            deps["current_plist_bytes"].assert_called_once()
            deps["goose16_only"].assert_called_once()

    def test_wrong_live_plist_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            plist = Path(folder) / "agent.plist"
            wrong = {"Label": hotfix.LABEL, "ProgramArguments": ["/bin/false"],
                     "WorkingDirectory": str(hotfix.ROOT), "RunAtLoad": True, "Umask": 63}
            plist.write_bytes(plistlib.dumps(wrong))
            with mock.patch.object(hotfix.prior, "PLIST", plist):
                with self.assertRaisesRegex(hotfix.HotfixGuard, "launchd arguments changed"):
                    hotfix.current_plist_bytes()

    def test_correct_live_plist_accepted_without_mutation(self):
        with tempfile.TemporaryDirectory() as folder:
            plist = Path(folder) / "agent.plist"
            value = {"Label": hotfix.LABEL, "ProgramArguments": hotfix.OLD_RUN,
                     "WorkingDirectory": str(hotfix.ROOT), "RunAtLoad": True, "Umask": 63}
            content = plistlib.dumps(value)
            plist.write_bytes(content)
            with mock.patch.object(hotfix.prior, "PLIST", plist):
                self.assertEqual(hotfix.current_plist_bytes(), content)

    def test_goose_ledger_mismatch_refused_before_schema_read(self):
        with mock.patch.object(hotfix.goose16, "check_goose_schema", return_value=15), \
             mock.patch.object(hotfix.goose16, "exec_safe") as run:
            with self.assertRaisesRegex(hotfix.HotfixGuard, "exactly Goose16"):
                hotfix.goose16_only()
            run.assert_not_called()

    def test_incomplete_goose16_physical_schema_refused(self):
        with mock.patch.object(hotfix.goose16, "check_goose_schema", return_value=16), \
             mock.patch.object(hotfix.goose16, "exec_safe", return_value="f"):
            with self.assertRaisesRegex(hotfix.HotfixGuard, "physical schema incomplete"):
                hotfix.goose16_only()

    def test_unreviewed_source_diff_refused(self):
        with mock.patch.object(hotfix.goose16, "exec_safe", return_value=hotfix.BASE_COMMIT), \
             mock.patch.object(hotfix.subprocess, "run", side_effect=[types.SimpleNamespace(returncode=0),
                                                                       types.SimpleNamespace(returncode=1)]):
            with self.assertRaisesRegex(hotfix.HotfixGuard, "Application or contract changed"):
                hotfix.source_gate()

    def test_failed_operator_backup_attempts_binary_only_rollback(self):
        # Every privileged operation is mocked: no live launchd, key, PG or S3.
        with tempfile.TemporaryDirectory() as folder:
            with mock.patch.object(hotfix, "RELEASE", Path(folder)), \
                 mock.patch.object(hotfix, "preflight", return_value=(hotfix.BASE_COMMIT, 51919)), \
                 mock.patch.object(hotfix, "operator_secret_precheck"), \
                 mock.patch.object(hotfix, "prepare_operator_material"), \
                 mock.patch.object(hotfix, "active_old", return_value=51919), \
                 mock.patch.object(hotfix, "current_plist_bytes", return_value=b"old-reviewed-plist"), \
                 mock.patch.object(hotfix.prior, "private_read", return_value=b"old-reviewed-plist"), \
                 mock.patch.object(hotfix.prior, "launchctl") as ctl, \
                 mock.patch.object(hotfix.prior, "wait_port"), \
                 mock.patch.object(hotfix.goose16, "require_no_other_database_writers"), \
                 mock.patch.object(hotfix.goose16, "fingerprint", return_value=["facts-unchanged"]), \
                 mock.patch.object(hotfix, "signed_backup_and_restore", side_effect=hotfix.HotfixGuard("test backup blocked")), \
                 mock.patch.object(hotfix, "rollback_previous") as rollback:
                with self.assertRaisesRegex(hotfix.HotfixGuard, "test backup blocked"):
                    hotfix.apply(True)
                ctl.assert_called_once_with("bootout", hotfix.JOB)
                rollback.assert_called_once_with()

    def test_missing_operator_key_aborts_before_service_stop(self):
        with mock.patch.object(hotfix, "preflight", return_value=(hotfix.BASE_COMMIT, 51919)), \
             mock.patch.object(hotfix, "operator_secret_precheck", side_effect=hotfix.HotfixGuard("precheck denied")), \
             mock.patch.object(hotfix.prior, "launchctl") as ctl:
            with self.assertRaisesRegex(hotfix.HotfixGuard, "precheck denied"):
                hotfix.apply(True)
            ctl.assert_not_called()

    def test_no_operator_hmac_in_candidate_read_only_actions(self):
        # Reviewed code must keep the private HMAC read inside apply's backup path.
        text = (HERE / "qual_goose16_hotfix.py").read_text()
        self.assertIn('"apply", "run"', text)
        self.assertIn("def signed_backup_and_restore():", text)
        self.assertIn("def preflight():", text)
        pre = text.split("def preflight():", 1)[1].split("def save_json", 1)[0]
        self.assertNotIn("qual-backup-hmac.key", pre)
        self.assertNotIn("runtime_env(", pre)
        self.assertNotIn("launchctl(", pre)


if __name__ == "__main__":
    unittest.main()
