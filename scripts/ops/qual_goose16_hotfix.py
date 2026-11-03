#!/usr/bin/env python3
"""QUAL-49: Human Operator-only Goose16 code hotfix for QUAL-43/44.

`inspect` and `preflight` are read-only and do not read Operator secrets.
`apply --apply` requires a trusted Human Operator terminal. It accesses private
runtime config, HMAC backup key, and qual-ops ONLY inside the explicit apply
path; an Agent must not execute it. The binary is replaced without migrations.
No secret value, token, DSN, private object bytes, or HMAC key is printed.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/ops"))
import qual_launchd_server as prior  # noqa: E402
import qual_goose16_rollout as goose16  # noqa: E402

BASE_COMMIT = "df12bcfc11fe862175d81206b8eb115369b3493c"
EXPECTED_OLD_SHA = "c17a1ce5038c9860cb51bd8b5914fead624367e972dae829dcb5af9fbef61572"
EXPECTED_NEW_SHA = "9e1ad81d23b62aff01dca1c935de7f09391051c40a9de65f8fe8d61d3be48a6e"
CANDIDATE = ROOT / "tmp/phase10/qual49-release/qual-server"
RELEASE = prior.STATE_DIR / "qual49-goose16-hotfix"
NEW_BINARY = RELEASE / "qual-server"
ORIGINAL_PLIST = RELEASE / "prior-goose16.plist"
RECEIPT = RELEASE / "live-hotfix-receipt.json"
LABEL = prior.LABEL
JOB = "gui/%d/%s" % (os.getuid(), LABEL)
OLD_BINARY = goose16.SERVER
OLD_RUN = ["/usr/bin/python3", str(ROOT / "scripts/ops/qual_goose16_rollout.py"), "run"]
NEW_RUN = ["/usr/bin/python3", str(ROOT / "scripts/ops/qual_goose16_hotfix.py"), "run"]
TEST_LOGS = {
    "main-postmerge-go-race.log": "4d5a44190351b44f8fffc33dd0e5cd376584be5b391fe4cac478f60a3e00b024",
    "isolated-integration-race.log": "5907ed7dca2fce229bfc0e7cc1d2b0437d509b0b5735edbf35c362774d561ba8",
    "security-negative-fixed.log": "0fb4a2419b864c1eeac1fde56351a1bb1b8f8e73d56168a0e74132e21db115fb",
}


class HotfixGuard(Exception):
    pass


def checked_binary(path: Path, expected: str):
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid():
        raise HotfixGuard("Release binary absent or not owned by current operator")
    if prior.sha(path) != expected:
        raise HotfixGuard("Release binary hash mismatch")
    return path


def source_gate():
    """Only reviewed application/contract sources may enter this release."""
    if not (ROOT / ".git").exists():
        raise HotfixGuard("Missing Qual Git worktree")
    head = goose16.exec_safe(["/usr/bin/git", "-C", str(ROOT), "rev-parse", "HEAD"], timeout=8).strip()
    if subprocess.run(["/usr/bin/git", "-C", str(ROOT), "merge-base", "--is-ancestor", BASE_COMMIT, head],
                      capture_output=True, timeout=8).returncode:
        raise HotfixGuard("Release HEAD is not descended from reviewed source")
    # Operator script changes are permitted; no new Go code or contract change.
    for argv in (["git", "diff", "--quiet", BASE_COMMIT, head, "--", "apps/server", "packages"],
                 ["git", "diff", "--quiet", "HEAD", "--", "apps/server", "packages"],
                 ["git", "diff", "--cached", "--quiet", "--", "apps/server", "packages"]):
        if subprocess.run(["/usr/bin/git", "-C", str(ROOT)] + list(argv[1:]), capture_output=True, timeout=10).returncode:
            raise HotfixGuard("Application or contract changed after reviewed QA")
    return head


def goose16_only():
    if goose16.check_goose_schema() != 16:
        raise HotfixGuard("Default database must remain exactly Goose16")
    statement = ("SELECT to_regclass('qual.external_threads') IS NOT NULL "
                 "AND to_regclass('qual.work_thread_links') IS NOT NULL "
                 "AND to_regclass('qual.agent_runs') IS NOT NULL "
                 "AND to_regclass('qual.agent_run_reports') IS NOT NULL "
                 "AND to_regclass('qual.test_case_coverage_work_context_idx') IS NOT NULL "
                 "AND to_regclass('qual.test_run_items_case_context_idx') IS NOT NULL "
                 "AND to_regclass('qual.test_attempts_run_context_idx') IS NOT NULL "
                 "AND to_regclass('qual.test_attempts_project_ingested_context_idx') IS NOT NULL")
    check = goose16.exec_safe(["/usr/local/bin/docker", "compose", "-f", str(ROOT / "deploy/compose.dev.yaml"),
                               "exec", "-T", "postgres", "psql", "-X", "-A", "-t", "-U", "qual", "-d", "qual", "-c", statement],
                              timeout=18).strip()
    if check != "t":
        raise HotfixGuard("Goose16 physical schema incomplete")


def active_old():
    if not prior.is_managed():
        raise HotfixGuard("Expected managed launchd job is missing")
    pid = prior.listening_pid(18080)
    if not pid or not prior.matches_binary(pid, OLD_BINARY):
        raise HotfixGuard("Listener is not the pinned previous Goose16 binary")
    checked_binary(OLD_BINARY, EXPECTED_OLD_SHA)
    if prior.http_status("http://127.0.0.1:18080/readyz")[0] != 200:
        raise HotfixGuard("Previous managed server is not healthy")
    return pid


def current_plist_bytes():
    path = prior.PLIST
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid():
        raise HotfixGuard("Live LaunchAgent plist is missing or untrusted")
    data = path.read_bytes()
    try:
        obj = plistlib.loads(data)
    except Exception as exc:
        raise HotfixGuard("Invalid managed LaunchAgent plist") from exc
    if obj.get("Label") != LABEL or obj.get("WorkingDirectory") != str(ROOT) or obj.get("ProgramArguments") != OLD_RUN:
        raise HotfixGuard("Managed launchd arguments changed or are not the reviewed release")
    if obj.get("RunAtLoad") is not True or obj.get("Umask") != 63:
        raise HotfixGuard("Managed launcher safety options changed")
    return data


def checked_test_logs():
    directory = ROOT / "tmp/audits/qual4344-security-acceptance"
    for name, digest in TEST_LOGS.items():
        path = directory / name
        if path.is_symlink() or not path.is_file() or prior.sha(path) != digest:
            raise HotfixGuard("Reviewed test evidence missing or hash mismatch: " + name)


def preflight():
    """Read-only. No backup key, runtime config, token, service stop, or writes."""
    head = source_gate()
    checked_binary(CANDIDATE, EXPECTED_NEW_SHA)
    checked_test_logs()
    goose16_only()
    oldpid = active_old()
    current_plist_bytes()
    print("HOTFIX_PREFLIGHT_PASS", "source", head[:12], "old_pid", oldpid,
          "goose", 16, "old_sha", EXPECTED_OLD_SHA[:12], "candidate_sha", EXPECTED_NEW_SHA[:12],
          "backup_not_run", True, "live_service_unchanged", True)
    return head, oldpid


def save_json(path, data):
    prior.safe_write(path, (json.dumps(data, sort_keys=True, indent=2) + "\n").encode())


def prepare_operator_material():
    prior.protected_dir(RELEASE)
    if NEW_BINARY.exists():
        checked_binary(NEW_BINARY, EXPECTED_NEW_SHA)
    else:
        # Stage in owner-only private release directory, never overwrite old.
        prior.safe_write(NEW_BINARY, CANDIDATE.read_bytes())
        NEW_BINARY.chmod(0o700)
        checked_binary(NEW_BINARY, EXPECTED_NEW_SHA)
    old = current_plist_bytes()
    if ORIGINAL_PLIST.exists():
        if prior.private_read(ORIGINAL_PLIST) != old:
            raise HotfixGuard("Previous protected rollback plist differs from live plist")
    else:
        prior.safe_write(ORIGINAL_PLIST, old)


def operator_secret_precheck():
    """Human Operator apply only: reject missing privileged inputs BEFORE downtime."""
    goose16.enforce_file(goose16.OPS, goose16.EXPECTED_OPS_SHA)
    prior.valid_config(json.loads(prior.private_read(prior.CONFIG)))
    key = prior.private_read(Path.home() / ".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", key):
        raise HotfixGuard("Human Operator HMAC signing key invalid")
    key = ""
    token = prior.private_read(Path.home() / ".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}", token):
        raise HotfixGuard("Scoped read-only acceptance token unavailable")
    token = ""


def signed_backup_and_restore():
    """Human Operator ONLY: invocations access the protected signing key."""
    goose16.enforce_file(goose16.OPS, goose16.EXPECTED_OPS_SHA)
    key = prior.private_read(Path.home() / ".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", key):
        raise HotfixGuard("Human Operator HMAC signing key invalid")
    env = prior.runtime_env()
    env.update({"QUAL_BACKUP_HMAC_KEY": key,
                "PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"})
    directory = prior.protected_dir(prior.STATE_DIR / "backups")
    out = directory / ("qual49-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + os.urandom(4).hex())
    try:
        backup = json.loads(goose16.exec_safe([str(goose16.OPS), "backup", "-out", str(out)], env, timeout=360))
        verify = json.loads(goose16.exec_safe([str(goose16.OPS), "verify", "-path", str(out)], env, timeout=90))
        restore = json.loads(goose16.exec_safe([str(goose16.OPS), "restore-drill", "-path", str(out)], env, timeout=360))
        if (backup.get("verified") is not True or backup.get("database") != "qual" or backup.get("table_count", 0) < 20 or
            backup.get("object_count", 0) < 1 or verify.get("verified") is not True or
            restore.get("verified") is not True or restore.get("postgresql_restored") is not True or
            restore.get("isolated_s3_restored") is not True or restore.get("scratch_cleaned") is not True or
            restore.get("objects_restored") != backup.get("object_count")):
            raise HotfixGuard("Human Operator backup/verify/isolated restore failed")
        return {"backup_dir_private": str(out), "signed_backup_verified": True,
                "isolated_pg_s3_restore_verified": True,
                "table_count": backup["table_count"], "object_count": backup["object_count"]}
    finally:
        key = ""
        env.pop("QUAL_BACKUP_HMAC_KEY", None)


def boot_verified(binary):
    prior.launchctl("bootstrap", "gui/%d" % os.getuid(), str(prior.PLIST))
    for _ in range(100):
        pid = prior.listening_pid(18080)
        if pid and prior.matches_binary(pid, binary) and prior.http_status("http://127.0.0.1:18080/readyz")[0] == 200:
            return pid
        time.sleep(0.25)
    raise HotfixGuard("LaunchAgent failed to boot pinned healthy release")


def rollback_previous():
    """Binary-only restore; never executes Goose Down or overwrites live data."""
    if prior.is_managed():
        prior.launchctl("bootout", JOB)
    prior.wait_port(18080, None, seconds=20)
    goose16_only()
    checked_binary(OLD_BINARY, EXPECTED_OLD_SHA)
    prior.safe_write(prior.PLIST, prior.private_read(ORIGINAL_PLIST), private_parent=False)
    pid = boot_verified(OLD_BINARY)
    print("HOTFIX_ROLLBACK_OLD_GOOSE16_RESTORED", "pid", pid, "ready", 200)


def postrelease_check():
    # Operator-only client token is read once; never printed or persisted.
    token = prior.private_read(Path.home() / ".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}", token):
        raise HotfixGuard("Scoped real PI token unavailable for read-only acceptance")
    try:
        checks = goose16.verify_candidate(18080, token, expected_new=True)
        for item in ("06fd0d51-6f71-4759-86dc-ab79c47fd764", "d80e1cc5-1e34-445b-a5d2-cdf02a0116bf"):
            endpoint = "http://127.0.0.1:18080/api/v1/projects/%s/work-items/%s" % (goose16.PROJECT, item)
            if prior.http_status(endpoint, token)[0] != 200:
                raise HotfixGuard("Reviewed high-risk work item unreadable after cutover")
            if prior.http_status(endpoint)[0] != 401:
                raise HotfixGuard("Anonymous WorkItem read not rejected after cutover")
        return checks
    finally:
        token = ""


def apply(explicit=False):
    """Human Operator ONLY, never call as a PI Agent or from unattended CI."""
    if not explicit:
        raise HotfixGuard("Human Operator must explicitly request apply --apply")
    head, oldpid = preflight()
    # Privileged operator inputs are inspected only on this explicit Human
    # path, before stopping the working service. PI preflight stays read-only.
    operator_secret_precheck()
    prepare_operator_material()
    lock_path = RELEASE / "exclusive-hotfix.lock"
    with open(lock_path, "a+b") as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise HotfixGuard("Another Operator holds Qual release lock") from exc
        if active_old() != oldpid or current_plist_bytes() != prior.private_read(ORIGINAL_PLIST):
            raise HotfixGuard("Live old process or launcher changed during preflight")
        prior.launchctl("bootout", JOB)
        try:
            prior.wait_port(18080, None, seconds=20)
            goose16.require_no_other_database_writers()
            fingerprints = goose16.fingerprint()
            receipt = signed_backup_and_restore()
            if fingerprints != goose16.fingerprint():
                raise HotfixGuard("Immutable Qual facts changed during signed backup window")
            goose16_only()  # no migration is ever run by this controller
            old_plist = plistlib.loads(prior.private_read(ORIGINAL_PLIST))
            old_plist["ProgramArguments"] = NEW_RUN
            prior.safe_write(prior.PLIST, plistlib.dumps(old_plist), private_parent=False)
            if subprocess.run(["/usr/bin/plutil", "-lint", str(prior.PLIST)],
                              capture_output=True, timeout=8).returncode:
                raise HotfixGuard("New launchd plist syntax rejected")
            pid = boot_verified(NEW_BINARY)
            if prior.listening_pid(18080) != pid:
                raise HotfixGuard("A foreign listener stole the Qual port")
            goose16_only()
            checks = postrelease_check()
            save_json(RECEIPT, {"source_commit": head, "goose_before": 16, "goose_after": 16,
                                "old_binary_sha256": EXPECTED_OLD_SHA, "new_binary_sha256": EXPECTED_NEW_SHA,
                                "old_pid": oldpid, "new_pid": pid, "launchd_label": LABEL,
                                "operator_signed_backup": receipt,
                                "pre_boot_business_fingerprints_stable": True, "read_only_api_codes": checks,
                                "ready_http": 200, "hotfix_type": "Goose16 code-only/no migrations",
                                "authorization": "Explicit Human Operator apply --apply required",
                                "at_utc": datetime.now(timezone.utc).isoformat(),
                                "no_claim_of_production_or_independent_human_test": True})
            print("GOOSE16_CODE_HOTFIX_APPLIED", "pid", pid, "goose", 16,
                  "new_sha", EXPECTED_NEW_SHA[:12], "signed_backup", True,
                  "isolated_restore", True, "ready", 200)
        except Exception:
            # Fail closed. Existing Goose16 binary remains the code rollback;
            # never restore old DB snapshot over any new business fact.
            print("HOTFIX_APPLY_FAILED_ATTEMPT_BINARY_ONLY_ROLLBACK")
            try:
                rollback_previous()
            except Exception as err:
                print("HOTFIX_ROLLBACK_REQUIRES_HUMAN_OPERATOR", type(err).__name__)
            raise


def run_server():
    checked_binary(NEW_BINARY, EXPECTED_NEW_SHA)
    env = prior.runtime_env(18080)  # Human Operator's private launchd runtime config
    os.chdir(ROOT)
    os.execve(str(NEW_BINARY), [str(NEW_BINARY)], env)


def inspect():
    source = source_gate()
    checked_binary(CANDIDATE, EXPECTED_NEW_SHA)
    version = goose16.check_goose_schema()
    pid = prior.listening_pid(18080)
    print("GOOSE16_HOTFIX_STATUS", "source", source[:12], "goose", version,
          "pid", pid, "old_release_active", bool(pid and prior.matches_binary(pid, OLD_BINARY)),
          "new_release_active", bool(pid and NEW_BINARY.is_file() and prior.matches_binary(pid, NEW_BINARY)),
          "ready_http", prior.http_status("http://127.0.0.1:18080/readyz")[0],
          "receipt_exists", RECEIPT.exists(), "candidate_sha", EXPECTED_NEW_SHA[:12])


def main():
    parser = argparse.ArgumentParser(description="Goose16 local-only Human Operator hotfix")
    parser.add_argument("action", choices=("inspect", "preflight", "apply", "run"))
    parser.add_argument("--apply", action="store_true", help="Human Operator explicit signed-backup cutover")
    args = parser.parse_args()
    try:
        if args.action == "inspect":
            inspect()
        elif args.action == "preflight":
            preflight()
        elif args.action == "apply":
            apply(args.apply)
        else:
            run_server()
    except (HotfixGuard, prior.GuardError, goose16.Guard, OSError, ValueError, KeyError,
            subprocess.TimeoutExpired) as err:
        # Unknown exceptions (including failed DB/CLI) must not dump private
        # environment or responses. The known guard messages contain no secret.
        safe = str(err) if isinstance(err, (HotfixGuard, prior.GuardError, goose16.Guard)) else "operation rejected"
        print("GOOSE16_HOTFIX_GUARD", type(err).__name__, safe, file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    main()
