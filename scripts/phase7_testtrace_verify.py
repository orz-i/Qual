#!/usr/bin/env python3
"""Read-only verification of externally reported Qual TestRun provenance.

This self-dogfood check uses only the PI Agent's scoped PAT and loopback REST:
it never executes a test, creates an Attempt or accesses the operator database.
RunItem -> immutable published CaseVersion/SHA, append-only Attempt numbering,
latest-status projection, and optional S3 Artifact metadata must agree.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
import uuid

from phase7_security_smoke import api
from phase7_verify import PAT, SEED_STATE, TOKEN_PATH, read_private

SHA256 = re.compile(r"^[0-9a-f]{64}$")
STATUSES = {"passed", "failed", "blocked", "skipped"}


def validate_trace(
    run: dict, versions: dict, history: dict, case_id: str,
    artifact: dict | None = None,
) -> dict:
    """Validate persisted read models without assuming that tests ran in Qual."""
    if not isinstance(run, dict) or not isinstance(run.get("id"), str):
        raise ValueError("TestRun missing")
    rows = run.get("items")
    if not isinstance(rows, list):
        raise ValueError("TestRun items missing")
    matching = [x for x in rows if isinstance(x, dict) and x.get("test_case_id") == case_id]
    if len(matching) != 1:
        raise ValueError("exactly one RunItem must bind the requested TestCase")
    item = matching[0]
    if not isinstance(item.get("id"), str) or not isinstance(item.get("case_version_id"), str):
        raise ValueError("RunItem missing immutable version reference")
    snapshots = versions.get("items") if isinstance(versions, dict) else None
    if not isinstance(snapshots, list):
        raise ValueError("published TestCase versions missing")
    found = [v for v in snapshots if isinstance(v, dict) and v.get("id") == item["case_version_id"]]
    if len(found) != 1:
        raise ValueError("RunItem has no unique published CaseVersion")
    version = found[0]
    digest = item.get("snapshot_sha256")
    if (version.get("test_case_id") != case_id
        or version.get("version") != item.get("case_version")
        or not isinstance(digest, str) or not SHA256.fullmatch(digest)
        or digest != version.get("sha256")):
        raise ValueError("frozen RunItem version/SHA differs from published CaseVersion")

    attempts = history.get("items") if isinstance(history, dict) else None
    if not isinstance(attempts, list) or not attempts:
        raise ValueError("no observed Attempts: do not infer a pass from an empty run")
    expected_count = item.get("attempt_count")
    if not isinstance(expected_count, int) or expected_count != len(attempts):
        raise ValueError("Attempt history is incomplete or disagrees with RunItem count")
    by_number: dict[int, dict] = {}
    for a in attempts:
        if not isinstance(a, dict) or a.get("run_item_id") != item["id"]:
            raise ValueError("Attempt belongs to a different RunItem")
        number = a.get("attempt_no")
        if not isinstance(number, int) or isinstance(number, bool) or number in by_number:
            raise ValueError("Attempt sequence contains invalid/duplicate number")
        if (a.get("status") not in STATUSES
            or not a.get("id") or not a.get("reported_by")
            or not a.get("source_kind") or not a.get("ingested_at")):
            raise ValueError("Attempt missing real reporter, source, or result")
        by_number[number] = a
    if sorted(by_number) != list(range(1, expected_count + 1)):
        raise ValueError("Attempt history has a gap")
    latest = by_number[expected_count]["status"]
    if item.get("latest_status") != latest:
        raise ValueError("RunItem latest status is inconsistent with append-only Attempts")

    detail = {
        "run_id": run["id"],
        "run_item_id": item["id"],
        "test_case_key": item.get("test_case_key"),
        "case_version_id": version["id"],
        "case_version": version["version"],
        "case_version_sha256": digest,
        "attempt_count": expected_count,
        "attempt_statuses_oldest_first": [by_number[i]["status"] for i in range(1, expected_count + 1)],
        "latest_status": latest,
    }
    if artifact is not None:
        if (artifact.get("state") != "ready"
            or artifact.get("target_kind") != "test_run"
            or artifact.get("target_id") != run["id"]
            or not isinstance(artifact.get("sha256"), str)
            or not SHA256.fullmatch(artifact["sha256"])
            or not isinstance(artifact.get("size_bytes"), int)
            or artifact["size_bytes"] < 1):
            raise ValueError("private Artifact is not ready or does not belong to this TestRun")
        detail["artifact_id"] = artifact.get("id")
        detail["artifact_sha256"] = artifact["sha256"]
        detail["artifact_bytes"] = artifact["size_bytes"]
    return detail


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, help="actual TestRun UUID")
    parser.add_argument("--case", required=True, help="published TestCase UUID")
    parser.add_argument("--artifact", help="optional ready Artifact UUID bound to the TestRun")
    args = parser.parse_args(argv)
    try:
        run_id = str(uuid.UUID(args.run))
        case_id = str(uuid.UUID(args.case))
        artifact_id = str(uuid.UUID(args.artifact)) if args.artifact else ""
        token = read_private(TOKEN_PATH)
        if not PAT.fullmatch(token):
            raise ValueError("invalid PI credential format")
        state = json.loads(read_private(SEED_STATE, 200_000))
        pid = str(uuid.UUID(state["project_id"]))
        if state.get("project_key") != "QUAL":
            raise ValueError("PI workspace is not QUAL")

        def get(path: str) -> dict:
            code, body = api(f"/api/v1/projects/{pid}/" + path, token)
            if code != 200:
                raise ValueError(f"scoped read rejected: HTTP {code} for {path.split('/')[0]}")
            return body

        run = get(f"test-runs/{run_id}")
        if run.get("id") != run_id or run.get("project_id") != pid:
            raise ValueError("TestRun project/id mismatch")
        versions = get(f"test-cases/{case_id}/versions")
        candidates = [i for i in run.get("items", []) if i.get("test_case_id") == case_id]
        if len(candidates) != 1:
            raise ValueError("specified Case not uniquely bound to the Run")
        history = get(f"test-run-items/{candidates[0]['id']}/attempts")
        artifact = get(f"artifacts/{artifact_id}") if artifact_id else None
        if artifact is not None and artifact.get("project_id") != pid:
            raise ValueError("Artifact project mismatch")
        result = validate_trace(run, versions, history, case_id, artifact)
        print(json.dumps({"result": "PASS", "project_key": "QUAL", **result}, ensure_ascii=False, indent=2))
        return 0
    except (ValueError, KeyError, TypeError, OSError) as exc:
        print(f"qual-testtrace: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
