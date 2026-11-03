#!/usr/bin/env python3
"""Read-only QUAL-30 Goose17 release preflight; never an Operator cutover.

This PI-side script does NOT read the runtime config, HMAC, PAT, backups, S3
secrets, or launchd plist; it does not stop a service or touch database DDL.
Any real Goose16->17 migration must be authorized and run separately by the
trusted Human Operator during a maintenance window.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
from urllib.error import HTTPError
from urllib.request import urlopen

SOURCE_ROOT = Path(__file__).resolve().parents[2]
LIVE_ROOT = Path("/Users/mini/Coding/Qual")
CANDIDATE_SOURCE = "edcf2d391dfbb76cf7c8258abd95627a53909684"
REVIEWED_MAIN = "84342276de7fc025aeb18516fa5d4c0c552acdc1"
CANDIDATE_SERVER_SHA = "acc0060d1f7bd16538e6d94e026560c8af8eb741ef9868972139454b5ddbd710"
CANDIDATE_OPS_SHA = "506f462d2d1affa57221200e01021342730b2ad85e7b845980058b91b246e913"
ACTIVE_GOOSE16_SHA = "9e1ad81d23b62aff01dca1c935de7f09391051c40a9de65f8fe8d61d3be48a6e"
GOOSE_CLI_SHA = "f7ac5a67387a9f2c7aa6a990647b674c0bf77cf0d5c988e62cc087b34e7302a3"
MIGRATION17_SHA = "57fc67aaf2751adf5f0e8f4d8410bb4a14080f95294184e4bbe8da141c1cd50f"
ENGINEERING_LOGS_SHA = "f3c5575508e3647ed6420508bc0f06aca4905c78458227a0a9fc32fadef27374"
CANDIDATE_ROOT = LIVE_ROOT / "tmp/worktrees/qual30-goose17-release"
SERVER = CANDIDATE_ROOT / "tmp/goose17-release/qual-server"
OPS = CANDIDATE_ROOT / "tmp/goose17-release/qual-ops"
LIVE_BINARY = Path.home() / ".local/state/qual/qual49-goose16-hotfix/qual-server"
GOOSE = LIVE_ROOT / "tmp/qual-governance/bin/goose"
RECEIPT = LIVE_ROOT / "tmp/worktrees/qual28-30-goose17/tmp/integrated17/qualification-receipt.json"
MIGRATION = CANDIDATE_ROOT / "apps/server/migrations/00017_external_events.sql"


class PreflightGuard(Exception):
    pass


def sha(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def verified_file(path: Path, digest: str):
    if path.is_symlink() or not path.is_file():
        raise PreflightGuard("reviewed file missing or symlinked: " + path.name)
    if path.stat().st_uid != os.getuid():
        raise PreflightGuard("reviewed file owner mismatch: " + path.name)
    if sha(path) != digest:
        raise PreflightGuard("reviewed SHA mismatch: " + path.name)


def shell(*argv: str, timeout=12) -> str:
    result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        # Do not print arbitrary stderr/stdout; config and auth may be sensitive.
        raise PreflightGuard("read-only command rejected: " + Path(argv[0]).name)
    return result.stdout.strip()


def verified_source():
    current = shell("git", "-C", str(SOURCE_ROOT), "rev-parse", "HEAD")
    for ancestor in (CANDIDATE_SOURCE, REVIEWED_MAIN):
        if subprocess.run(["git", "-C", str(SOURCE_ROOT),
                           "merge-base", "--is-ancestor", ancestor, current],
                          capture_output=True, timeout=8).returncode:
            raise PreflightGuard("candidate lacks reviewed source/security lineage")
    diff = shell("git", "-C", str(SOURCE_ROOT), "diff", "--name-only",
                 CANDIDATE_SOURCE, current, "--", "apps/", "packages/", "integrations/")
    if diff:
        raise PreflightGuard("Go Server or client app sources changed after qualification")
    if shell("git", "-C", str(SOURCE_ROOT), "status", "--porcelain",
             "--untracked-files=no"):
        raise PreflightGuard("candidate tracked files are dirty")
    main = shell("git", "-C", str(LIVE_ROOT), "rev-parse", "HEAD")
    if subprocess.run(["git", "-C", str(LIVE_ROOT), "merge-base",
                       "--is-ancestor", REVIEWED_MAIN, main],
                      capture_output=True, timeout=8).returncode:
        raise PreflightGuard("live main is not descendant of reviewed Goose16 baseline")
    promoted = subprocess.run(["git", "-C", str(LIVE_ROOT), "merge-base",
                               "--is-ancestor", CANDIDATE_SOURCE, main],
                              capture_output=True, timeout=8).returncode == 0
    # Before main promotion the current Goose16 application must not drift;
    # AFTER promotion exactly the reviewed Goose17 app/SDK may differ.
    permitted_base = CANDIDATE_SOURCE if promoted else REVIEWED_MAIN
    live_diff = shell("git", "-C", str(LIVE_ROOT), "diff", "--name-only",
                      permitted_base, main, "--", "apps/", "packages/", "integrations/")
    if live_diff:
        raise PreflightGuard("live main application/contract changed; requalify")
    if shell("git", "-C", str(LIVE_ROOT), "status", "--porcelain", "--untracked-files=no"):
        raise PreflightGuard("live main has uncommitted tracked edits")
    return current, main


def version_and_objects():
    if not (LIVE_ROOT / "deploy/compose.dev.yaml").is_file():
        raise PreflightGuard("trusted local Compose stack missing")
    cmd = ["/usr/local/bin/docker", "exec", "qual-dev-postgres-1",
           "psql", "-U", "qual", "-d", "qual", "-At", "-v", "ON_ERROR_STOP=1"]
    query = ("SELECT COALESCE(MAX(version_id),0) "
             "FROM public.goose_db_version WHERE is_applied")
    raw = shell(*(cmd + ["-c", query]), timeout=15)
    if raw != "16":
        raise PreflightGuard("default database not exactly Goose16")
    objects = ("SELECT to_regclass('qual.external_events') IS NULL AND "
               "to_regclass('qual.agent_runs') IS NOT NULL AND "
               "to_regclass('qual.agent_run_reports') IS NOT NULL AND "
               "to_regclass('qual.external_threads') IS NOT NULL AND "
               "to_regclass('qual.work_thread_links') IS NOT NULL AND "
               "to_regclass('qual.test_attempts_project_ingested_context_idx') IS NOT NULL")
    if shell(*(cmd + ["-c", objects]), timeout=15) != "t":
        raise PreflightGuard("physical Goose16 objects not ready or Event prematurely applied")
    return 16


def running_server():
    verified_file(LIVE_BINARY, ACTIVE_GOOSE16_SHA)
    pids = shell("/usr/sbin/lsof", "-nP", "-t", "-iTCP:18080", "-sTCP:LISTEN").splitlines()
    if len(pids) != 1 or not pids[0].isdigit():
        raise PreflightGuard("unexpected local listener count")
    pid = pids[0]
    command = shell("/bin/ps", "-p", pid, "-o", "command=")
    if command != str(LIVE_BINARY):
        raise PreflightGuard("active Goose16 process binary not reviewed QUAL49")
    with urlopen("http://127.0.0.1:18080/readyz", timeout=4) as response:
        if response.status != 200:
            raise PreflightGuard("Goose16 server failed readiness")
    try:
        with urlopen("http://127.0.0.1:18080/api/v1/projects/"
                     "e6560273-31f1-4c3f-b690-3b4586977700/external-events",
                     timeout=4) as response:
            code = response.status
    except HTTPError as err:
        code = err.code
    if code != 404:
        raise PreflightGuard("Goose16 unexpectedly exposes Event route")
    return int(pid)


def check_receipt():
    verified_file(RECEIPT, ENGINEERING_LOGS_SHA)
    data = json.loads(RECEIPT.read_text(encoding="utf-8"))
    if (data.get("goose_version_default") != 16 or
        data.get("full_integration_go_race", {}).get("passed") != 49 or
        data.get("full_integration_go_race", {}).get("isolated_fixture_skips") != 21 or
        data.get("separately_rerun_skipped_cases", {}).get("passed") != 21):
        raise PreflightGuard("reviewed integration qualification evidence differs")
    return len(data.get("logs", []))


def inspect():
    source, main = verified_source()
    for filename, digest in ((SERVER, CANDIDATE_SERVER_SHA),
                             (OPS, CANDIDATE_OPS_SHA),
                             (GOOSE, GOOSE_CLI_SHA),
                             (MIGRATION, MIGRATION17_SHA)):
        verified_file(filename, digest)
    logs = check_receipt()
    if logs != 14:
        raise PreflightGuard("qualification log count changed")
    version = version_and_objects()
    pid = running_server()
    return {
        "type": "read_only_preflight",
        "source_commit": source,
        "main_commit": main,
        "real_default_goose": version,
        "live_qual49_pid": pid,
        "ready_http": 200,
        "event_route_status": 404,
        "candidate_server_sha256": CANDIDATE_SERVER_SHA,
        "candidate_ops_sha256": CANDIDATE_OPS_SHA,
        "migration17_sha256": MIGRATION17_SHA,
        "engineering_evidence_verified": True,
        "integrated_log_count": logs,
        "goose17_applied_to_default": False,
        "operator_key_or_runtime_secrets_read": False,
        "operator_approval_obtained": False,
        "public_production_claimed": False,
        "human_operator_apply_supported_in_this_script": False,
    }


def main():
    parser = argparse.ArgumentParser(description="Read-only Goose17 PI release guard")
    parser.add_argument("action", choices=["inspect"])
    args = parser.parse_args()
    try:
        if args.action == "inspect":
            info = inspect()
            print("QUAL30_GOOSE17_READONLY_PREFLIGHT_PASS",
                  "candidate", info["source_commit"][:12],
                  "main", info["main_commit"][:12], "goose", info["real_default_goose"],
                  "pid", info["live_qual49_pid"], "sha", CANDIDATE_SERVER_SHA[:12],
                  "logs", info["integrated_log_count"],
                  "operator_cutover_not_run", True)
    except (PreflightGuard, OSError, ValueError, KeyError, subprocess.TimeoutExpired) as e:
        message = str(e) if isinstance(e, PreflightGuard) else "preflight rejected"
        print("QUAL30_GOOSE17_PREFLIGHT_GUARD", type(e).__name__, message, file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    main()
