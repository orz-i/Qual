#!/usr/bin/env python3
"""Phase 7 live PI security probe, against Qual loopback REST only.

This is a deliberately non-destructive negative/consistency test:
- Read QUAL with the exact project-scoped PI Agent identity.
- Compare the same WorkItem from CLI and REST, including the optimistic version.
- Ensure anonymous, forged-token, and nonexistent-project reads disclose nothing.
- If review is NOT delegated and a real item is in_review, exercise a denied
  transition with deliberately stale CAS. Otherwise skip the dangerous live
  mutation; a delegated Reviewer is never probed with a potentially valid Done.
- Validate current Credential Scope + Project permission without treating a
  legitimate Human-granted review/audit delegation as a security failure.
- An existing foreign project's membership isolation is tested separately in
  the isolated PostgreSQL integration suite, not inferred from a random UUID.
- Confirm the original records and versions are unchanged afterwards.
No test runner, Agent, DB admin, PI registration or CI is launched by Qual.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
import uuid
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from phase7_verify import BASE, PRIVATE, SEED_STATE, TOKEN_PATH, PAT, read_private, inspect_agent_access

ROOT = Path(__file__).resolve().parent.parent
FAKE_TOKEN = "qual_pat_" + "0" * 64
UNKNOWN_PROJECT = "ffffffff-ffff-4fff-afff-ffffffffffff"


def api(
    path: str,
    token: str = "",
    *,
    method: str = "GET",
    body: dict | None = None,
    idempotency_key: str | None = None,
    if_match: int | None = None,
) -> tuple[int, dict]:
    if not path.startswith("/api/v1/"):
        raise ValueError("security probe supports only Qual REST business paths")
    data = json.dumps(body, separators=(",", ":")).encode("utf-8") if body is not None else None
    headers: dict[str, str] = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if body is not None:
        headers["Content-Type"] = "application/json"
    if idempotency_key:
        headers["Idempotency-Key"] = idempotency_key
    if if_match is not None:
        headers["If-Match"] = f'"{if_match}"'
    request = Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urlopen(request, timeout=10) as response:
            data = json.load(response)
            return response.status, data if isinstance(data, dict) else {}
    except HTTPError as exc:
        raw = exc.read(8192)
        try:
            data = json.loads(raw)
        except (json.JSONDecodeError, UnicodeDecodeError):
            data = {}
        return exc.code, data if isinstance(data, dict) else {}
    except URLError:
        raise RuntimeError("Qual loopback API unavailable") from None


def ensure_denied(
    response: tuple[int, dict],
    expected_status: int,
    expected_code: str,
    secrets: tuple[str, ...],
) -> str:
    code, payload = response
    error = payload.get("error", {})
    actual_code = error.get("code") if isinstance(error, dict) else None
    if code != expected_status or actual_code != expected_code:
        raise RuntimeError(f"expected HTTP {expected_status} {expected_code}; got {code} {actual_code}")
    serialized = json.dumps(payload, ensure_ascii=False)
    if any(secret and secret in serialized for secret in secrets):
        raise RuntimeError("denied response disclosed sensitive information")
    return f"{code} {actual_code}"


def check_cli(item_id: str, token: str, expected: dict) -> None:
    command = [str(ROOT / "scripts/qual-pi.sh"), "cli", "show", "-project", "QUAL", "-item", item_id]
    # Poison the inherited environment with a forged token. The trusted
    # wrapper MUST ignore it and read the private PI Agent PAT instead.
    environment = dict(os.environ)
    environment["QUAL_TOKEN"] = FAKE_TOKEN
    environment["QUAL_API_URL"] = "http://example.invalid"
    result = subprocess.run(
        command,
        cwd=ROOT,
        env=environment,
        capture_output=True,
        text=True,
        timeout=18,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError("scoped PI CLI read failed")
    try:
        item = json.loads(result.stdout)
    except json.JSONDecodeError:
        raise RuntimeError("PI CLI output is not valid JSON") from None
    for field in ("key", "id", "project_id", "version", "status"):
        if item.get(field) != expected.get(field):
            raise RuntimeError(f"PI CLI and REST disagree on {field}")
    if token in result.stdout or token in result.stderr:
        raise RuntimeError("PI CLI printed the private Agent credential")


def choose_review_candidate(items: object) -> dict:
    """Choose an actual pending item; version >1 permits a safe stale CAS."""
    if not isinstance(items, list):
        raise RuntimeError("WorkItem listing is not a list")
    candidates = [
        item for item in items
        if isinstance(item, dict)
        and item.get("status") == "in_review"
        and isinstance(item.get("version"), int)
        and item["version"] > 1
        and isinstance(item.get("id"), str)
        and isinstance(item.get("key"), str)
    ]
    if not candidates:
        raise RuntimeError("no real in_review WorkItem available for safe Agent denial probe")
    return sorted(candidates, key=lambda item: item["key"])[0]


def run() -> dict:
    if (PRIVATE / "qual-self-owner.token").exists():
        raise RuntimeError("temporary Human Owner bootstrap PAT must not remain")
    token = read_private(TOKEN_PATH)
    if not PAT.fullmatch(token):
        raise RuntimeError("PI Agent PAT file is not a valid restricted credential")
    seed = json.loads(read_private(SEED_STATE, 200_000))
    pid = str(uuid.UUID(seed["project_id"]))
    if seed.get("project_key") != "QUAL":
        raise RuntimeError("seed does not describe QUAL")
    tasks = {task["slug"]: task for task in seed["tasks"]}
    if "mcp-cli-smoke" not in tasks:
        raise RuntimeError("expected security task is missing")
    qual2_id = str(uuid.UUID(tasks["mcp-cli-smoke"]["id"]))
    base = f"/api/v1/projects/{pid}"

    status, access = api(base + "/access/me", token)
    if status != 200:
        raise RuntimeError("Agent project access inspection failed")
    rights = inspect_agent_access(access, pid)

    status, projects = api("/api/v1/projects", token)
    items = projects.get("items", [])
    if status != 200 or len(items) != 1 or items[0].get("id") != pid:
        raise RuntimeError("PI Agent must only list QUAL")

    status, work = api(base + "/work-items/" + qual2_id, token)
    if status != 200 or work.get("key") != "QUAL-2":
        raise RuntimeError("QUAL-2 not readable by scoped Agent")
    version = work.get("version")
    if not isinstance(version, int) or version < 2:
        raise RuntimeError("unexpected optimistic version")
    check_cli(qual2_id, token, work)

    sensitive = (
        token,
        FAKE_TOKEN,
        work.get("title", ""),
        tasks["mcp-cli-smoke"]["id"],
    )
    results = {
        "cli_rest_same_work_item_and_version": True,
        "anonymous_read": ensure_denied(api(base + "/work-items/" + qual2_id), 401, "UNAUTHENTICATED", sensitive),
        "forged_pat_read": ensure_denied(api(base + "/work-items/" + qual2_id, FAKE_TOKEN), 401, "UNAUTHENTICATED", sensitive),
        "unknown_project_read": ensure_denied(
            api(f"/api/v1/projects/{UNKNOWN_PROJECT}/work-items/{qual2_id}", token),
            404, "NOT_FOUND", sensitive,
        ),
        "unknown_work_item": ensure_denied(
            api(base + "/work-items/" + str(uuid.uuid4()), token),
            404, "NOT_FOUND", sensitive,
        ),
    }

    for name, route in {
        "archive_read": "exports",
        "webhook_read": "webhooks",
        "delivery_history": "webhook-deliveries",
    }.items():
        results[name] = ensure_denied(api(base + "/" + route, token), 403, "PERMISSION_DENIED", sensitive)

    if rights["audit_read_effective"]:
        audit_status, _ = api(base + "/audit", token)
        if audit_status != 200:
            raise RuntimeError("granted audit:read did not authorize live audit GET")
        results["audit_read"] = "200 authorized (contents omitted)"
    else:
        results["audit_read"] = ensure_denied(
            api(base + "/audit", token), 403, "PERMISSION_DENIED", sensitive
        )

    results["archive_create"] = ensure_denied(
        api(
            base + "/exports",
            token,
            method="POST",
            body={},
            idempotency_key="phase7-qual2-forbidden-export-20261031",
        ),
        403,
        "PERMISSION_DENIED",
        sensitive,
    )

    # Never issue a synthetic Done request using a legitimately empowered
    # work:review token: the legacy "must return 403" assumption is obsolete,
    # and a real WorkItem must not serve as a destructive test fixture.
    if rights["work_review_effective"]:
        results["review_transition_probe"] = {
            "performed": False, "reason": "Agent has delegated work:review; use isolated PG negative tests"
        }
    else:
        status, review_page = api(base + "/work-items?limit=100", token)
        if status != 200:
            raise RuntimeError("current review candidate list not readable")
        try:
            candidate = choose_review_candidate(review_page.get("items"))
        except RuntimeError:
            # Review queue may be empty; do not create a fake in_review item.
            results["review_transition_probe"] = {
                "performed": False, "reason": "no real in_review target (no test fixture created)"
            }
        else:
            review_id = str(uuid.UUID(candidate["id"]))
            status, review_item = api(base + "/work-items/" + review_id, token)
            if status != 200 or review_item.get("status") != "in_review":
                raise RuntimeError("review candidate changed; refusing live transition probe")
            review_version = review_item.get("version")
            if not isinstance(review_version, int) or review_version <= 1:
                raise RuntimeError("review candidate lacks safe stale CAS version")
            results["agent_done_denied"] = ensure_denied(
                api(
                    base + "/work-items/" + review_id + "/transitions",
                    token,
                    method="POST",
                    body={"status": "done", "expected_version": review_version - 1},
                    idempotency_key=f"phase7-qual2-review-denied-{review_id[:8]}-v{review_version}",
                ),
                403,
                "PERMISSION_DENIED",
                sensitive + (review_item.get("title", ""),),
            )
            status, after_review = api(base + "/work-items/" + review_id, token)
            if (status != 200 or after_review.get("version") != review_version
                    or after_review.get("status") != "in_review"):
                raise RuntimeError("forbidden Agent Review attempt changed live WorkItem")
            results["review_transition_probe"] = {
                "performed": True, "key": review_item.get("key"), "version": review_version,
                "unchanged": True
            }

    results["stale_write_denied"] = ensure_denied(
        api(
            base + "/work-items/" + qual2_id,
            token,
            method="PATCH",
            body={"description": "This stale-write probe MUST NEVER be persisted."},
            idempotency_key="phase7-qual2-stale-version-denied-20261031",
            if_match=version - 1,
        ),
        409,
        "VERSION_CONFLICT",
        sensitive,
    )
    status, current = api(base + "/work-items/" + qual2_id, token)
    if status != 200 or current.get("version") != version or current.get("description") != work.get("description"):
        raise RuntimeError("stale optimistic-version write mutated QUAL-2")

    return {
        "project": "QUAL",
        "tested_task": "QUAL-2",
        "observed_version": version,
        "checks": results,
        "review_effective": rights["work_review_effective"],
        "audit_effective": rights["audit_read_effective"],
        "result": "PASS",
        "note": "conditional live review probe reported explicitly (may be skipped); unknown-project 404 is not a live foreign-membership test; exercised writes were rejected and Qual runs no tests/Agents/CI",
    }


def main() -> int:
    try:
        print(json.dumps(run(), indent=2, ensure_ascii=False))
        return 0
    except Exception as exc:
        # Probe errors use normalized categories and never log credentials or
        # transport headers. Don't include HTTP response bodies on failure.
        print(f"phase7-security-smoke: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
