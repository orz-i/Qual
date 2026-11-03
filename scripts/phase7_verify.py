#!/usr/bin/env python3
"""Read-only Phase 7 self-dogfood smoke checks with Agent-scoped PAT."""
import json
import os
import re
import stat
import sys
import uuid
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

BASE = "http://127.0.0.1:18080"
PRIVATE = Path.home() / ".config/qual"
SEED_STATE = PRIVATE / "phase7-seed-state.json"
TOKEN_PATH = PRIVATE / "qual-pi-agent.token"
PAT = re.compile(r"^qual_pat_[a-f0-9]{64}$")
# The seven baseline capabilities remain necessary for this PI dogfood host.
# work:review and audit:read can be explicitly delegated later; neither
# grants access:manage or unrelated Archive/Webhook administration rights.
BASELINE_SCOPES = frozenset((
    "work:read", "work:write", "work:claim", "test:read", "test:write",
    "artifact:read", "artifact:write",
))
FORBIDDEN_EFFECTIVE = frozenset(("access:manage", "archive:export", "webhook:manage"))


def inspect_agent_access(access: dict, project_id: str) -> dict:
    """Validate server-issued credential and effective Project permissions.

    Do not infer permission from the Principal's type, a historic ACL or
    self-review. This check only verifies facts; it grants no permissions.
    """
    scopes = access.get("credential_scopes")
    effective = access.get("effective_permissions")
    if access.get("kind") != "agent" or access.get("project_id") != project_id:
        raise RuntimeError("unexpected Agent identity or Project")
    try:
        if str(uuid.UUID(access.get("principal_id", ""))) != access["principal_id"]:
            raise ValueError("not canonical")
    except (ValueError, TypeError, AttributeError):
        raise RuntimeError("Agent Principal UUID missing or invalid") from None
    if (not isinstance(scopes, list) or not isinstance(effective, list)
            or not all(isinstance(v, str) for v in scopes + effective)):
        raise RuntimeError("invalid Agent credential/effective permission response")
    cred, granted = set(scopes), set(effective)
    if len(cred) != len(scopes) or len(granted) != len(effective):
        raise RuntimeError("duplicate credential/effective permission entries")
    if not BASELINE_SCOPES <= cred or not BASELINE_SCOPES <= granted:
        raise RuntimeError("Agent missing one or more required baseline capabilities")
    if not granted <= cred:
        raise RuntimeError("effective Project permissions exceed Credential Scope")
    if (cred | granted) & FORBIDDEN_EFFECTIVE:
        raise RuntimeError("Agent has an unexpected privileged Credential Scope or Project capability")
    return {
        "work_review_scope": "work:review" in cred,
        "work_review_effective": "work:review" in granted,
        "audit_read_scope": "audit:read" in cred,
        "audit_read_effective": "audit:read" in granted,
        "effective_scopes": sorted(granted),
    }


def read_private(path: Path, max_size: int = 32_768) -> str:
    # A private file in a public or redirected parent directory is not an
    # acceptable credential handoff; refuse a symlink in the final directory.
    directory = path.parent
    parent = directory.lstat()
    if not stat.S_ISDIR(parent.st_mode) or parent.st_uid != os.getuid() or \
            stat.S_IMODE(parent.st_mode) != 0o700:
        raise ValueError("Phase 7 private directory must be owner-only 0700 and not a symlink")
    if not path.is_file() or path.is_symlink():
        raise ValueError("required private Phase 7 file missing")
    info = path.stat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077 or info.st_size > max_size:
        raise ValueError("private Phase 7 file has insecure permissions or size")
    return path.read_text(encoding="utf-8").strip()


def get(path: str, token: str = "") -> tuple[int, dict]:
    req = Request(BASE + path, headers={"Authorization": "Bearer " + token} if token else {})
    try:
        with urlopen(req, timeout=10) as response:
            data = json.load(response)
            return response.status, data if isinstance(data, dict) else {}
    except HTTPError as ex:
        return ex.code, {}
    except URLError:
        raise RuntimeError("local Qual service unavailable") from None


