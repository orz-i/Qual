#!/usr/bin/env python3
"""QUAL-24 local Goose13 cutover with explicit source, backup, and rollout guards.

This is NOT for production or for untrusted databases. No runtime secrets
are logged; only the existing private QUAL-23 config is used.
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
import subprocess
import sys
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("prior_goose12_runner", ROOT / "scripts/ops/qual_launchd_server.py")
prior = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prior)
NEW = ROOT / "tmp/phase10/qual25-release/qual-server"
NEW_HASH = "a7807449ef0d9bf00cf769cef338b741e4ac9d02a7ddcbb7a3138067c6a12284"
MIGRATION = ROOT / "apps/server/migrations/00013_explicit_review_acceptance.sql"
MIGRATION_HASH = "92f2a0c0c54bd94362dddf89ea257953b148acf2756b4024775c7b9194fcb1af"
OLD_PLIST = prior.STATE_DIR / "qual24-goose12-original-launchd.plist"
BACKUP_RECEIPT = prior.STATE_DIR / "qual24-precutover-backup.json"
RELEASE_RECEIPT = prior.STATE_DIR / "qual24-goose13-release.json"
JOB = f"gui/{os.getuid()}/{prior.LABEL}"
PROJECT = "e6560273-31f1-4c3f-b690-3b4586977700"
S3_TEST_ARTIFACT = "276dedc9-da37-48de-9d3c-5458ca13176a"
S3_TEST_SHA = "e08d450c5d3a181cd9e316e6ee2269359fb1c83729b93854e4ab6570dd8ebbed"


class ReleaseGuardError(Exception):
    pass


def enforce_hash(path, digest):
    if not path.is_file() or path.is_symlink() or prior.sha(path) != digest:
        raise ReleaseGuardError("Approved candidate/migration hash mismatch")


def psql(statement: str = "", sql_file: Path | None = None, database: str = "qual", atomic: bool = False):
    if database != "qual":
        raise ReleaseGuardError("Only the actual local qual database is permitted")
    command = ["docker", "compose", "-f", str(ROOT / "deploy/compose.dev.yaml"),
               "exec", "-T", "postgres", "psql", "-U", "qual", "-d", database,
               "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1"]
    if atomic:
        command += ["-1"]
    if sql_file is not None:
        command += ["-f", "/dev/stdin"]
        statement = sql_file.read_text()
    else:
        command += ["-c", statement] if not atomic else []
    out = subprocess.run(command, input=statement if atomic or sql_file is not None else None,
                         cwd=ROOT, capture_output=True, text=True, timeout=50)
    if out.returncode:
        raise ReleaseGuardError("Controlled Postgres migration/check failed")
    return out.stdout.strip()


def goose():
    return int(psql("SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version WHERE is_applied=true;"))


def business_fingerprints():
    sql = """
      SELECT 'work_items:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),''))
        FROM qual.work_items t;
      SELECT 'work_role_entries:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),''))
        FROM qual.work_role_entries t;
      SELECT 'audit_records:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),''))
        FROM qual.audit_records t;
      SELECT 'test_attempts:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),''))
        FROM qual.test_attempts t;
      SELECT 'artifacts:'||count(*)||':'||md5(COALESCE(string_agg(md5(to_jsonb(t)::text),'|' ORDER BY id::text),''))
        FROM qual.artifacts t;
    """
    return psql(sql).splitlines()


def authorized_task():
    r = subprocess.run([str(ROOT / "scripts/qual-pi.sh"), "cli", "show", "-project", "QUAL",
                        "-item", "78e3bbae-2110-4f69-9007-3602b51c134f"],
                       cwd=ROOT, capture_output=True, text=True, timeout=10)
    if r.returncode:
        raise ReleaseGuardError("Cannot read actual QUAL-24 from restricted PI")
    item = json.loads(r.stdout)
    if item["status"] != "in_progress" or item["assignee_id"] != "7f4e7c05-8348-4dda-af25-ad41bf582b58":
        raise ReleaseGuardError("QUAL-24 has not been explicitly claimed by real Human Operator")
    r = subprocess.run([str(ROOT / "scripts/qual-pi.sh"), "cli", "show", "-project", "QUAL",
                        "-item", "053f53af-0790-4f3c-a4da-42ead5a9d5ac"],
                       cwd=ROOT, capture_output=True, text=True, timeout=10)
    if r.returncode or json.loads(r.stdout)["status"] != "done":
        raise ReleaseGuardError("Stage C QUAL-21 must have real Human completion")
    return item["version"]


def verify_backup():
    document = json.loads(prior.private_read(BACKUP_RECEIPT))
    backup = Path(document.get("backup_path_private", ""))
    if document.get("schema_before") != 12 or document.get("signed_verify") == "FAIL":
        pass  # legacy shape: actual keys below
    if (document.get("backup_and_signature") != "PASS"
        or document.get("independent_scratch_restore_drill") != "PASS"
        or document.get("schema_before") != 12
        or backup.is_symlink() or not backup.is_dir()
        or backup.parent != prior.STATE_DIR / "backups"):
        raise ReleaseGuardError("Fresh signed backup and scratch restore drill are mandatory")
    when = datetime.fromisoformat(document["at_utc"])
    if when.tzinfo is None or (datetime.now(timezone.utc) - when).total_seconds() > 6*3600:
        raise ReleaseGuardError("Backup must be recent")
    key = prior.private_read(Path.home() / ".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", key):
        raise ReleaseGuardError("Signing key unavailable")
    ops = ROOT / "tmp/phase10/qual24-hardened-release/qual-ops"
    enforce_hash(ops, "4140969b2c137a0f362f318fbe34151ac5f36f24aef50031114e1194c57980f3")
    env = prior.runtime_env()
    env.update({"QUAL_BACKUP_HMAC_KEY": key, "PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"})
    verify = subprocess.run([str(ops), "verify", "-path", str(backup)],
                            cwd=ROOT, env=env, capture_output=True, text=True, timeout=40)
    if verify.returncode or not json.loads(verify.stdout).get("verified"):
        raise ReleaseGuardError("Fresh signed backup verification failed")
    print("signed_backup=PASS prior_isolated_restore=PASS")


def run():
    enforce_hash(NEW, NEW_HASH)
    os.chdir(ROOT)
    exe = str(NEW.resolve())
    os.execve(exe, [exe], prior.runtime_env(18080))


def new_plist():
    source = plistlib.loads(prior.private_read(OLD_PLIST))
    if source.get("Label") != prior.LABEL:
        raise ReleaseGuardError("Original service label mismatch")
    source["ProgramArguments"] = ["/usr/bin/python3", str(Path(__file__).resolve()), "run"]
    if source.get("WorkingDirectory") != str(ROOT):
        raise ReleaseGuardError("Original project working directory mismatch")
    return plistlib.dumps(source)


def managed_pid():
    if not prior.is_managed():
        raise ReleaseGuardError("Expected existing launchd-managed Qual service missing")
    pid = prior.listening_pid(18080)
    if pid is None:
        raise ReleaseGuardError("No running Qual service on primary port")
    return pid


def wait_started():
    until = time.monotonic() + 18
    while time.monotonic() < until:
        pid = prior.listening_pid(18080)
        if pid and prior.matches_binary(pid, NEW) and prior.http_status("http://127.0.0.1:18080/readyz")[0] == 200:
            return pid
        time.sleep(.2)
    raise ReleaseGuardError("New launchd process did not pass binary identity and readiness checks")


def check_api(port=18080, require_goose13=False):
    if prior.http_status(f"http://127.0.0.1:{port}/readyz")[0] != 200:
        raise ReleaseGuardError("Ready check failed")
    # Actual scoped low-right PI probe. No secrets exposed.
    token = prior.private_read(Path.home() / ".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}", token):
        raise ReleaseGuardError("Real restricted PI PAT unavailable")
    url = f"http://127.0.0.1:{port}/api/v1/projects/{PROJECT}/artifacts/{S3_TEST_ARTIFACT}/content"
    code, body = prior.http_status(url, token)
    if code != 200 or len(body) != 3125 or hashlib.sha256(body).hexdigest() != S3_TEST_SHA:
        raise ReleaseGuardError("Real scoped S3 Ready Artifact integrity failed")
    code, _ = prior.http_status(f"http://127.0.0.1:{port}/api/v1/projects/{PROJECT}/review-queue?limit=2")
    if code not in (401, 403):
        raise ReleaseGuardError("Anonymous review queue was not denied")
    if require_goose13:
        with tempfile.TemporaryDirectory(prefix="goose13-post-cutover-", dir=str(prior.STATE_DIR)) as tmp:
            outcome = subprocess.run(["/usr/bin/python3","-B",str(ROOT/"scripts/qual24_goose13_cutover_probe.py"),
                                      "--result",str(Path(tmp)/"verify.json")],cwd=ROOT,
                                     capture_output=True,text=True,timeout=25)
            if outcome.returncode:
                raise ReleaseGuardError("Strict live Goose13 policy/legacy/authorization gate failed")
            result = json.loads(Path(tmp,"verify.json").read_text())
            if not result.get("passed") or any(v != 200 for v in result.get("status_codes",{}).values()):
                raise ReleaseGuardError("Goose13 API proof incomplete")
    return True


def migration(up: bool):
    enforce_hash(MIGRATION, MIGRATION_HASH)
    original = MIGRATION.read_text()
    if original.count("-- +goose Up") != 1 or original.count("-- +goose Down") != 1:
        raise ReleaseGuardError("Unexpected migration source")
    fragment = original.split("-- +goose Down")[0].replace("-- +goose Up","") if up else original.split("-- +goose Down")[1]
    preamble = """
    DO $QUAL$
    BEGIN
     IF current_database() <> 'qual' THEN RAISE EXCEPTION 'Wrong database'; END IF;
    END $QUAL$;
    """
    if up:
        if goose() != 12:
            raise ReleaseGuardError("Goose13 Up only allowed from Goose12")
        marker = "INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(13,true);"
    else:
        if goose() != 13:
            raise ReleaseGuardError("Goose13 Down only allowed from Goose13")
        marker = "UPDATE public.goose_db_version SET is_applied=false WHERE version_id=13 AND is_applied=true;"
    # psql -1 wraps SQL and version marker atomically. Its stderr is never printed (may contain SQL).
    psql(preamble + fragment + "\n" + marker,atomic=True)
    if goose() != (13 if up else 12):
        raise ReleaseGuardError("Migration version marker mismatch")
    print("versioned_Goose13_"+("Up" if up else "Down"),"transaction=PASS")


def boot_new():
    prior.launchctl("bootstrap",f"gui/{os.getuid()}",str(prior.PLIST))
    return wait_started()


def recover(pre_up=False):
    """Recovery only while governance records are EMPTY, never erase genuine history."""
    if prior.is_managed():
        prior.launchctl("bootout",JOB)
    prior.wait_port(18080,None,seconds=12)
    version=goose()
    if version==13:
        migration(up=False)  # SQL itself refuses to drop any governance records.
        version=goose()
    if version !=12:
        raise ReleaseGuardError("Backward rollback prohibited; retain Goose13 and seek forward repair")
    if not OLD_PLIST.is_file():
        raise ReleaseGuardError("Old plist backup missing")
    prior.safe_write(prior.PLIST,prior.private_read(OLD_PLIST),private_parent=False)
    prior.launchctl("bootstrap",f"gui/{os.getuid()}",str(prior.PLIST))
    old=prior.wait_port(18080,"any",seconds=16)
    if not prior.matches_binary(old,prior.NEW) or prior.http_status("http://127.0.0.1:18080/readyz")[0]!=200:
        raise ReleaseGuardError("Old pinned service rollback not healthy")
    print("goose12_old_release_rollback=PASS")


def cutover():
    authorized_task()
    enforce_hash(NEW,NEW_HASH)
    enforce_hash(MIGRATION,MIGRATION_HASH)
    if goose()!=12:
        raise ReleaseGuardError("Real default database is not pristine Goose12")
    verify_backup()
    old=managed_pid()
    if not prior.matches_binary(old,prior.NEW):
        raise ReleaseGuardError("Currently managed binary differs from reviewed Goose12 prior release")
    original=prior.PLIST
    if original.is_symlink() or original.stat().st_uid!=os.getuid():
        raise ReleaseGuardError("Original plist is not trusted")
    prior.safe_write(OLD_PLIST,original.read_bytes())
    before=business_fingerprints()
    print("human_operator_gate=PASS prior_pid",old,"old_goose",goose())
    activated=False
    upgraded=False
    try:
        prior.launchctl("bootout",JOB)
        prior.wait_port(18080,None,seconds=16)
        prior.safe_write(original,new_plist(),private_parent=False)
        new=boot_new()
        activated=True
        check_api(require_goose13=False)
        print("new_stagec_server_on_goose12=PASS pid",new)
        # Maintenance window: stop all HTTP writes before running the versioned SQL.
        prior.launchctl("bootout",JOB)
        prior.wait_port(18080,None,seconds=16)
        migration(up=True)
        upgraded=True
        if before!=business_fingerprints():
            raise ReleaseGuardError("Goose13 changed immutable historic row fingerprints")
        new=boot_new()
        check_api(require_goose13=True)
        if goose()!=13:
            raise ReleaseGuardError("Goose13 schema did not remain applied")
        receipt={
          "status":"PASS","at_utc":datetime.now(timezone.utc).isoformat(),
          "work_item":"QUAL-24","human_operator_assignee_verified":True,
          "actual_os_operator":"Local PI Agent via authorized Mac UID",
          "goose_before":12,"goose_after":13,"previous_pid":old,
          "launchd_new_pid":new,"launchd_label":prior.LABEL,
          "reviewed_new_binary_sha256":NEW_HASH,
          "migration_sha256":MIGRATION_HASH,
          "business_row_fingerprints_preserved":True,
          "signed_backup_verified":True,"scratch_restore_drill_passed":True,
          "goose13_strict_live_probe_pass":True,"anonymous_rejected":True,
          "ready_http":200,"ready_artifact_sha_match":True,
          "source_git":"939f35f50598470db9999d11435f5b6a639f4103",
          "legacy_qual2_history_unclassified":True,
          "migration_rollback_prohibited_after_governance_records":True,
          "no_credential_values_recorded":True,"no_docker_restarts":True
        }
        prior.safe_write(RELEASE_RECEIPT,(json.dumps(receipt,indent=2)+"\n").encode())
        print("GOOSE13_LIVE_CUTOVER_PASS pid",new,"strict_policy_api=PASS")
        return
    except Exception as ex:
        print("upgrade_aborted_attempting_conditional_safe_recovery",type(ex).__name__)
        try:
            recover(pre_up=not upgraded)
        except Exception as recover_error:
            print("recovery_failed_forward_compatible_human_intervention_required",type(recover_error).__name__)
        raise


def status():
    current=goose()
    pid=prior.listening_pid(18080)
    print("goose",current,"server_pid",pid,"new_binary",bool(pid and prior.matches_binary(pid,NEW)),
          "launchd",prior.is_managed(),"ready_http",prior.http_status("http://127.0.0.1:18080/readyz")[0])


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("command",choices=["run","cutover","status"])
    args=parser.parse_args()
    try:
        if args.command=="run":run()
        elif args.command=="cutover":cutover()
        else:status()
    except (ReleaseGuardError,prior.GuardError,ValueError,OSError,subprocess.TimeoutExpired) as ex:
        print("QUAL24_RELEASE_GUARD_ERROR",type(ex).__name__,str(ex),file=sys.stderr)
        raise SystemExit(3)


if __name__=="__main__":
    main()
