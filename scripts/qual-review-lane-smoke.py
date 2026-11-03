#!/usr/bin/env python3
"""Read-only, reproducible QUAL ReviewQueue regression probe.

Compares externally run Go server versions using the same real QUAL data.
Does not create WorkItems, Attempts, Bugs or permissions. No credentials logged.
"""
import argparse
import datetime
import json
import os
from pathlib import Path
import stat
import sys
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

PROJECT_ID = "e6560273-31f1-4c3f-b690-3b4586977700"

def validate_review_queue(data):
    """Return specific semantic failures, preserving raw WorkItem statuses."""
    if not isinstance(data.get("items"), list):
        return ["API response lacks an items array"]
    failures = []
    rows = data["items"]
    if not rows:
        failures.append("No review entries in project: acceptance scenario not exercised")
    for item in rows:
        key = item.get("key", "<unknown>")
        state = item.get("status")
        review = item.get("latest_review") or {}
        outcome = review.get("outcome")
        self_review = review.get("self_review", False)
        resubmitted = item.get("resubmitted_after_review", False)
        if resubmitted and (state != "in_review" or not review):
            failures.append(f"{key}: invalid resubmission without a current in_review and prior Reviewer")
            continue
        if resubmitted:
            # The server verified an explicit in_review transition after the
            # prior Reviewer event; this requires a FRESH review, not approval.
            expected = "awaiting"
        elif outcome in ("needs_work", "failed"):
            expected = "needs_work"
        elif state == "in_review" and outcome == "passed" and not self_review:
            expected = "ready_to_close"
        elif state == "in_review":
            expected = "awaiting"
        else:
            failures.append(f"{key}: unexpected queue inclusion: {state}, {outcome}")
            continue
        if item.get("review_lane") != expected:
            failures.append(f"{key}: expected review_lane={expected}, got {item.get('review_lane')!r}")
        if state == "done":
            failures.append(f"{key}: Done unexpectedly included in review queue")
    return failures

def validate_required_resubmission(data, key):
    """Fail closed when the running binary does not expose fresh re-review.

    Requiring a specific WorkItem is a release acceptance gate, NOT proof that
    the Reviewer has approved it. A legacy server must fail this check.
    """
    rows = data.get('items') if isinstance(data, dict) else None
    if not isinstance(rows, list):
        return ['ReviewQueue response has no items list']
    matches = [row for row in rows if isinstance(row, dict) and row.get('key') == key]
    if len(matches) != 1:
        return [f'{key}: expected exactly one matching work item in review queue']
    item = matches[0]
    review = item.get('latest_review') or {}
    if (item.get('status') != 'in_review' or
            item.get('resubmitted_after_review') is not True or
            item.get('review_lane') != 'awaiting' or
            review.get('outcome') not in ('needs_work', 'failed')):
        return [f'{key}: running Go API has not verified a fresh pending re-review after prior rejection']
    return []


def private_token():
    path = Path.home() / ".config/qual/qual-pi-agent.token"
    file_stat = path.lstat()
    if not stat.S_ISREG(file_stat.st_mode) or stat.S_IMODE(file_stat.st_mode) != 0o600 or file_stat.st_uid != os.getuid():
        raise RuntimeError("A private owner-only scoped credential is required")
    value = path.read_text().strip()
    if not value.startswith("qual_pat_") or len(value) != 73:
        raise RuntimeError("Unexpected private credential format")
    return value

def run():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--build-id", required=True, help="Grounded commit or binary hash, never a credential")
    parser.add_argument("--result", required=True, help="Local JSON evidence path; never stores a credential")
    parser.add_argument("--require-resubmission", help="Require an audited post-remediation awaiting item; old server must fail")
    args = parser.parse_args()
    url = urlsplit(args.base_url)
    if url.scheme != "http" or url.hostname not in ("127.0.0.1", "localhost") or url.username or url.password or url.query or url.fragment:
        raise RuntimeError("Only local loopback HTTP test targets are supported")
    token = private_token()
    path = f"/api/v1/projects/{PROJECT_ID}/review-queue?limit=50"
    req = Request(args.base_url.rstrip("/") + path, headers={"Authorization": "Bearer " + token},method="GET")
    with urlopen(req, timeout=7) as response:
        status = response.status
        data = json.load(response)
    failures = validate_review_queue(data)
    if args.require_resubmission:
        failures.extend(validate_required_resubmission(data, args.require_resubmission))
    report = {
        "source": "executed real loopback Go API, scoped real PI Agent PAT; no write operations",
        "observed_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "build_id": args.build_id,
        "request_method": "GET",
        "api_path": path,
        "http_status": status,
        "work_item_count": len(data.get("items", [])),
        "required_resubmission_key": args.require_resubmission,
        "running_binary_resubmission_gate_passed": bool(args.require_resubmission) and status == 200 and not failures,
        "passed": status == 200 and not failures,
        "failures": failures,
    }
    out = Path(args.result)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")
    print(f"ReviewQueue contract: {'PASS' if report['passed'] else 'FAIL'} build={args.build_id} checked={report['work_item_count']}")
    for failure in failures:
        print(" -", failure)
    return 0 if report["passed"] else 1

if __name__ == "__main__":
    try:
        sys.exit(run())
    except Exception as exc:
        # Never include request headers or token values in exception output.
        print("Regression probe could not finish:",type(exc).__name__,file=sys.stderr)
        sys.exit(2)
