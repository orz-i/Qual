"""Regression for the operator-only Goose release preflight."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "ops/check_migration_sequence.py"
SPEC = importlib.util.spec_from_file_location("check_migration_sequence", SOURCE)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class MigrationSequenceGuardTests(unittest.TestCase):
    def create(self, names):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        root = Path(tmp.name)
        for name in names:
            (root / name).write_text("-- +goose Up\nSELECT 1;\n")
        return root

    def test_current_monotonic_sequence(self):
        root = Path(__file__).resolve().parents[2] / "apps/server/migrations"
        self.assertEqual(MODULE.check_sequence(root), (1, 17))

    def release_names(self):
        return [
            MODULE.FROZEN_MIGRATIONS.get(number, f"{number:05d}_change.sql")
            for number in range(1, 18)
        ]

    def test_future_14_15_16_17_chain(self):
        root = self.create(self.release_names())
        self.assertEqual(MODULE.check_sequence(root), (1, 17))

    def test_release_16_without_15_must_fail(self):
        root = self.create([name for name in self.release_names() if not name.startswith("00015_")])
        with self.assertRaisesRegex(ValueError, "missing Goose migrations: 15"):
            MODULE.check_sequence(root)

    def test_allocated_15_cannot_be_replaced_by_future_event_migration(self):
        names = ["00015_external_events.sql" if name.startswith("00015_") else name
                 for name in self.release_names()]
        root = self.create(names)
        with self.assertRaisesRegex(ValueError, "Goose 15 is allocated"):
            MODULE.check_sequence(root)

    def test_allocated_14_16_17_are_frozen(self):
        for number in (14, 16, 17):
            names = [f"{number:05d}_conflict.sql" if name.startswith(f"{number:05d}_") else name
                     for name in self.release_names()]
            root = self.create(names)
            with self.assertRaisesRegex(ValueError, f"Goose {number} is allocated"):
                MODULE.check_sequence(root)

    def test_new_goose17_requires_no_gaps(self):
        root=self.create([name for name in self.release_names() if not name.startswith("00016_")])
        with self.assertRaisesRegex(ValueError,"missing Goose migrations: 16"):
            MODULE.check_sequence(root)

    def test_duplicate_version_rejected(self):
        root = self.create(["00001_one.sql", "00001_two.sql"])
        with self.assertRaisesRegex(ValueError, "duplicate Goose version"):
            MODULE.check_sequence(root)

    def test_nonnumeric_filename_rejected(self):
        root = self.create(["00001_one.sql", "migration.sql"])
        with self.assertRaisesRegex(ValueError, "invalid Goose filename"):
            MODULE.check_sequence(root)

    def test_missing_directory_rejected(self):
        with self.assertRaisesRegex(ValueError, "directory not found"):
            MODULE.check_sequence(Path("/nonexistent/qual-migration-dir"))


if __name__ == "__main__":
    unittest.main()
