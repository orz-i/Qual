"""Offline guard tests for the *real* Goose13 operator runner; never migrates a DB."""
import importlib.util
import hashlib
from pathlib import Path
import plistlib
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[2]
script=ROOT/"scripts/ops/qual_goose13_rollout.py"
spec=importlib.util.spec_from_file_location("goose13_rollout",script)
op=importlib.util.module_from_spec(spec)
spec.loader.exec_module(op)


class Goose13OperatorTests(unittest.TestCase):
    def test_postcutover_probe_dependencies_exist(self):
        # Ensure runtime imports used only by the live Goose13 branch are loaded.
        self.assertTrue(hasattr(op, 'tempfile'))
        self.assertTrue(hasattr(op.tempfile, 'TemporaryDirectory'))

    def test_release_sha_is_reviewed(self):
        op.enforce_hash(op.NEW,op.NEW_HASH)
        op.enforce_hash(op.MIGRATION,op.MIGRATION_HASH)

    def test_migration_contains_guards(self):
        s=op.MIGRATION.read_text()
        self.assertEqual(s.count("-- +goose Up"),1)
        self.assertEqual(s.count("-- +goose Down"),1)
        self.assertIn("Goose13 Down refused",s)
        for t in ("work_acceptance_decisions","review_policy_changes","work_review_policy_overrides","project_review_policies"):
            self.assertIn(t,s)

    def test_launchd_plist_has_no_secrets_and_correct_wrapper(self):
        with tempfile.TemporaryDirectory(prefix="goose13-plist-",dir=str(op.prior.STATE_DIR)) as d:
            original=op.OLD_PLIST
            try:
                op.OLD_PLIST=Path(d)/"original.plist"
                op.prior.safe_write(op.OLD_PLIST,plistlib.dumps({
                    "Label":op.prior.LABEL,
                    "ProgramArguments":["/usr/bin/python3",str(op.ROOT/"scripts/ops/qual_launchd_server.py"),"run"],
                    "WorkingDirectory":str(op.ROOT),
                    "RunAtLoad":True,
                }))
                data=op.new_plist()
                parsed=plistlib.loads(data)
                self.assertEqual(parsed["ProgramArguments"][0],"/usr/bin/python3")
                self.assertEqual(parsed["ProgramArguments"][1],str(op.script) if hasattr(op,"script") else str(script))
                self.assertEqual(parsed["ProgramArguments"][2],"run")
                self.assertNotIn(b"PASSWORD",data)
                self.assertNotIn(b"qual_pat_",data)
            finally:
                op.OLD_PLIST=original

    def test_current_backup_is_not_checked_in(self):
        self.assertTrue(str(op.BACKUP_RECEIPT).startswith(str(Path.home())))
        self.assertTrue(str(op.OLD_PLIST).startswith(str(Path.home())))


if __name__ == "__main__":
    unittest.main()
