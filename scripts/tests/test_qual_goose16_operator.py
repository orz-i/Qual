"""No-live-mutation tests for the protected local Goose16 release controller."""
import importlib.util
import json
from pathlib import Path
import plistlib
import unittest
from unittest import mock

SOURCE = Path(__file__).resolve().parents[1] / "ops" / "qual_goose16_rollout.py"
SPEC = importlib.util.spec_from_file_location("qual_goose16_release", SOURCE)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class Goose16ReleaseGuardTests(unittest.TestCase):
    def test_real_source_requires_integrated_release_base(self):
        self.assertEqual(len(release.EXPECTED_BASE), 40)
        self.assertEqual(len(release.EXPECTED_SERVER_SHA), 64)
        self.assertEqual(len(release.EXPECTED_OPS_SHA), 64)
        self.assertEqual(len(release.EXPECTED_GOOSE_SHA), 64)
        self.assertTrue(release.source_gate())

    def test_explicit_apply_is_required_before_any_private_data_read(self):
        with mock.patch.object(release, "read_json", side_effect=AssertionError("unsafe private read")), \
             mock.patch.object(release.prior, "launchctl", side_effect=AssertionError("unsafe launchctl")):
            with self.assertRaisesRegex(release.Guard, "--apply"):
                release.cutover(False)

    def test_release_arguments_never_include_db_dsn(self):
        source = SOURCE.read_text()
        self.assertIn('"GOOSE_DBSTRING":env["DATABASE_URL"]', source)
        self.assertIn('exec_safe([str(GOOSE),"up-to",str(version)],env', source)
        self.assertNotIn('["-allow-missing"]', source)
        self.assertIn("no_downgrade_or_destructive_down", source)

    def test_plist_updates_only_runner(self):
        original = {
            "Label": release.LABEL,
            "ProgramArguments": ["/usr/bin/python3", str(release.ROOT / "scripts/ops/qual_goose13_rollout.py"),"run"],
            "WorkingDirectory": str(release.ROOT),
            "StandardOutPath": "/private/tmp/qual-stdout.log",
            "KeepAlive": {"SuccessfulExit":False},
            "RunAtLoad": True
        }
        with mock.patch.object(release.prior, "private_read", return_value=plistlib.dumps(original)):
            new = plistlib.loads(release.plist_for_new_runner())
        self.assertEqual(new["Label"], original["Label"])
        self.assertEqual(new["WorkingDirectory"],original["WorkingDirectory"])
        self.assertEqual(new["KeepAlive"],original["KeepAlive"])
        self.assertEqual(new["StandardOutPath"], original["StandardOutPath"])
        self.assertEqual(new["ProgramArguments"],
                         ["/usr/bin/python3",str(release.ROOT / "scripts/ops/qual_goose16_rollout.py"),"run"])

    def test_source_change_in_application_must_requalify(self):
        # Never let an arbitrary code mutation after the verified e92 source
        # silently become part of a release just because it has the same name.
        with mock.patch.object(release, "exec_safe", side_effect=[
            "deadbeef\n", "apps/server/cmd/server/main.go\n"
        ]), mock.patch.object(release.subprocess,"run") as process:
            process.return_value.returncode=0
            with self.assertRaisesRegex(release.Guard, "changed since tested"):
                release.source_gate()


if __name__ == "__main__":
    unittest.main()
