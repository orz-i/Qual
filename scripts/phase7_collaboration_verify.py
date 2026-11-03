#!/usr/bin/env python3
"""Read-only proof of an existing Human + PI WorkItem closure in local QUAL.

The PI host runs this verifier externally. Qual never runs Agents or tests.
Human actor, independent reviewer, commit and TestRun must already exist;
this script does NOT sign in as a Human, create approval, or mutate work.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import uuid
from datetime import datetime
from pathlib import Path

from phase7_security_smoke import api
from phase7_verify import TOKEN_PATH, PAT, read_private

ROOT = Path(__file__).resolve().parent.parent
SHA = re.compile(r"^[a-f0-9]{40}$")
SHA256 = re.compile(r"^[a-f0-9]{64}$")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RuntimeError(message)


def scoped_cli(*args: str) -> dict:
    # Only the trusted PI wrapper may load QUAL_TOKEN. Its stdout is data,
    # never authorization headers. Do not print subprocess stderr on failure.
    env = dict(os.environ)
    env["QUAL_TOKEN"] = "intentionally-incorrect-inherited-token"
    env["QUAL_API_URL"] = "http://invalid.example"
    response = subprocess.run(
        [str(ROOT / "scripts/qual-pi.sh"), "cli", *args],
        cwd=ROOT, env=env, capture_output=True, text=True, timeout=25,
        check=False,
    )
    require(response.returncode == 0, f"scoped Qual CLI {args[0]} failed")
    try:
        payload = json.loads(response.stdout)
    except json.JSONDecodeError:
        raise RuntimeError("scoped Qual CLI returned invalid JSON") from None
    require(isinstance(payload, dict), "scoped Qual CLI result must be an object")
    return payload


def repo_commit_exists(sha: str) -> bool:
    if not SHA.fullmatch(sha):
        return False
    response = subprocess.run(
        ["git", "cat-file", "-t", sha], cwd=ROOT,
        capture_output=True, text=True, timeout=10, check=False,
    )
    return response.returncode == 0 and response.stdout.strip() == "commit"


def choose_handoff(item: dict, entries: list, principal_types: dict) -> dict:
    """Fail closed on absent/ambiguous roles, self review, or wrong actors."""
    require(item.get("status") == "done", "sample WorkItem is not done")
    agent_id = item.get("created_by")
    require(isinstance(agent_id, str) and principal_types.get(agent_id) == "agent",
            "WorkItem creator must be a real Agent Principal")
    require(item.get("assignee_id") == agent_id, "actual Agent did not own WorkItem")
    require(isinstance(entries, list), "role history missing")
    executors = [r for r in entries if r.get("capacity") == "executor" and
                 r.get("actor_id") == agent_id and r.get("reference_kind") == "git_commit"]
    testers = [r for r in entries if r.get("capacity") == "tester" and
               r.get("actor_id") == agent_id and r.get("outcome") == "passed" and
               r.get("reference_kind") == "test_run"]
    reviewers = [r for r in entries if r.get("capacity") == "reviewer"]
    require(executors and testers and reviewers, "missing actual Agent executor/tester or reviewer")
    # Only the final reviewer decision can support this historical proof.
    try:
        reviewer = max(reviewers, key=lambda r: datetime.fromisoformat(r["recorded_at"]))
    except (KeyError, ValueError, TypeError):
        raise RuntimeError("reviewer timestamp invalid") from None
    require(reviewer.get("outcome") == "passed", "latest reviewer verdict was not Passed")
    executor, tester = executors[-1], testers[-1]
    reviewer_id = reviewer.get("actor_id")
    require(reviewer_id != agent_id and principal_types.get(reviewer_id) == "human" and
            reviewer.get("self_review") is False, "review is not independent Human approval")
    require(reviewer.get("work_status") == "in_review" and
            executor.get("work_status") == "in_progress" and
            tester.get("work_status") == "in_progress", "capacity records not in expected stages")
    require(reviewer.get("reference_kind") == "test_run" and
            reviewer.get("reference_value") == tester.get("reference_value"),
            "reviewer did not cite the executed TestRun")
    require(isinstance(item.get("version"), int) and
            item["version"] > reviewer.get("work_version", 10**12),
            "WorkItem Done version must follow reviewer record")
    try:
        times = [datetime.fromisoformat(x["recorded_at"]) for x in (executor, tester, reviewer)]
        require(times[0] <= times[1] <= times[2], "executor/tester/reviewer order invalid")
    except (KeyError, ValueError, TypeError):
        raise RuntimeError("role timestamps invalid") from None
    commit = executor.get("reference_value", "")
    require(isinstance(commit, str) and SHA.fullmatch(commit), "Git reference not a full commit SHA")
    require(isinstance(tester.get("reference_value"), str), "TestRun reference invalid")
    return {
        "agent_id": agent_id,
        "reviewer_id": reviewer_id,
        "executor_entry_id": executor["id"],
        "tester_entry_id": tester["id"],
        "reviewer_entry_id": reviewer["id"],
        "executor_commit": commit,
        "test_run_id": tester["reference_value"],
        "review_self_review": False,
        "review_work_version": reviewer["work_version"],
    }


def verify_attempts(run: dict, attempts: list, agent_id: str) -> dict:
    require(run.get("source_system", "").startswith("external-"), "TestRun is not externally sourced")
    rows = run.get("items")
    require(isinstance(rows, list) and len(rows) == 1, "expected one fixed CaseVersion RunItem")
    row = rows[0]
    require(row.get("latest_status") == "passed" and row.get("attempt_count", 0) >= 2,
            "RunItem latest status not Passed or missing immutable history")
    require(isinstance(attempts, list) and len(attempts) >= 2, "actual attempts unavailable")
    by_no = sorted(attempts, key=lambda a: a.get("attempt_no", -1))
    require(by_no[0].get("status") == "failed" and by_no[-1].get("status") == "passed",
            "missing actual Failed followed by Passed")
    require(len({a.get("id") for a in by_no}) == len(by_no), "Attempt identities reused")
    require([a.get("attempt_no") for a in by_no] == list(range(1, len(by_no) + 1)),
            "append-only Attempt sequence contains gaps or duplicates")
    require(all(a.get("reported_by") == agent_id and a.get("source_kind") == "external" for a in by_no),
            "attempt history was not really reported by the PI Agent")
    require(row.get("attempt_count") == len(by_no), "RunItem attempt count disagrees with append-only list")
    return {
        "run_item_id": row["id"], "test_case_key": row["test_case_key"],
        "case_version_id": row["case_version_id"],
        "attempts": [{"id": a["id"], "attempt_no": a["attempt_no"], "status": a["status"]}
                     for a in by_no],
        "latest_status": "passed",
    }


def verify_review_activity(activity: list, item_id: str, reviewer_id: str) -> dict:
    """Project activity is limited to its latest 100; absence is inconclusive."""
    require(isinstance(activity, list), "project activity unavailable")
    events = [a for a in activity if a.get("entity_id") == item_id and
              a.get("actor_id") == reviewer_id]
    review = next((e for e in events if e.get("operation") == "work_item.role_recorded"), None)
    completed = next((e for e in events if e.get("operation") == "work_item.transition"), None)
    require(review is not None and completed is not None,
            "recent activity window lacks actual Human reviewer and Done transition; manual audit required")
    try:
        ordered = datetime.fromisoformat(review["occurred_at"]) <= datetime.fromisoformat(completed["occurred_at"])
    except (KeyError, ValueError, TypeError):
        raise RuntimeError("Human activity timestamps invalid") from None
    require(ordered, "Human transition predates reviewer")
    return {
        "human_reviewer_recorded_activity_id": review["id"],
        "human_transition_activity_id": completed["id"],
        "activity_window_limit": 100,
    }


def verify_artifact(run_id: str) -> dict:
    page = scoped_cli("evidence", "list", "-project", "QUAL", "-target-kind", "test_run", "-target", run_id)
    artifacts = [r for r in page.get("items", []) if r.get("state") == "ready" and
                 isinstance(r.get("sha256"), str) and SHA256.fullmatch(r["sha256"])]
    require(artifacts, "no Ready artifact with SHA-256 for TestRun")
    record = sorted(artifacts, key=lambda r: r["id"])[0]
    (ROOT / "tmp" / "phase10").mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="qual3-evidence-", dir=ROOT / "tmp" / "phase10") as folder:
        destination = Path(folder) / "report.bytes"
        scoped_cli("evidence", "download", "-project", "QUAL", "-id", record["id"], "-out", str(destination))
        raw = destination.read_bytes()
        require(hashlib.sha256(raw).hexdigest() == record["sha256"], "private Artifact round-trip SHA-256 mismatch")
        require(len(raw) == record.get("size_bytes"), "private Artifact byte count mismatch")
    return {"artifact_id": record["id"], "sha256": record["sha256"],
            "size_bytes": record["size_bytes"], "authenticated_download_sha256_verified": True}


def verify_existing_handoff(item_id: str) -> dict:
    item_id = str(uuid.UUID(item_id))
    project_page = scoped_cli("projects")
    projects = project_page.get("items", [])
    require(len(projects) == 1 and projects[0].get("key") == "QUAL", "Agent project visibility changed")
    pid = projects[0]["id"]
    current = scoped_cli("access", "me", "-project", "QUAL")
    agent_id = current.get("principal_id")
    require(isinstance(agent_id, str) and "work:review" not in current.get("effective_permissions", []),
            "Agent credential cannot be used to verify independent Human review")
    item = scoped_cli("show", "-project", "QUAL", "-item", item_id)
    token = read_private(TOKEN_PATH)
    require(PAT.fullmatch(token), "PI scoped credential missing or malformed")
    code, rest = api(f"/api/v1/projects/{pid}/work-items/{item_id}", token)
    require(code == 200 and all(item.get(k) == rest.get(k) for k in ("id", "key", "version", "status")),
            "PI CLI/REST WorkItem status or version drift")
    entries = scoped_cli("role", "list", "-project", "QUAL", "-item", item_id).get("items", [])
    reviewer_ids = {r.get("actor_id") for r in entries if r.get("capacity") == "reviewer"}
    require(len(reviewer_ids) == 1, "reviewer principal history ambiguous")
    reviewer_id = next(iter(reviewer_ids))
    ids = ",".join((agent_id, reviewer_id))
    code, people = api(f"/api/v1/projects/{pid}/principals?ids={ids}", token)
    require(code == 200, "cannot resolve actual project actor identity types")
    types = {p["id"]: p["kind"] for p in people.get("items", [])}
    require(len(types) == 2 and types.get(agent_id) == "agent" and types.get(reviewer_id) == "human",
            "Human/PI identity not independently resolved from real server")
    handoff = choose_handoff(item, entries, types)
    require(handoff["agent_id"] == agent_id, "Executor is not this actual PI Agent")
    require(repo_commit_exists(handoff["executor_commit"]), "executed Git commit not found in current checkout")
    runs = scoped_cli("test", "runs", "-project", "QUAL").get("items", [])
    run = next((r for r in runs if r.get("id") == handoff["test_run_id"]), None)
    require(run is not None, "reviewed TestRun is absent from scoped project")
    run_items = run.get("items", [])
    require(len(run_items) == 1, "reviewed TestRun has ambiguous RunItems")
    attempts = scoped_cli("test", "attempts", "-project", "QUAL", "-item", run_items[0]["id"]).get("items", [])
    attempt_proof = verify_attempts(run, attempts, agent_id)
    activity = scoped_cli("activity", "-project", "QUAL", "-limit", "100").get("items", [])
    activity_proof = verify_review_activity(activity, item_id, reviewer_id)
    evidence = verify_artifact(run["id"])
    return {
        "scope": "read-only recheck of already completed local Human/PI collaboration",
        "work_item": {k: item[k] for k in ("id", "key", "status", "version")},
        "cli_rest_same_work_item_and_version": True,
        "agent_principal": {"id": agent_id, "kind": types[agent_id]},
        "reviewer_principal": {"id": reviewer_id, "kind": types[reviewer_id], "independent": True},
        "roles": handoff,
        "test_run": {"id": run["id"], "source_system": run["source_system"], **attempt_proof},
        "private_artifact": evidence,
        "activity": activity_proof,
        "limitations": [
            "this call does not create an authenticated Human browser session; existing Human action is verified from stored roles/activity",
            "the project activity endpoint exposes only its latest 100 records and cannot act as perpetual audit pagination",
            "this does not authorize PI to review or complete QUAL-3",
        ],
        "result": "PASS",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--item", required=True, help="existing done WorkItem UUID with independent Human review")
    parser.add_argument("--out", help="optional local JSON report; never contains credentials")
    args = parser.parse_args()
    try:
        result = verify_existing_handoff(args.item)
        content = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
        if args.out:
            destination = Path(args.out)
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(content, encoding="utf-8")
        print(content, end="")
        return 0
    except Exception as exc:
        # Always emit normalized safe categories, never HTTP bodies or secrets.
        print(f"phase7-collaboration-verify: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
