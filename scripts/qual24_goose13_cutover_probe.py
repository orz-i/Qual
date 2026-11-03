#!/usr/bin/env python3
"""Read-only, fail-closed Qual Goose13 cutover verification.

This probe deliberately cannot change schema, WorkItem state or review policy.
Only scoped PI PAT and loopback HTTP GET are used. A healthy Goose12 server
must FAIL: readiness 200 alone is not an acceptance of Goose13 rollout.
Run only after an authorized Human Operator deploys the new server/schema.
"""
from __future__ import annotations

import argparse
import datetime
import json
import sys
from pathlib import Path

from phase7_security_smoke import api
from phase7_verify import BASE, PAT, SEED_STATE, TOKEN_PATH, read_private

LEGACY_DONE = "68c800cb-8c70-48c5-bffe-4d5968ae20d1"  # historical QUAL-2, v13, already Done
PENDING_ITEM = "78e3bbae-2110-4f69-9007-3602b51c134f"  # QUAL-24, not Done before post-cutover verification


def validate_cutover(policy: object, legacy: object, pending: object,
                     effective: object, work: object) -> list[str]:
    failures: list[str] = []
    if not isinstance(policy, dict) or policy.get("default_mode") != "independent_review" or \
            policy.get("owner_acceptance_allowed") is not False or \
            policy.get("version") != 0:
        failures.append("no verified conservative Goose13 project policy baseline")
    if not isinstance(legacy, dict) or legacy.get("work_item_id") != LEGACY_DONE or \
            legacy.get("work_status") != "done" or legacy.get("acceptance_mode") != "legacy_unclassified" or \
            legacy.get("decision") is not None:
        failures.append("old Human Done was not presented as unclassified legacy history")
    if not isinstance(work, dict) or work.get("id") != PENDING_ITEM or \
            work.get("status") not in ("todo", "in_progress", "in_review") or \
            not isinstance(work.get("version"), int):
        failures.append("QUAL-24 release task is missing or already completed before cutover verification")
    if not isinstance(pending, dict) or pending.get("work_item_id") != PENDING_ITEM or \
            pending.get("acceptance_mode") != "not_completed" or pending.get("decision") is not None or \
            not isinstance(work, dict) or pending.get("work_status") != work.get("status") or \
            pending.get("work_version") != work.get("version"):
        failures.append("pending WorkItem lacks a consistent, undecided acceptance history")
    if not isinstance(effective, dict) or effective.get("work_item_id") != PENDING_ITEM or \
            effective.get("mode") != "independent_review" or effective.get("owner_acceptance_allowed") is not False:
        failures.append("WorkItem effective mode or project safety bound not validated")
    return failures


def run() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--result", required=True, help="Local JSON-only result; never stores token or HTTP response bodies")
    args = parser.parse_args()
    token = read_private(TOKEN_PATH)
    if not PAT.fullmatch(token):
        raise RuntimeError("PI Credential invalid")
    state = json.loads(read_private(SEED_STATE, 200_000))
    if state.get("project_key") != "QUAL":
        raise RuntimeError("unexpected scoped PI project")
    base = f"/api/v1/projects/{state['project_id']}"

    endpoints = {
        "policy": "/review-policy",
        "historic_done": f"/work-items/{LEGACY_DONE}/acceptances",
        "pending_review": f"/work-items/{PENDING_ITEM}/acceptances",
        "effective_review": f"/work-items/{PENDING_ITEM}/review-policy",
        "work": f"/work-items/{PENDING_ITEM}",
    }
    statuses: dict[str, int] = {}
    objects: dict[str, object] = {}
    for name, path in endpoints.items():
        status, body = api(base + path, token)
        statuses[name] = status
        objects[name] = body if status == 200 else None
    failures = []
    if any(value != 200 for value in statuses.values()):
        failures.append("new Goose13 policy/history endpoints are not all deployed and authorized")
    else:
        failures.extend(validate_cutover(objects["policy"], objects["historic_done"],
                                         objects["pending_review"], objects["effective_review"], objects["work"]))
    result = {
        "observed_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "base_url": BASE, "project": "QUAL", "method": "GET only",
        "status_codes": statuses, "historical_work_item_key": "QUAL-2",
        "pending_work_item_key": "QUAL-24",
        "passed": not failures,
        "failures": failures,
        "does_not_infer_green_readyz_means_goose13": True,
        "real_Human_approval_executed": False,
        "credential_values_serialized": False,
    }
    path = Path(args.result)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(result, indent=2, ensure_ascii=False) + "\n")
    print("QUAL-24 Goose13 loopback gate: " + ("PASS" if not failures else "FAIL")
          + "; verified HTTP code collection and expected WorkItem modes")
    return 0 if not failures else 1


if __name__ == "__main__":
    try:
        sys.exit(run())
    except (RuntimeError, ValueError):
        print("Goose13 gate could not finish safely; no database or WorkItems were changed", file=sys.stderr)
        sys.exit(2)
