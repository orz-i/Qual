#!/usr/bin/env python3
"""Guarded Goose13→16 release of the local loopback Qual launchd service.

This is a development/pilot Operator release, never a public/production rollout.
No secret is accepted on argv or printed. The signed backup + independently
restored scratch PostgreSQL/S3 bytes are mandatory while the old server is down.
Fail closed after migration; NEVER try a destructive Goose Down.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import plistlib
import re
import shutil
import socket
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("qual_prior_local_release", ROOT / "scripts/ops/qual_launchd_server.py")
prior = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prior)

EXPECTED_BASE = "e92ff7603671290da28ef3465862333f0f03e3b6"
EXPECTED_SERVER_SHA = "c17a1ce5038c9860cb51bd8b5914fead624367e972dae829dcb5af9fbef61572"
EXPECTED_OPS_SHA = "b9e2e77b67484b07d26b379a0546b954ebcc18ed415e27e0ef8b15233fdb0f93"
EXPECTED_GOOSE_SHA = "f7ac5a67387a9f2c7aa6a990647b674c0bf77cf0d5c988e62cc087b34e7302a3"
OLD_BIN = ROOT / "tmp/phase10/qual25-release/qual-server"
OLD_SHA = "a7807449ef0d9bf00cf769cef338b741e4ac9d02a7ddcbb7a3138067c6a12284"
RELEASE = prior.STATE_DIR / "qual16-release"
SERVER = RELEASE / "qual-server"
OPS = RELEASE / "qual-ops"
META = RELEASE / "release-prepared.json"
PREFLIGHT = RELEASE / "preflight.json"
ORIGINAL_PLIST = RELEASE / "original-launchd.plist"
BACKUP_RECEIPT = RELEASE / "signed-backup-and-restore.json"
FINAL_RECEIPT = RELEASE / "goose16-live-release.json"
MIGRATIONS = ROOT / "apps/server/migrations"
GOOSE = ROOT / "tmp/qual-governance/bin/goose"
LABEL = prior.LABEL
JOB = f"gui/{os.getuid()}/{LABEL}"
PROJECT = "e6560273-31f1-4c3f-b690-3b4586977700"
WORK_ITEM = "6ecfe29a-34c3-485f-ae98-92e7878d87ca"

class Guard(Exception):
    pass


def now_utc():
    return datetime.now(timezone.utc).isoformat()


def sha(path: Path) -> str:
    return prior.sha(path)


def enforce_file(path: Path, expected: str):
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or sha(path) != expected:
        raise Guard(f"Untrusted or altered release file: {path.name}")


def read_json(path: Path) -> dict:
    return json.loads(prior.private_read(path))


def save_json(path: Path, data: dict):
    prior.safe_write(path, (json.dumps(data, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode())


def exec_safe(argv, env=None, timeout=60):
    result = subprocess.run(argv, cwd=ROOT, env=env, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        # Deliberately exclude argv, stderr, stdout: they may contain private DSN.
        raise Guard(f"Operator command failed: {Path(argv[0]).name}")
    return result.stdout


def source_gate():
    if not (ROOT / ".git").exists():
        raise Guard("Not a registered Qual worktree")
    current = exec_safe(["/usr/bin/git", "-C", str(ROOT), "rev-parse", "HEAD"], timeout=8).strip()
    ancestor = subprocess.run(["/usr/bin/git", "-C", str(ROOT), "merge-base", "--is-ancestor",
                               EXPECTED_BASE, current], capture_output=True, timeout=8)
    if ancestor.returncode:
        raise Guard("Unapproved candidate is not in release source history")
    source_changes = exec_safe(["/usr/bin/git", "-C", str(ROOT), "diff",
                               "--name-only", EXPECTED_BASE, current,
                               "--", "apps/", "packages/"], timeout=8)
    if source_changes.strip():
        raise Guard("Application/SDK source changed since tested candidate; requalify")
    return current


def check_goose_schema():
    from urllib.parse import urlsplit
    conf = prior.runtime_env()
    uri = urlsplit(conf["DATABASE_URL"])
    if uri.hostname != "127.0.0.1" or uri.port != 15432 or uri.path != "/qual":
        raise Guard("Real database name/host mismatch")
    query = ["/usr/local/bin/docker", "compose", "-f", str(ROOT / "deploy/compose.dev.yaml"),
             "exec", "-T", "postgres", "psql", "-U", "qual", "-d", "qual",
             "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1",
             "-c", "SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version WHERE is_applied=true"]
    actual = exec_safe(query, timeout=20).strip()
    try:
        return int(actual)
    except ValueError:
        raise Guard("Invalid real Goose schema ledger") from None


def validate_layout():
    src = source_gate()
    enforce_file(GOOSE, EXPECTED_GOOSE_SHA)
    if not prior.is_managed():
        raise Guard("Managed local Qual launchd job is missing")
    if prior.listening_pid(18080) is None:
        raise Guard("Live Qual listen port missing")
    if prior.http_status("http://127.0.0.1:18080/readyz")[0] != 200:
        raise Guard("Existing service is not ready")
    prior.valid_config(json.loads(prior.private_read(prior.CONFIG)))
    return src


def install_binary(source: Path, destination: Path, expected: str):
    enforce_file(source, expected)
    if destination.exists():
        enforce_file(destination, expected)
    else:
        prior.safe_write(destination, source.read_bytes())
        destination.chmod(0o700)
        enforce_file(destination, expected)


def prepare():
    src = validate_layout()
    if check_goose_schema() != 13:
        raise Guard("Prepare only allowed on existing default Goose13")
    if not prior.matches_binary(prior.listening_pid(18080), OLD_BIN):
        raise Guard("Active binary differs from the explicitly reviewed Goose13 release")
    enforce_file(OLD_BIN, OLD_SHA)
    prior.protected_dir(RELEASE)
    source_server = ROOT / "tmp/worktrees/qual27-integration-round3/tmp/release16/qual-server-candidate"
    source_ops = ROOT / "tmp/worktrees/qual27-integration-round3/tmp/release16/qual-ops-candidate"
    install_binary(source_server, SERVER, EXPECTED_SERVER_SHA)
    install_binary(source_ops, OPS, EXPECTED_OPS_SHA)
    if prior.PLIST.is_symlink() or prior.PLIST.stat().st_uid != os.getuid():
        raise Guard("Live launchd plist ownership/symlink rejected")
    plist = plistlib.loads(prior.PLIST.read_bytes())
    if (plist.get("Label") != LABEL or
        plist.get("WorkingDirectory") != str(ROOT) or
        plist.get("ProgramArguments") != ["/usr/bin/python3",
                                         str(ROOT / "scripts/ops/qual_goose13_rollout.py"), "run"]):
        raise Guard("Unexpected active Goose13 launcher; refusing to replace")
    if not ORIGINAL_PLIST.exists():
        prior.safe_write(ORIGINAL_PLIST, prior.PLIST.read_bytes())
    if prior.private_read(ORIGINAL_PLIST) != prior.PLIST.read_bytes():
        raise Guard("Original plist changed since approval")
    save_json(META, {
        "source_commit": src, "reviewed_base": EXPECTED_BASE,
        "candidate_server_sha256": EXPECTED_SERVER_SHA,
        "candidate_ops_sha256": EXPECTED_OPS_SHA,
        "goose_binary_sha256": EXPECTED_GOOSE_SHA,
        "original_plist_sha256": sha(ORIGINAL_PLIST),
        "goose_before": 13, "at_utc": now_utc(),
        "main_unmodified_by_prepare": True,
    })
    print("PREPARE_PASS", "source", src[:12], "schema", 13,
          "server_sha", EXPECTED_SERVER_SHA[:12], "operator_ops_sha", EXPECTED_OPS_SHA[:12])


def verify_candidate(port, token: str, expected_new: bool):
    base = f"http://127.0.0.1:{port}/api/v1/projects/{PROJECT}"
    def request(path, bearer=None):
        return prior.http_status(base + path, bearer)[0]
    auth = request(f"/work-items/{WORK_ITEM}", token)
    if auth != 200:
        raise Guard("Real scoped WorkItem read failed")
    if expected_new:
        actual = {
            "context": request(f"/work-items/{WORK_ITEM}/context?section=overview", token),
            "threads": request(f"/work-items/{WORK_ITEM}/threads", token),
            "runs": request(f"/work-items/{WORK_ITEM}/agent-runs", token),
            "anonymous_context": request(f"/work-items/{WORK_ITEM}/context"),
            "anonymous_runs": request(f"/work-items/{WORK_ITEM}/agent-runs"),
        }
        expected = {"context":200,"threads":200,"runs":200,"anonymous_context":401,"anonymous_runs":401}
    else:
        actual = {
            "context": request(f"/work-items/{WORK_ITEM}/context?section=overview", token),
            "threads": request(f"/work-items/{WORK_ITEM}/threads", token),
            "runs": request(f"/work-items/{WORK_ITEM}/agent-runs", token),
            "anonymous_context": request(f"/work-items/{WORK_ITEM}/context"),
            "anonymous_runs": request(f"/work-items/{WORK_ITEM}/agent-runs"),
        }
        expected = {name:404 for name in actual}
    if actual != expected:
        raise Guard(f"New route registration/permission mismatch: {actual}")
    # Existing real immutable S3 Artifact is a stronger check than green readyz.
    url = f"{base}/artifacts/{prior.ARTIFACT}/content"
    code, body = prior.http_status(url, token)
    if code != 200 or len(body) != 3125 or hashlib.sha256(body).hexdigest() != prior.ARTIFACT_SHA:
        raise Guard("Real private ready S3 artifact integrity probe failed")
    if request("/review-queue?limit=2") not in (401,403):
        raise Guard("Anonymous review queue access accepted")
    return actual


def preflight():
    prepare()
    token = prior.private_read(Path.home()/".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}", token):
        raise Guard("Real scoped credential unavailable")
    with socket.socket() as sk:
        sk.bind(("127.0.0.1", 0))
        port = sk.getsockname()[1]
    path = RELEASE / "preflight-process.log"
    p = None
    try:
        with open(path, "ab") as out:
            p = subprocess.Popen([str(SERVER)], cwd=ROOT, env=prior.runtime_env(port),
                                 stdin=subprocess.DEVNULL, stdout=out,
                                 stderr=subprocess.STDOUT, start_new_session=True)
        for _ in range(80):
            if p.poll() is not None:
                raise Guard("Candidate quit during Goose13-only preflight")
            if prior.listening_pid(port) == p.pid and prior.http_status(f"http://127.0.0.1:{port}/readyz")[0] == 200:
                break
            time.sleep(.15)
        else:
            raise Guard("Candidate preflight ready timeout")
        checks = verify_candidate(port, token, expected_new=False)
        save_json(PREFLIGHT, {
            "at_utc": now_utc(), "goose": 13, "source_commit": source_gate(),
            "binary_sha": EXPECTED_SERVER_SHA, "real_s3_sha": prior.ARTIFACT_SHA,
            "route_codes": checks, "passed": True,
        })
        print("PREFLIGHT_PASS", "goose13=compatible", "old_routes=200",
              "new_routes=404", "real_s3_sha=PASS", "candidate_sha", EXPECTED_SERVER_SHA[:12])
    finally:
        token = ""
        if p is not None and p.poll() is None:
            p.terminate()
            try:
                p.wait(timeout=8)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait(timeout=3)


def fingerprint():
    # Only immutable business rows, not credentials or secrets.
    sql = """
