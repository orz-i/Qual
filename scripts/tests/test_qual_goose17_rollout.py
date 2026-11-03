"""QUAL-30 Human-only Goose17 rollout mock guards; never call live apply."""
import importlib.util
from pathlib import Path
import plistlib
import subprocess
import tempfile
import unittest
from unittest import mock

PATH = Path(__file__).resolve().parents[1] / "ops/qual_goose17_rollout.py"
SPEC = importlib.util.spec_from_file_location("qual30_operator_goose17", PATH)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class Goose17HumanReleaseGuardTests(unittest.TestCase):
    def test_apply_requires_explicit_human_operator_and_reviewed_sha(self):
        with mock.patch.object(release, "enforce_source", side_effect=AssertionError("unexpected protected call")):
            with self.assertRaisesRegex(release.ReleaseGuard, "Human Operator"):
                release.apply(False, None)
            with self.assertRaisesRegex(release.ReleaseGuard, "Human Operator"):
                release.apply(True, "not-a-reviewed-sha")

    def test_unpromoted_branch_cannot_run_privileged_apply(self):
        # In an isolated worktree, enforce_source refuses even if an Agent
        # sets the Human-only flags. It cannot reach runtime_env or HMAC.
        with mock.patch.object(release, "ROOT", Path("/tmp/qual30-worktree")), \
             mock.patch.object(release, "safe_exec", side_effect=AssertionError("no command allowed")):
            with self.assertRaisesRegex(release.ReleaseGuard, "promoted real main"):
                release.enforce_source()

    def test_inspect_does_not_read_operator_config(self):
        with mock.patch.object(release.guard, "inspect", return_value={"ready_http": 200,
                                                                     "real_default_goose": 16}):
            with mock.patch.object(release.prior, "private_read", side_effect=AssertionError("HMAC read denied")), \
                 mock.patch.object(release.prior, "runtime_env", side_effect=AssertionError("runtime credentials denied")), \
                 mock.patch.object(release.prior, "launchctl", side_effect=AssertionError("live service operation denied")):
                result = release.preflight()
                self.assertEqual(result["ready_http"], 200)

    def test_wrong_launchagent_program_rejected(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "local.qual.go-server.plist"
            value = {"Label": release.LABEL, "WorkingDirectory": str(release.LIVE_ROOT),
                     "ProgramArguments": ["/usr/bin/python3", "/bin/false", "run"],
                     "RunAtLoad": True, "Umask": 63}
            path.write_bytes(plistlib.dumps(value))
            with mock.patch.object(release.prior, "PLIST", path):
                with self.assertRaisesRegex(release.ReleaseGuard, "reviewed baseline"):
                    release.current_plist()

    def test_goose17_migration_source_not_read_from_argv(self):
        recorded = []
        def fake_exec(argv, env=None, timeout=60):
            recorded.append((argv, env))
            return ""
        with mock.patch.object(release.goose16, "enforce_file"), \
             mock.patch.object(release, "real_goose_version", return_value=16), \
             mock.patch.object(release.prior, "runtime_env",
                               return_value={"DATABASE_URL": "postgres://synthetic-test.invalid/qual"}), \
             mock.patch.object(release, "safe_exec", side_effect=fake_exec), \
             mock.patch.object(release, "require_goose17_physical"), \
             mock.patch("builtins.print"):
            # Do not print a fake APPLIED milestone from a mocked test.
            release.migrate_goose17()
        self.assertEqual(len(recorded), 1)
        argv, env = recorded[0]
        self.assertEqual(argv[-2:], ["up-to", "17"])
        self.assertEqual(env["GOOSE_DRIVER"], "postgres")
        self.assertEqual(env["GOOSE_DBSTRING"], "postgres://synthetic-test.invalid/qual")
        self.assertNotIn("synthetic-test.invalid", str(argv))
        self.assertNotIn("allow-missing", str(argv))

    def test_missing_physical_schema_cannot_appear_ready(self):
        with mock.patch.object(release, "real_goose_version", return_value=17), \
             mock.patch.object(release, "safe_exec", return_value="f"):
            with self.assertRaisesRegex(release.ReleaseGuard, "immutable trigger mismatch"):
                release.require_goose17_physical()

    def test_rollback_never_launches_old_binary_on_goose17(self):
        with mock.patch.object(release.prior, "is_managed", return_value=False), \
             mock.patch.object(release.prior, "wait_port"), \
             mock.patch.object(release, "real_goose_version", return_value=17), \
             mock.patch.object(release.prior, "safe_write", side_effect=AssertionError("must not overwrite")):
            with self.assertRaisesRegex(release.ReleaseGuard, "cannot restore Goose16"):
                release.restore_previous_without_migration()

    def test_unreviewed_server_binary_refused_before_runtime_env(self):
        with mock.patch.object(release.goose16, "enforce_file", side_effect=release.ReleaseGuard("bad binary")), \
             mock.patch.object(release.prior, "runtime_env", side_effect=AssertionError("secret unavailable")):
            with self.assertRaisesRegex(release.ReleaseGuard, "bad binary"):
                release.run_server()

    def test_compiled_reviewed_server_hash_is_pinned(self):
        self.assertEqual(len(release.REVIEWED_SOURCE), 40)
        self.assertEqual(len(release.EXPECTED_SERVER_SHA), 64)
        self.assertEqual(len(release.EXPECTED_PREVIOUS_SHA), 64)
        self.assertEqual(len(release.EXPECTED_GOOSE_SHA), 64)
        self.assertNotEqual(release.EXPECTED_PREVIOUS_SHA, release.EXPECTED_SERVER_SHA)

    def test_operator_code_cannot_destroy_goose_history(self):
        source = PATH.read_text(encoding="utf-8")
        self.assertNotIn("goose down", source.lower())
        self.assertNotIn('"down"', source)
        self.assertIn("if real_goose_version()==16:", source)
        self.assertIn("no_destructive_down", source)
        self.assertIn("signed_backup_and_restore()", source)
        self.assertIn("operator_secret_precheck()", source)

    def test_operator_code_has_no_plaintext_secrets_or_dump(self):
        source = PATH.read_text()
        self.assertNotIn("postgres://qual:", source)
        self.assertNotIn("QUAL_DB_PASSWORD", source)
        self.assertNotIn("print(token", source)
        self.assertNotIn("print(key", source)
        self.assertIn("api/v1/projects", source)
        self.assertIn("default_new_permission_grants", source)


if __name__ == "__main__":
    unittest.main()
