"""Security and idempotency-shape checks for Qual's PI dogfood seed."""
import importlib.util
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import phase7_seed  # noqa: E402
import phase7_verify  # noqa: E402


class PilotSchemaTests(unittest.TestCase):
    def test_unique_tasks_and_case_mapping(self):
        seed = ROOT / "docs/pilot/phase7-seed.json"
        if not seed.is_file():
            self.skipTest("private pilot seed intentionally omitted from public source snapshot")
        data = json.loads(seed.read_text(encoding="utf-8"))
        self.assertEqual(data["schema_version"], 1)
        self.assertEqual(data["project_key"], "QUAL")
        self.assertEqual(len(data["tasks"]), 6)
        task_slugs = {t["slug"] for t in data["tasks"]}
        self.assertEqual(len(task_slugs), len(data["tasks"]))
        self.assertEqual(len(data["suite"]["cases"]), 3)
        self.assertEqual(len({c["slug"] for c in data["suite"]["cases"]}), 3)
        for case in data["suite"]["cases"]:
            self.assertIn(case["task_slug"], task_slugs)
            self.assertTrue(case["steps"])
            self.assertTrue(case["expected_result"])
        for task in data["tasks"]:
            self.assertEqual(task["priority"] in ("high", "medium"), True)
            self.assertTrue(task["acceptance_criteria"])

    def test_private_owner_token_rejects_symlink_and_bad_permissions(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "qual-owner.token"
            token = "qual_pat_" + "a" * 64
            path.write_text(token + "\n", encoding="ascii")
            os.chmod(path, 0o600)
            self.assertEqual(phase7_seed.owner_token(path), token)
            os.chmod(path, 0o644)
            with self.assertRaises(ValueError):
                phase7_seed.owner_token(path)
            os.chmod(path, 0o600)
            link = Path(folder) / "link.token"
            link.symlink_to(path)
            with self.assertRaises(ValueError):
                phase7_seed.owner_token(link)

    def test_verifier_refuses_public_and_symlinked_state(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "state.json"
            path.write_text('{"key":"QUAL"}', encoding="utf-8")
            os.chmod(path, 0o600)
            self.assertIn("QUAL", phase7_verify.read_private(path))
            os.chmod(path, 0o777)
            with self.assertRaises(ValueError):
                phase7_verify.read_private(path)
            os.chmod(path, 0o600)
            link = Path(folder) / "state-link.json"
            link.symlink_to(path)
            with self.assertRaises(ValueError):
                phase7_verify.read_private(link)

    def test_verifier_refuses_world_readable_or_symlinked_parent_directory(self):
        with tempfile.TemporaryDirectory() as folder:
            private = Path(folder) / "private"
            private.mkdir(mode=0o700)
            path = private / "agent.token"
            path.write_text("qual_pat_" + "a" * 64, encoding="ascii")
            os.chmod(path, 0o600)
            self.assertTrue(phase7_verify.read_private(path).startswith("qual_pat_"))
            os.chmod(private, 0o755)
            with self.assertRaises(ValueError):
                phase7_verify.read_private(path)
            os.chmod(private, 0o700)
            shortcut = Path(folder) / "shortcut"
            shortcut.symlink_to(private, target_is_directory=True)
            with self.assertRaises(ValueError):
                phase7_verify.read_private(shortcut / "agent.token")

    def test_pi_wrapper_rejects_public_or_redirected_token_directory(self):
        with tempfile.TemporaryDirectory() as folder:
            config = Path(folder) / ".config"
            private = config / "qual"
            private.mkdir(parents=True, mode=0o700)
            path = private / "qual-pi-agent.token"
            token = "qual_pat_" + "a" * 64
            path.write_text(token, encoding="ascii")
            os.chmod(path, 0o600)
            env = dict(os.environ, HOME=folder, QUAL_TOKEN="inherited-admin-token")
            def rejected():
                result = subprocess.run(["bash", str(ROOT / "scripts/qual-pi.sh"), "cli", "projects"],
                                        cwd=ROOT, env=env, text=True, capture_output=True, timeout=8)
                self.assertEqual(result.returncode, 3)
                self.assertNotIn(token, result.stdout + result.stderr)
                self.assertNotIn("inherited-admin-token", result.stdout + result.stderr)
                self.assertIn("0700", result.stderr)
            os.chmod(private, 0o755)
            rejected()
            os.chmod(private, 0o700)
            target = config / "realqual"
            private.rename(target)
            private.symlink_to(target, target_is_directory=True)
            rejected()

    def test_seed_disallows_nonlocal_service(self):
        with self.assertRaises(ValueError):
            phase7_seed.seed("e6560273-31f1-4c3f-b690-3b4586977700", Path("/not-present"),
                             base="http://somewhere.invalid:18080")


if __name__ == "__main__":
    unittest.main()