def main() -> int:
    try:
        if (PRIVATE / "qual-self-owner.token").exists():
            raise ValueError("temporary Owner bootstrap PAT must be revoked and removed")
        token = read_private(TOKEN_PATH)
        if not PAT.fullmatch(token):
            raise ValueError("Agent credential format is invalid")
        state = json.loads(read_private(SEED_STATE, 200_000))
        pid = str(uuid.UUID(state["project_id"]))
        if state["project_key"] != "QUAL" or len(state["tasks"]) != 6 or len(state["test_cases"]) != 3:
            raise ValueError("seed metadata does not match Phase 7")
        code, ready = get("/readyz")
        if code != 200 or ready.get("status") != "ready":
            raise RuntimeError("Qual service or storage not ready")

        # A syntactically valid but unissued PAT must not authenticate. Never
        # print either credential or the Authorization header.
        invalid_pat = "qual_pat_" + "0" * 64
        if invalid_pat == token:
            invalid_pat = "qual_pat_" + "f" * 64
        code, _ = get("/api/v1/projects", invalid_pat)
        if code != 401:
            raise RuntimeError(f"unissued Agent PAT must return 401, received {code}")

        code, access = get(f"/api/v1/projects/{pid}/access/me", token)
        if code != 200:
            raise RuntimeError("Agent access inspection failed")
        permissions = inspect_agent_access(access, pid)
        if permissions["work_review_effective"]:
            # A legitimate Human-delegated review role is no longer forbidden.
            # Require an actual, readable Goose13 governance policy instead.
            code, review_policy = get(f"/api/v1/projects/{pid}/review-policy", token)
            if (code != 200 or review_policy.get("project_id") != pid
                    or review_policy.get("source") != "project"
                    or review_policy.get("default_mode") not in (
                        "independent_review", "owner_acceptance")
                    or not isinstance(review_policy.get("version"), int)
                    or review_policy["version"] < 1):
                raise RuntimeError("Agent Review delegation lacks a valid project review policy")
            permissions["project_review_mode"] = review_policy["default_mode"]
            permissions["owner_acceptance_allowed"] = review_policy.get("owner_acceptance_allowed") is True

        code, projects = get("/api/v1/projects", token)
        items = projects.get("items", [])
        if code != 200 or len(items) != 1 or items[0].get("id") != pid or items[0].get("key") != "QUAL":
            raise RuntimeError("Agent must see exactly one authorized QUAL project")

        code, work = get(f"/api/v1/projects/{pid}/work-items?limit=100", token)
        if code != 200:
            raise RuntimeError("Agent WorkItem read failed")
        existing = {row.get("key"): row for row in work.get("items", [])}
        for expected in state["tasks"]:
            if expected["key"] not in existing or existing[expected["key"]].get("id") != expected["id"]:
                raise RuntimeError("seed WorkItem missing or inaccessible")

        code, cases = get(f"/api/v1/projects/{pid}/test-cases", token)
        if code != 200:
            raise RuntimeError("Agent test cases read failed")
        indexed = {row.get("key"): row for row in cases.get("items", [])}
        for expected in state["test_cases"]:
            found = indexed.get(expected["key"])
            if found is None or found.get("id") != expected["id"] or found.get("published_version", 0) < 1:
                raise RuntimeError("test case version missing")
            code, coverage = get(f"/api/v1/projects/{pid}/test-cases/{expected['id']}/coverage", token)
            if code != 200 or not any(row.get("key") == expected["coverage_key"] for row in coverage.get("items", [])):
                raise RuntimeError("test case coverage missing")

        denied = []
        for route in ("exports", "webhooks", "webhook-deliveries"):
            code, _ = get(f"/api/v1/projects/{pid}/{route}", token)
            if code != 403:
                raise RuntimeError(f"Agent privilege bypass or unexpected API status on {route}: {code}")
            denied.append(route)
        # A later audit:read delegation is permitted only when the server
        # confirms it with both credential Scope and Project authorization.
        code, _ = get(f"/api/v1/projects/{pid}/audit", token)
        expected_audit = 200 if permissions["audit_read_effective"] else 403
        if code != expected_audit:
            raise RuntimeError(f"Agent Audit response disagrees with effective permission ({code})")
        if code == 403:
            denied.append("audit")

        code, runs = get(f"/api/v1/projects/{pid}/test-runs", token)
        if code != 200:
            raise RuntimeError("TestRun read failed")
        # Never infer that an Attempt was executed merely from a published Case.
        print(json.dumps({
            "workspace": "qual-self",
            "project_key": "QUAL",
            "project_id": pid,
            "seeded_tasks_verified": len(state["tasks"]),
            "published_cases_verified": len(state["test_cases"]),
            "current_run_count": len(runs.get("items", [])),
            "agent_privileges_denied": denied,
            "agent_access": permissions,
            "unissued_pat_rejected": True,
            "access_manage_effective": False,
            "pi_registration": "not checked (user action)",
            "result": "PASS",
        }, indent=2))
        return 0
    except Exception as ex:
        print(f"qual-phase7-check: {ex}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
