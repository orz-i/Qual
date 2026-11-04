#!/usr/bin/env python3
"""Read-only quality gate for public GitHub PR descriptions.

No GITHUB_TOKEN, network, repository secrets, or PR mutations. This checks
structure and honest evidence boundaries, NOT the truth of claims or approvals.
"""
from __future__ import annotations

import json
from pathlib import Path
import re
import sys

REQUIRED = (
    "why",
    "changes",
    "evidence and how to verify",
    "security & deployment boundaries",
    "review requested",
)

def validate(title: str, body: str) -> list[str]:
    problems: list[str] = []
    if len(title.strip()) < 18 or len(title.strip()) > 160 or "\n" in title:
        problems.append("PR title must be descriptive, single-line and 18-160 characters")
    if len(body.strip()) < 500:
        problems.append("PR description is too short to explain changes, tests and risks")
    if body.count("\n") < 12 or r"\n##" in body or r"\n-" in body:
        problems.append("Use real Markdown line breaks, not literal backslash-n sequences")
    present = {
        m.group(1).strip().lower()
        for m in re.finditer(r"^##\s+(.+?)\s*$", body, re.MULTILINE)
    }
    missing = [section for section in REQUIRED if section not in present]
    if missing:
        problems.append("Missing PR sections: " + ", ".join(missing))
    if len(re.findall(r"(?m)^\s*- \[[ xX]\]\s+\S+", body)) < 3:
        problems.append("PR must include an explicit security/rollout checklist")
    if not re.search(r"(?i)\b(?:test|ci|race|vet|typecheck|验证|测试)\b", body):
        problems.append("Include concrete verification and test evidence")
    if not re.search(r"https://github\.com/[^\s)]+/actions/runs/\d+", body):
        problems.append("Link an actual GitHub Actions run, or explain missing CI evidence")
    return problems


def main() -> int:
    if len(sys.argv) != 2:
        print("Usage: check_pr_description.py <GitHub event JSON>", file=sys.stderr)
        return 2
    try:
        event = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
        pr = event["pull_request"]
        title = pr["title"]
        body = pr.get("body") or ""
        if not isinstance(title, str) or not isinstance(body, str):
            raise ValueError("invalid PR title/body types")
    except (KeyError, TypeError, ValueError, OSError, json.JSONDecodeError):
        print("PR_DESCRIPTION_QUALITY_FAIL: invalid pull_request event", file=sys.stderr)
        return 2
    problems = validate(title, body)
    if problems:
        for problem in problems:
            print("PR_DESCRIPTION_QUALITY_FAIL: " + problem, file=sys.stderr)
        return 1
    print("PR_DESCRIPTION_QUALITY_PASS: real Markdown sections, review scope and linked evidence")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
