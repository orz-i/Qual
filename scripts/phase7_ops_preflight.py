#!/usr/bin/env python3
"""QUAL-6 read-only loopback operational preflight for the existing PI Agent.

Does not run Operator-only backup/recovery, fetch the Operator secret, change
permissions, submit user feedback, or mutate any Qual WorkItem/TestRun.
The result is deliberately Pilot Pending even when the preflight passes.
"""
from __future__ import annotations

import json
import sys
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import HTTPRedirectHandler, Request, build_opener

from phase7_verify import BASE, PAT, SEED_STATE, TOKEN_PATH, read_private

PATHS = frozenset({"/healthz", "/readyz", "/internal/metrics"})
FEEDBACK_TEMPLATE = Path(__file__).resolve().parents[1] / "docs/pilot/feedback-template.md"


class RefuseRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def fetch(path: str, bearer: str = "") -> tuple[int, bytes, str]:
    if path not in PATHS:
        raise ValueError("operational preflight only permits exact loopback health and metrics routes")
    headers = {"Accept": "application/json"}
    if bearer:
        headers["Authorization"] = "Bearer " + bearer
    request = Request(BASE + path, headers=headers, method="GET")
    try:
        with build_opener(RefuseRedirects).open(request, timeout=6) as response:
            return response.status, response.read(4096), response.headers.get("Cache-Control", "")
    except HTTPError as error:
        # Never expose raw error bodies or headers. Do not follow redirects.
        return error.code, error.read(4096), error.headers.get("Cache-Control", "")
    except URLError:
        raise RuntimeError("Qual local loopback service is unavailable") from None


def validate_health(path: str, result: tuple[int, bytes, str]) -> str:
    code, body, cache_policy = result
    if code != 200 or cache_policy != "no-store":
        raise RuntimeError(f"{path} readiness/liveness or no-store policy failed")
    try:
        data = json.loads(body)
    except (UnicodeDecodeError, ValueError):
        raise RuntimeError(f"{path} did not return valid JSON") from None
    expected = "ok" if path == "/healthz" else "ready"
    if data != {"status": expected}:
        raise RuntimeError(f"{path} returned unexpected health state")
    return expected


def validate_metrics(
    results: dict[str, tuple[int, bytes, str]], token: str, invalid_bearer: str
) -> dict:
    if set(results) != {"anonymous", "agent_pat", "bogus_token"}:
        raise RuntimeError("missing exact metrics-negative probes")
    observed = {}
    for label, (code, body, cache_policy) in results.items():
        # When this endpoint is configured it must return 401 to every non-
        # Operator caller. When disabled it is deliberately not routed (404).
        if code not in (401, 404) or cache_policy != "no-store":
            raise RuntimeError(f"metrics was unexpectedly accessible or cacheable for {label}")
        if token.encode() in body or invalid_bearer.encode() in body:
            raise RuntimeError("metrics rejection echoed an authentication value")
        observed[label] = code
    if len(set(observed.values())) != 1:
        raise RuntimeError("inconsistent metrics registration/auth behavior across unauthorized requests")
    mode = ("operator_token_not_configured_or_route_absent"
            if observed["anonymous"] == 404 else "operator_token_required_for_access")
    return {
        "mode": mode,
        "denied_statuses": observed,
        "configured_operator_access_verified": False,
        "ordinary_agent_metrics_access_denied": True,
    }


def feedback_template_ready(text: str) -> bool:
    # A present template is not evidence a real Human submitted feedback.
    return all(field in text for field in (
        "实际完成情况", "可复现步骤", "严重程度", "试点者是否确认该反馈为真实操作记录",
    ))


def run() -> dict:
    token = read_private(TOKEN_PATH)
    if not PAT.fullmatch(token):
        raise RuntimeError("current PI Agent credential format invalid")
    if (TOKEN_PATH.parent / "qual-self-owner.token").exists():
        raise RuntimeError("temporary bootstrap Owner token still present")
    metadata = json.loads(read_private(SEED_STATE, 200_000))
    if metadata.get("project_key") != "QUAL":
        raise RuntimeError("PI pilot metadata is not QUAL")
    health = validate_health("/healthz", fetch("/healthz"))
    ready = validate_health("/readyz", fetch("/readyz"))

    bogus = "invalid-local-operator-probe"
    if bogus == token:
        raise RuntimeError("invalid metrics bearer unexpectedly matches PI PAT")
    metrics = validate_metrics({
        "anonymous": fetch("/internal/metrics"),
        "agent_pat": fetch("/internal/metrics", token),
        "bogus_token": fetch("/internal/metrics", bogus),
    }, token, bogus)
    try:
        template = FEEDBACK_TEMPLATE.read_text(encoding="utf-8")
    except OSError:
        raise RuntimeError("real participant feedback template unavailable") from None
    if not feedback_template_ready(template):
        raise RuntimeError("feedback template lacks required genuine participant fields")
    return {
        "project_key": "QUAL",
        "scope": "actual loopback read-only PI Agent operations preflight",
        "liveness": {"status_code": 200, "status": health, "cache_control": "no-store"},
        "readiness": {"status_code": 200, "status": ready, "cache_control": "no-store"},
        "operator_metrics": metrics,
        "feedback_template_fields_available": True,
        "real_participant_feedback_collected": False,
        "pg_s3_backup_restore_executed": False,
        "operator_token_presented": False,
        "production_readiness_verified": False,
        "pilot_status": "Pending",
        "limitations": [
            "readyz is an application readiness signal; this probe cannot prove whether S3 was configured",
            "a 404 metrics response means route disabled/unregistered, not a verified configured Operator credential",
            "an actual backup and restore rehearsal requires an authorized Human Operator and a maintenance window",
            "no authentic Human or external participant feedback was collected",
            "no production OIDC, TLS, remote MCP OAuth, disaster failover or independent Human signoff",
        ],
        "result": "PASS (limited preflight only)",
    }


def main() -> int:
    try:
        print(json.dumps(run(), indent=2, ensure_ascii=False))
        return 0
    except Exception as exc:
        # Only raise program-authored error strings; never log HTTP bodies or
        # exceptions with request headers (which could include bearer tokens).
        if isinstance(exc, (RuntimeError, ValueError)):
            print("qual6-ops-preflight: " + str(exc), file=sys.stderr)
        else:
            print("qual6-ops-preflight: unexpected local verification failure", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
