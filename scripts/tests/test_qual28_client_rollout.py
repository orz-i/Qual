"""QUAL-28 client-only release policy (no server/DB/HMAC operations)."""
import hashlib
import importlib.util
import os
from pathlib import Path
import stat
import tempfile
import unittest
from unittest import mock

PATH = Path(__file__).resolve().parents[1] / "ops/qual28_client_rollout.py"
SPEC = importlib.util.spec_from_file_location("qual28_client_release", PATH)
release = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(release)


class TestClientReleaseGuards(unittest.TestCase):
    def test_source_commit_and_candidate_hashes_locked(self):
        self.assertEqual(len(release.SOURCE_COMMIT), 40)
        self.assertEqual(len(release.REVIEWED_MAIN), 40)
        self.assertEqual(set(release.BINARIES), {"qual", "qual-mcp"})
        for digest in release.BINARIES.values():
            self.assertEqual(len(digest["old"]), 64)
            self.assertEqual(len(digest["new"]), 64)
            self.assertNotEqual(digest["old"], digest["new"])

    def test_apply_requires_explicit_approval_before_any_read(self):
        with mock.patch.object(release, "safe_private_dir", side_effect=AssertionError("unexpected FS operation")):
            with self.assertRaisesRegex(release.ReleaseGuard, "explicit --apply"):
                release.apply(False)

    def test_rollback_requires_explicit_approval_before_fs(self):
        with mock.patch.object(release, "safe_private_dir", side_effect=AssertionError("unexpected FS operation")):
            with self.assertRaisesRegex(release.ReleaseGuard, "explicit --apply"):
                release.rollback(False)

    def test_untrusted_symlink_is_never_an_executable(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            a = p / "real"
            a.write_bytes(b"valid")
            l = p / "qual"
            l.symlink_to(a)
            self.assertFalse(release.owned_regular(l))
            self.assertTrue(release.owned_regular(a))

    def test_private_backup_directory_has_strict_mode(self):
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            p = base / "release"
            release.safe_private_dir(p)
            self.assertEqual(stat.S_IMODE(p.stat().st_mode), 0o700)
            p.chmod(0o755)
            with self.assertRaisesRegex(release.ReleaseGuard, "mode invalid"):
                release.safe_private_dir(p)

    def test_atomic_install_and_backup_bytes_match_sha(self):
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            original = base / "previous"
            original.write_bytes(b"immutable original binary")
            candidate = base / "candidate"
            candidate.write_bytes(b"reviewed executable bytes 2026")
            target = base / "qual"
            target.write_bytes(original.read_bytes())
            expected = hashlib.sha256(candidate.read_bytes()).hexdigest()
            release._atomic_copy(candidate, target, 0o755, expected)
            self.assertEqual(release.sha(target), expected)
            self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o755)
            self.assertEqual(original.read_bytes(), b"immutable original binary")

    def test_copy_rejects_unreviewed_candidate_without_touching_old(self):
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            candidate, target = base / "candidate", base / "qual"
            candidate.write_bytes(b"corrupted build")
            target.write_bytes(b"preserve this binary")
            with self.assertRaisesRegex(release.ReleaseGuard, "SHA integrity"):
                release._atomic_copy(candidate, target, 0o755, "f" * 64)
            self.assertEqual(target.read_bytes(), b"preserve this binary")

    def test_external_agent_has_no_operator_backup_or_service_controls(self):
        text = PATH.read_text()
        self.assertNotIn("qual-backup-hmac.key", text)
        self.assertNotIn("launchctl", text)
        self.assertNotIn("goose up", text)
        self.assertNotIn("DATABASE_URL", text)
        self.assertIn("prior_mcp_processes_automatically_reloaded", text)


if __name__ == "__main__":
    unittest.main()
