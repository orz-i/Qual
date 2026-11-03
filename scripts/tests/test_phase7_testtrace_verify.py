"""Offline tests for read-only Qual test-result provenance validation."""
import copy
import io
import json
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import phase7_testtrace_verify as trace  # noqa: E402

CASE = "11111111-1111-4111-8111-111111111111"
VERSION = "22222222-2222-4222-8222-222222222222"
RUN = "33333333-3333-4333-8333-333333333333"
ITEM = "44444444-4444-4444-8444-444444444444"
ART = "55555555-5555-4555-8555-555555555555"
PROJECT = "66666666-6666-4666-8666-666666666666"
SHA = "e" * 64


def fixture():
    run = {
        "id": RUN, "project_id": PROJECT,
        "items": [{
            "id": ITEM, "test_case_id": CASE, "test_case_key": "QUAL-TC-3",
            "case_version_id": VERSION, "case_version": 1,
            "snapshot_sha256": SHA, "attempt_count": 2,
            "latest_status": "passed",
        }],
    }
    versions = {"items": [{
        "id": VERSION, "test_case_id": CASE, "version": 1, "sha256": SHA,
    }]}
    history = {"items": [
        {"id": "a", "run_item_id": ITEM, "attempt_no": 2, "status": "passed",
         "source_kind": "external", "reported_by": "agent", "ingested_at": "2026-10-31"},
        {"id": "b", "run_item_id": ITEM, "attempt_no": 1, "status": "failed",
         "source_kind": "external", "reported_by": "agent", "ingested_at": "2026-10-31"},
    ]}
    art = {"id": ART, "project_id": PROJECT, "state": "ready",
           "target_kind": "test_run", "target_id": RUN,
           "sha256": SHA, "size_bytes": 128}
    return run, versions, history, art


class ProvenanceTests(unittest.TestCase):
    def test_failed_then_passed_preserves_both_observations(self):
        r, v, h, a = fixture()
        result = trace.validate_trace(r, v, h, CASE, a)
        self.assertEqual(result["attempt_statuses_oldest_first"], ["failed", "passed"])
        self.assertEqual(result["latest_status"], "passed")
        self.assertEqual(result["case_version_sha256"], SHA)
        self.assertEqual(result["artifact_id"], ART)

    def test_missing_or_wrong_case_version_rejected(self):
        for alteration in ("hash", "version", "missing"):
            with self.subTest(alteration=alteration):
                r, v, h, a = fixture()
                if alteration == "hash":
                    v["items"][0]["sha256"] = "f" * 64
                if alteration == "version":
                    r["items"][0]["case_version"] = 2
                if alteration == "missing":
                    v["items"].clear()
                with self.assertRaisesRegex(ValueError, "CaseVersion|SHA|version"):
                    trace.validate_trace(r, v, h, CASE, a)

    def test_missing_or_partial_attempt_history_rejected(self):
        for alteration in ("empty", "partial", "duplicate", "gap", "wrong_item"):
            with self.subTest(alteration=alteration):
                r, v, h, a = fixture()
                if alteration == "empty":
                    h["items"] = []
                if alteration == "partial":
                    h["items"].pop()
                if alteration == "duplicate":
                    h["items"][0]["attempt_no"] = 1
                if alteration == "gap":
                    h["items"][0]["attempt_no"] = 5
                if alteration == "wrong_item":
                    h["items"][0]["run_item_id"] = RUN
                with self.assertRaisesRegex(ValueError, "Attempt|observed"):
                    trace.validate_trace(r, v, h, CASE, a)

    def test_latest_status_mismatch_rejected(self):
        r, v, h, a = fixture()
        r["items"][0]["latest_status"] = "failed"
        with self.assertRaisesRegex(ValueError, "latest"):
            trace.validate_trace(r, v, h, CASE, a)

    def test_reporter_source_missing_rejected(self):
        r, v, h, a = fixture()
        h["items"][0]["reported_by"] = ""
        with self.assertRaisesRegex(ValueError, "reporter"):
            trace.validate_trace(r, v, h, CASE, a)

    def test_duplicate_binding_and_wrong_case_rejected(self):
        r, v, h, a = fixture()
        r["items"].append(copy.deepcopy(r["items"][0]))
        with self.assertRaisesRegex(ValueError, "exactly one"):
            trace.validate_trace(r, v, h, CASE, a)
        r["items"] = []
        with self.assertRaisesRegex(ValueError, "exactly one"):
            trace.validate_trace(r, v, h, CASE, a)

    def test_artifact_not_ready_or_wrong_run_rejected(self):
        for change in ("state", "target_id", "sha256", "size_bytes"):
            with self.subTest(change=change):
                r, v, h, a = fixture()
                a[change] = "uploaded" if change == "state" else RUN + "wrong" if change == "target_id" else "x" if change == "sha256" else 0
                with self.assertRaisesRegex(ValueError, "Artifact"):
                    trace.validate_trace(r, v, h, CASE, a)

    def test_live_entrypoint_get_only_and_never_prints_token(self):
        r, v, h, a = fixture()
        token = "qual_pat_" + "1" * 64
        seed = json.dumps({"project_id": PROJECT, "project_key": "QUAL"})
        requested = []
        def mocked_read(path, *rest):
            return token if path == trace.TOKEN_PATH else seed
        def mocked_api(path, bearer):
            requested.append(path)
            self.assertEqual(bearer, token)
            if path.endswith("/test-runs/" + RUN):
                return 200, r
            if path.endswith("/test-cases/" + CASE + "/versions"):
                return 200, v
            if path.endswith("/test-run-items/" + ITEM + "/attempts"):
                return 200, h
            if path.endswith("/artifacts/" + ART):
                return 200, a
            self.fail("unexpected REST path " + path)
        out = io.StringIO()
        with patch.object(trace, "read_private", side_effect=mocked_read), patch.object(trace, "api", side_effect=mocked_api):
            with redirect_stdout(out):
                status = trace.main(["--run", RUN, "--case", CASE, "--artifact", ART])
        self.assertEqual(status, 0)
        self.assertEqual(len(requested), 4)
        self.assertNotIn(token, out.getvalue())
        self.assertEqual(json.loads(out.getvalue())["result"], "PASS")

    def test_live_entrypoint_fails_closed_on_unauthorized_read(self):
        token = "qual_pat_" + "1" * 64
        seed = json.dumps({"project_id": PROJECT, "project_key": "QUAL"})
        with patch.object(trace, "read_private", side_effect=[token, seed]):
            with patch.object(trace, "api", return_value=(403, {"error": {"code": "PERMISSION_DENIED"}})):
                with redirect_stderr(io.StringIO()):
                    self.assertEqual(trace.main(["--run", RUN, "--case", CASE]), 1)


if __name__ == "__main__":
    unittest.main()
