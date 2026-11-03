"""Public source mirror policy tests; no remote writes or public branch mutation."""
import importlib.util
from pathlib import Path
import re
import unittest
from unittest import mock

PATH = Path(__file__).resolve().parents[1] / "ops/qual_public_export.py"
SPEC = importlib.util.spec_from_file_location("qual_public_export", PATH)
export = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(export)


class PublicMirrorSafetyTests(unittest.TestCase):
    def test_private_dirs_never_in_public_tree(self):
        self.assertTrue({"docs/design", "docs/pilot", "docs/engineering"} <= set(export.EXCLUDED))
        self.assertTrue({".png", ".jpg", ".webp", ".pdf", ".zip"} <= export.BIN_SUFFIXES)

    def test_readme_and_docs_index_are_replaced_with_purpose_built_public_text(self):
        self.assertEqual(set(export.TEMPLATES), {"README.md", "AGENTS.md", "docs/README.md"})
        for path in export.TEMPLATES.values():
            text = (export.ROOT / path).read_text(encoding="utf-8")
            self.assertIn("{{SOURCE_COMMIT}}", text)
            self.assertNotIn("Internal Human screenshot captured", text)

    def test_sensitive_prefixes_are_rejected_as_data_not_plaintext_in_test(self):
        for rule, pattern in export.BLOCKED.items():
            self.assertIsInstance(rule, str)
            self.assertIsInstance(pattern, bytes)
        fake_login = b"qlc_" + b"a" * 64
        fake_pat = b"qual_pat_" + b"b" * 64
        self.assertIsNotNone(re.search(export.BLOCKED["qual_human_login"], fake_login))
        self.assertIsNotNone(re.search(export.BLOCKED["qual_pat"], fake_pat))

    def test_public_export_never_calls_git_push(self):
        source = PATH.read_text(encoding="utf-8")
        self.assertNotIn('"push"', source)
        self.assertNotIn('"--force"', source)
        self.assertIn('"update-ref"', source)
        self.assertIn('"commit-tree"', source)

    def test_branch_gate_refuses_non_main(self):
        with mock.patch.object(export, "git", side_effect=[
            b"f" * 40, b"feature/external-adapter\n"
        ]):
            with self.assertRaisesRegex(export.ExportGuard, "local main"):
                export.verify_root()

    def test_dirty_main_refuses_snapshot(self):
        with mock.patch.object(export, "git", side_effect=[
            b"f" * 40, b"main\n", b" M apps/server/cmd/server/main.go\n"
        ]):
            with self.assertRaisesRegex(export.ExportGuard, "dirty"):
                export.verify_root()


if __name__ == "__main__":
    unittest.main()