SELECT 'work_items:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.work_items t;
SELECT 'work_role_entries:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.work_role_entries t;
SELECT 'work_acceptance_decisions:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.work_acceptance_decisions t;
SELECT 'audit_records:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.audit_records t;
SELECT 'test_attempts:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.test_attempts t;
SELECT 'artifacts:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),'')) FROM qual.artifacts t;
"""
    cmd = ["/usr/local/bin/docker","compose","-f",str(ROOT/"deploy/compose.dev.yaml"),
           "exec","-T","postgres","psql","-U","qual","-d","qual","-X","-A","-t",
           "-v","ON_ERROR_STOP=1","-c",sql]
    return exec_safe(cmd,timeout=30).strip().splitlines()


def require_no_other_database_writers():
    # The only permissible client is this query. No other PID can mutate
    # business data while the logical snapshot and S3 backup is in progress.
    cmd = ["/usr/local/bin/docker","compose","-f",str(ROOT/"deploy/compose.dev.yaml"),
           "exec","-T","postgres","psql","-U","qual","-d","qual","-X","-A","-t",
           "-v","ON_ERROR_STOP=1","-c",
           "SELECT count(*) FROM pg_stat_activity WHERE datname='qual' AND pid<>pg_backend_pid() AND backend_type='client backend'"]
    if exec_safe(cmd,timeout=15).strip() != "0":
        raise Guard("Uncontrolled live PostgreSQL clients remain after launchd shutdown")


def backup_and_restore():
    key = prior.private_read(Path.home()/".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", key):
        raise Guard("Backup signing key not valid")
    env = prior.runtime_env()
    env.update({
        "PATH":"/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin",
        "QUAL_BACKUP_HMAC_KEY": key,
    })
    backups = prior.protected_dir(prior.STATE_DIR / "backups")
    backup_dest = backups / ("goose16-" + datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") +
                             "-" + os.urandom(4).hex())
    # The operator binary creates the new directory; do not create it here.
    backup = json.loads(exec_safe([str(OPS),"backup","-out",str(backup_dest)],env,timeout=360))
    if (backup.get("verified") is not True or backup.get("database") != "qual" or
        backup.get("table_count",0) < 20 or backup.get("object_count",0) < 1):
        raise Guard("Signed live backup incomplete")
    verify = json.loads(exec_safe([str(OPS),"verify","-path",str(backup_dest)],env,timeout=90))
    drill = json.loads(exec_safe([str(OPS),"restore-drill","-path",str(backup_dest)],env,timeout=360))
    if (verify.get("verified") is not True or verify.get("database") != "qual" or
        drill.get("verified") is not True or drill.get("postgresql_restored") is not True or
        drill.get("isolated_s3_restored") is not True or drill.get("scratch_cleaned") is not True or
        drill.get("objects_restored") != backup["object_count"]):
        raise Guard("Signed backup independent PostgreSQL/S3 restoration failed")
    save_json(BACKUP_RECEIPT, {
        "at_utc": now_utc(), "backup_path_private": str(backup_dest),
        "goose_before": 13, "signed_verify": True, "scratch_restore_verified": True,
        "table_count": backup["table_count"], "object_count": backup["object_count"],
        "objects_restored": drill["objects_restored"], "scratch_cleaned": drill["scratch_cleaned"],
        "snapshot_dump_sha256": json.loads((backup_dest/"manifest.json").read_text())["database_sha256"],
    })
    print("SIGNED_BACKUP_PASS", "tables",backup["table_count"],"objects",backup["object_count"],
          "RESTORE_DRILL_PASS", "objects_restored",drill["objects_restored"],
          "scratch_cleaned",drill["scratch_cleaned"])
    return backup_dest


def migrate_to_16():
    enforce_file(GOOSE, EXPECTED_GOOSE_SHA)
    env = prior.runtime_env()
    env.update({
        "GOOSE_DRIVER":"postgres",
        "GOOSE_DBSTRING":env["DATABASE_URL"],  # env only; NEVER in process argv
        "GOOSE_MIGRATION_DIR":str(MIGRATIONS),
    })
    for version in (14,15,16):
        if check_goose_schema() != version-1:
            raise Guard(f"Unexpected migration ledger before Goose{version}")
        exec_safe([str(GOOSE),"up-to",str(version)],env,timeout=150)
        if check_goose_schema() != version:
            raise Guard(f"Expected exact Goose{version} was not applied")
        print(f"DEFAULT_GOOSE{version}_APPLIED_AND_LEDGER_VERIFIED")
    # Do not claim complete if any required physical object is absent.
    cmd = ["/usr/local/bin/docker","compose","-f",str(ROOT/"deploy/compose.dev.yaml"),
           "exec","-T","postgres","psql","-U","qual","-d","qual","-X","-A","-t",
           "-v","ON_ERROR_STOP=1","-c",
           "SELECT to_regclass('qual.external_threads') IS NOT NULL AND "
           "to_regclass('qual.work_thread_links') IS NOT NULL AND "
           "to_regclass('qual.test_case_coverage_work_context_idx') IS NOT NULL AND "
           "to_regclass('qual.test_run_items_case_context_idx') IS NOT NULL AND "
           "to_regclass('qual.test_attempts_run_context_idx') IS NOT NULL AND "
           "to_regclass('qual.test_attempts_project_ingested_context_idx') IS NOT NULL AND "
           "to_regclass('qual.agent_runs') IS NOT NULL AND "
           "to_regclass('qual.agent_run_reports') IS NOT NULL"]
    if exec_safe(cmd,timeout=15).strip() != "t":
        raise Guard("Goose ledger without required physical schema objects")


def plist_for_new_runner():
    original = plistlib.loads(prior.private_read(ORIGINAL_PLIST))
    if original.get("Label") != LABEL or original.get("WorkingDirectory") != str(ROOT):
        raise Guard("Saved original plist does not match Qual service")
    original["ProgramArguments"] = ["/usr/bin/python3", str(ROOT/"scripts/ops/qual_goose16_rollout.py"),"run"]
    return plistlib.dumps(original)


def boot():
    prior.launchctl("bootstrap",f"gui/{os.getuid()}",str(prior.PLIST))
    for _ in range(125):
        pid = prior.listening_pid(18080)
        if pid is not None and prior.matches_binary(pid,SERVER) and prior.http_status("http://127.0.0.1:18080/readyz")[0] == 200:
            return pid
        time.sleep(.2)
    raise Guard("New managed service failed health/binary identity")


def restore_old():
    if prior.is_managed():
        prior.launchctl("bootout",JOB)
    prior.wait_port(18080,None,seconds=16)
    if check_goose_schema() != 13:
        raise Guard("Backward rollback prohibited after any Goose14+ migration")
    prior.safe_write(prior.PLIST, prior.private_read(ORIGINAL_PLIST), private_parent=False)
    prior.launchctl("bootstrap",f"gui/{os.getuid()}",str(prior.PLIST))
    pid = prior.wait_port(18080,"any",seconds=22)
    if not prior.matches_binary(pid,OLD_BIN) or prior.http_status("http://127.0.0.1:18080/readyz")[0] != 200:
        raise Guard("Old Goose13 local service recovery did not pass")
    print("PRIOR_GOOSE13_SERVICE_RESTORED_ON_PREMIGRATION_ABORT")


def cutover(explicit_apply=False):
    if not explicit_apply:
        raise Guard("Live cutover requires --apply; never execute by status/preflight")
    prepared = read_json(META)
    receipt = read_json(PREFLIGHT)
    if not receipt.get("passed") or receipt.get("binary_sha") != EXPECTED_SERVER_SHA or receipt.get("goose") != 13:
        raise Guard("No validated compatibility preflight")
    at = datetime.fromisoformat(receipt["at_utc"])
    if at.tzinfo is None or (datetime.now(timezone.utc)-at).total_seconds() > 3*3600:
        raise Guard("Preflight is too old")
    if prepared["source_commit"] != source_gate() or read_json(META)["candidate_server_sha256"] != EXPECTED_SERVER_SHA:
        raise Guard("Release metadata/source revision was changed")
    enforce_file(SERVER, EXPECTED_SERVER_SHA)
    enforce_file(OPS, EXPECTED_OPS_SHA)
    enforce_file(GOOSE, EXPECTED_GOOSE_SHA)
    if check_goose_schema() != 13:
        raise Guard("Default database is not Goose13 immediately before cutover")
    original_pid = prior.listening_pid(18080)
    if not original_pid or not prior.is_managed() or not prior.matches_binary(original_pid,OLD_BIN):
        raise Guard("Current service no longer matches reviewed prior binary")
    if prior.private_read(ORIGINAL_PLIST) != prior.PLIST.read_bytes():
        raise Guard("Active launchd service changed since preflight")
    # Prevent multiple local operator commands from interleaving cutovers.
    import fcntl
    lock_path = RELEASE / "exclusive-cutover.lock"
    with open(lock_path,"a+b") as lock:
        try: fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError: raise Guard("Another Goose16 release operator holds exclusive lock")
        prior.launchctl("bootout",JOB)
        prior.wait_port(18080,None,seconds=20)
        migrated = False
        try:
            require_no_other_database_writers()
            fingerprints = fingerprint()
            backup_path = backup_and_restore()
            if fingerprints != fingerprint():
                raise Guard("Business facts changed during signed backup window")
            print("BACKUP_FINGERPRINTS_STABLE")
            migrate_to_16()
            migrated = True
            if fingerprints != fingerprint():
                raise Guard("Goose14–16 changed immutable existing business facts")
            prior.safe_write(prior.PLIST,plist_for_new_runner(),private_parent=False)
            pid = boot()
            token = prior.private_read(Path.home()/".config/qual/qual-pi-agent.token").decode().strip()
            if not re.fullmatch(r"qual_pat_[0-9a-f]{64}",token):
                raise Guard("Post-release scoped PI token unavailable")
            checks = verify_candidate(18080,token,expected_new=True)
            if check_goose_schema() != 16:
                raise Guard("Goose16 ledger changed after service startup")
            release = {
                "at_utc": now_utc(), "status":"PASS",
                "source_commit": source_gate(), "tested_base": EXPECTED_BASE,
                "goose_before":13, "goose_after":16,
                "candidate_sha256":EXPECTED_SERVER_SHA,
                "prior_pid":original_pid, "new_pid":pid, "launchd_label":LABEL,
                "signed_backup_verified":True,"isolated_postgresql_s3_restore_verified":True,
                "backup_receipt_path":str(BACKUP_RECEIPT),
                "business_fingerprints_unchanged_at_migration":True,
                "private_s3_artifact_read_sha256_verified":True,
                "actual_http":checks,
                "no_downgrade_or_destructive_down":True,
                "local_loopback_pilot_only":True,
                "operator_execution_identity":"macOS user via explicit ChatGPT user-directed Mac PI",
                "no_independent_human_qual_review_implied":True,
            }
            save_json(FINAL_RECEIPT,release)
            print("GOOSE16_DEFAULT_LIVE_CUTOVER_PASS", "goose",16,
                  "ready_http",200,"pid",pid, "S3_SHA_PASS",True)
        except Exception:
            print("GOOSE16_CUTOVER_FAILED_CLEAN_RECOVERY_IF_STILL_GOOSE13")
            if check_goose_schema() == 13:
                try: restore_old()
                except Exception as ex:
                    print("PRIOR_SERVICE_RECOVERY_FAILED",type(ex).__name__)
            else:
                # After any Goose14+ write, the old Goose13 runner will not
                # pass readiness and a destructive Down is forbidden. If the
                # partial schema is internally consistent, bring up the new
                # binary in degraded-version mode, preserving service access.
                # Its inspectSchema() is authoritative and fails closed on
                # missing indexes/tables; never call this a completed release.
                print("SCHEMA_UPGRADED: FORWARD_REPAIR_ONLY_NO_DESTRUCTIVE_DOWN")
                try:
                    if not prior.is_managed() and prior.listening_pid(18080) is None:
                        prior.safe_write(prior.PLIST, plist_for_new_runner(), private_parent=False)
                        partial_pid = boot()
                        print("PARTIAL_SCHEMA_COMPATIBLE_SERVICE_RESTARTED", "pid", partial_pid,
                              "goose", check_goose_schema(), "release_accepted", False)
                except Exception as recover_error:
                    print("PARTIAL_SCHEMA_RECOVERY_REQUIRES_OPERATOR", type(recover_error).__name__)
            raise


def run_server():
    enforce_file(SERVER,EXPECTED_SERVER_SHA)
    # Server inspectSchema() fails closed at startup and readiness; launchd
    # intentionally lacks Docker in PATH and should exec only the binary.
    env = prior.runtime_env(18080)
    os.chdir(ROOT)
    os.execve(str(SERVER),[str(SERVER)],env)


def status():
    goose=check_goose_schema()
    pid=prior.listening_pid(18080)
    active=bool(pid and prior.matches_binary(pid,SERVER))
    print("DEFAULT_GOOSE",goose,"PID",pid,"REVIEWED_GOOSE16_BINARY",active,
          "READY_HTTP",prior.http_status("http://127.0.0.1:18080/readyz")[0],
          "RELEASE_RECEIPT",FINAL_RECEIPT.exists())


def main():
    arg=argparse.ArgumentParser()
    arg.add_argument("action", choices=["prepare","preflight","cutover","run","status"])
    arg.add_argument("--apply",action="store_true")
    cmd=arg.parse_args()
    try:
        if cmd.action=="prepare":prepare()
        elif cmd.action=="preflight":preflight()
        elif cmd.action=="cutover":cutover(cmd.apply)
        elif cmd.action=="run":run_server()
        else:status()
    except (Guard,prior.GuardError,ValueError,KeyError,OSError,subprocess.TimeoutExpired) as err:
        print("GOOSE16_OPERATOR_GUARD",type(err).__name__,
              # Only safe known message; do not include runtime exception from unknown APIs.
              str(err) if isinstance(err,(Guard,prior.GuardError)) else "operator action rejected",
              file=sys.stderr)
        raise SystemExit(3)

if __name__=="__main__":
    main()
