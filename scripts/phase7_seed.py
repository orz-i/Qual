#!/usr/bin/env python3
"""Idempotently seed real Qual dogfood work/test *definitions* through REST.

No direct DB writes, no fabricated Attempts, no PI execution. All responses
are sanitized to nonsecret IDs/keys and never include authentication headers.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import sys
import uuid
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

REPO = Path(__file__).resolve().parent.parent
SEED = REPO / "docs/pilot/phase7-seed.json"
DEFAULT_OWNER = Path.home() / ".config/qual/qual-self-owner.token"
PAT = re.compile(r"^qual_pat_[a-f0-9]{64}$")


def owner_token(path: Path) -> str:
    if path.is_symlink() or not path.is_file():
        raise ValueError("owner PAT must be a regular, private file")
    st = path.stat()
    if st.st_uid != os.getuid() or st.st_mode & 0o077:
        raise ValueError("owner PAT permissions too broad")
    value = path.read_text(encoding="ascii").strip()
    if not PAT.fullmatch(value):
        raise ValueError("invalid owner PAT format")
    return value


def request(base: str, token: str, method: str, path: str, body: dict | None, idem: str = "") -> dict:
    data = json.dumps(body, ensure_ascii=False, separators=(",", ":")).encode() if body is not None else None
    headers = {"Authorization": "Bearer " + token, "Accept": "application/json"}
    if body is not None:
        headers["Content-Type"] = "application/json"
    if idem:
        headers["Idempotency-Key"] = idem
    req = Request(base + path, data=data, method=method, headers=headers)
    try:
        with urlopen(req, timeout=12) as res:
            if res.status not in (200, 201):
                raise RuntimeError(f"unexpected status {res.status} on {path}")
            result = json.load(res)
            if not isinstance(result, dict):
                raise RuntimeError(f"invalid service response for {path}")
            return result
    except HTTPError as ex:
        code = "HTTP_ERROR"
        try:
            error = json.loads(ex.read(4096)).get("error", {})
            if isinstance(error, dict):
                code = str(error.get("code", code))
        except Exception:
            pass
        raise RuntimeError(f"Qual {method} {path}: HTTP {ex.code} {code}") from None
    except URLError:
        raise RuntimeError("Qual local REST endpoint unavailable") from None


def seed(project_id: str, token_file: Path, base: str = "http://127.0.0.1:18080") -> dict:
    if base != "http://127.0.0.1:18080":
        raise ValueError("Phase 7 seed is restricted to the default loopback Qual service")
    pid = str(uuid.UUID(project_id))
    token = owner_token(token_file)
    doc = json.loads(SEED.read_text(encoding="utf-8"))
    if doc["project_key"] != "QUAL" or doc["workspace_slug"] != "qual-self" or doc["schema_version"] != 1:
        raise ValueError("unexpected seed schema")
    prefix = f"/api/v1/projects/{pid}"
    tasks: dict[str, dict] = {}
    for item in doc["tasks"]:
        slug = item["slug"]
        draft = {
            "kind": "task",
            "title": item["title"],
            "description": item["description"],
            "priority": item["priority"],
            "acceptance_criteria": item["acceptance_criteria"],
        }
        result = request(base, token, "POST", prefix + "/work-items", draft, "qual-dogfood-task-" + slug + "-v1")
        work_id = str(uuid.UUID(result["id"]))
        current = request(base, token, "GET", prefix + "/work-items/" + work_id, None)
        if current.get("key") != result.get("key") or current.get("kind") != "task":
            raise RuntimeError("seed WorkItem verification failed")
        tasks[slug] = {"id": work_id, "key": result["key"], "status": current["status"]}
    suite = request(base, token, "POST", prefix + "/test-suites", {"name": doc["suite"]["name"]},
                    "qual-dogfood-suite-integration-v1")
    suite_id = str(uuid.UUID(suite["id"]))
    cases: list[dict] = []
    for case in doc["suite"]["cases"]:
        spec = {
            "title": case["title"],
            "preconditions": case["preconditions"],
            "steps": case["steps"],
            "expected_result": case["expected_result"],
        }
        created = request(base, token, "POST", prefix + "/test-cases",
                          {"suite_id": suite_id, "specification": spec},
                          "qual-dogfood-case-" + case["slug"] + "-v1")
        case_id = str(uuid.UUID(created["id"]))
        published = request(base, token, "POST", prefix + "/test-cases/" + case_id + "/versions",
                            {"expected_revision": 1},
                            "qual-dogfood-version-" + case["slug"] + "-v1")
        linked = request(base, token, "POST", prefix + "/test-cases/" + case_id + "/coverage",
                         {"work_item_id": tasks[case["task_slug"]]["id"]},
                         "qual-dogfood-coverage-" + case["slug"] + "-v1")
        covered = request(base, token, "GET", prefix + "/test-cases/" + case_id + "/coverage", None)
        coverage_matches = any(row.get("key") == tasks[case["task_slug"]]["key"] for row in covered.get("items", []))
        if published.get("version") != 1 or linked.get("id") != case_id or not coverage_matches:
            raise RuntimeError("published/linked Case verification failed")
        cases.append({"key": created["key"], "id": case_id,
                      "version": published["version"], "coverage_key": tasks[case["task_slug"]]["key"]})
    return {
        "workspace": "qual-self", "project_key": "QUAL", "project_id": pid,
        "tasks": [{"slug": item["slug"], **tasks[item["slug"]]} for item in doc["tasks"]],
        "test_suite_id": suite_id, "test_cases": cases,
        "recorded_attempts": 0,
        "pi_registration": "user_action_pending",
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Seed idempotent QUAL pilot facts via local REST")
    parser.add_argument("--project-id", required=True)
    parser.add_argument("--owner-token-file", type=Path, default=DEFAULT_OWNER)
    parser.add_argument("--output", type=Path, help="write sanitized result JSON to an existing private directory")
    args = parser.parse_args()
    try:
        result = seed(args.project_id, args.owner_token_file)
        text = json.dumps(result, ensure_ascii=False, indent=2) + "\n"
        if args.output:
            folder = args.output.parent
            if folder.is_symlink() or (not folder.is_dir()) or folder.stat().st_mode & 0o077:
                raise ValueError("output directory must be owner-only")
            fd = os.open(str(args.output), os.O_CREAT | os.O_WRONLY | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, "w", encoding="utf-8") as file:
                file.write(text)
            print(f"QUAL project seeded: {len(result['tasks'])} tasks, {len(result['test_cases'])} published cases; no Attempts created.")
        else:
            print(text, end="")
        return 0
    except Exception as err:
        # Never display URL with auth or raw payload. Errors above are normalized.
        print(f"qual-seed: {err}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
