#!/usr/bin/env python3
"""QUAL-30 Goose16->17 Human Operator-only local release controller.

Read-only inspect and preflight never read the HMAC key, token or private runtime
configuration. Only a trusted Human's explicit apply --apply --reviewed-sha can
stop launchd, create a signed PostgreSQL/S3 backup, run the isolated restore,
migrate Goose17 and switch the pinned Go Server.

Mac Pi must NEVER invoke apply/run itself or impersonate an independent Human.
This is a loopback local pilot, not a public production rollout.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
LIVE_ROOT = Path("/Users/mini/Coding/Qual")
sys.path.insert(0, str(ROOT / "scripts/ops"))
import qual_launchd_server as prior  # noqa: E402
import qual_goose16_rollout as goose16  # noqa: E402
import qual30_goose17_preflight as guard  # noqa: E402

REVIEWED_SOURCE = "edcf2d391dfbb76cf7c8258abd95627a53909684"
EXPECTED_SERVER_SHA = "acc0060d1f7bd16538e6d94e026560c8af8eb741ef9868972139454b5ddbd710"
EXPECTED_PREVIOUS_SHA = "9e1ad81d23b62aff01dca1c935de7f09391051c40a9de65f8fe8d61d3be48a6e"
EXPECTED_GOOSE_SHA = "f7ac5a67387a9f2c7aa6a990647b674c0bf77cf0d5c988e62cc087b34e7302a3"
RELEASE = prior.STATE_DIR / "qual30-goose17-release"
NEW_BINARY = RELEASE / "qual-server"
OLD_BINARY = prior.STATE_DIR / "qual49-goose16-hotfix/qual-server"
CANDIDATE = LIVE_ROOT / "tmp/worktrees/qual30-goose17-release/tmp/goose17-release/qual-server"
ORIGINAL_PLIST = RELEASE / "old-goose16-qual49.plist"
RECEIPT = RELEASE / "goose17-live-release.json"
BACKUP_RECEIPT = RELEASE / "signed-backup-and-restore.json"
LABEL = prior.LABEL
JOB = "gui/%d/%s" % (os.getuid(), LABEL)
OLD_RUN = ["/usr/bin/python3", str(LIVE_ROOT/"scripts/ops/qual_goose16_hotfix.py"), "run"]
NEW_RUN = ["/usr/bin/python3", str(LIVE_ROOT/"scripts/ops/qual_goose17_rollout.py"), "run"]
PROJECT = "e6560273-31f1-4c3f-b690-3b4586977700"
WORK_ITEM = "d6ab8709-e407-4565-9b50-a097f97bee1a"


class ReleaseGuard(Exception):
    pass


def safe_exec(argv, env=None, timeout=60):
    result = subprocess.run(argv, cwd=LIVE_ROOT, env=env, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=timeout)
    if result.returncode:
        # Never display argv or subprocess errors; a future tool may contain
        # private connection data or object store request metadata.
        raise ReleaseGuard("operator command failed: "+Path(argv[0]).name)
    return result.stdout


def enforce_source():
    if ROOT != LIVE_ROOT:
        raise ReleaseGuard("Goose17 Human apply must run from promoted real main")
    result = subprocess.run(["/usr/bin/git","-C",str(ROOT),"merge-base","--is-ancestor",
                             REVIEWED_SOURCE,"HEAD"],capture_output=True,timeout=8)
    if result.returncode:
        raise ReleaseGuard("reviewed Goose17 app not merged in real main")
    if safe_exec(["/usr/bin/git","-C",str(ROOT),"diff","--name-only",REVIEWED_SOURCE,
                  "HEAD","--","apps/","packages/","integrations/"], timeout=8).strip():
        raise ReleaseGuard("Go Server/SDK source drifted since real QA")
    if safe_exec(["/usr/bin/git","-C",str(ROOT),"status","--porcelain",
                  "--untracked-files=no"],timeout=8).strip():
        raise ReleaseGuard("main has uncommitted tracked changes")
    return safe_exec(["/usr/bin/git","-C",str(ROOT),"rev-parse","HEAD"],timeout=8).strip()


def preflight():
    # No Operator secrets, runtime_env() or HMAC access in this function.
    return guard.inspect()


def current_plist():
    path=prior.PLIST
    if path.is_symlink() or not path.is_file() or path.stat().st_uid!=os.getuid():
        raise ReleaseGuard("existing managed LaunchAgent plist is untrusted")
    raw=path.read_bytes()
    try: data=plistlib.loads(raw)
    except Exception as ex:raise ReleaseGuard("invalid LaunchAgent plist") from ex
    if (data.get("Label")!=LABEL or data.get("WorkingDirectory")!=str(LIVE_ROOT)
        or data.get("ProgramArguments")!=OLD_RUN
        or data.get("Umask")!=63 or data.get("RunAtLoad") is not True):
        raise ReleaseGuard("active Goose16 hotfix launcher does not match reviewed baseline")
    return raw


def operator_secret_precheck():
    # Human Operator APPLY ONLY. No key is logged or returned.
    goose16.enforce_file(goose16.OPS,goose16.EXPECTED_OPS_SHA)
    prior.valid_config(json.loads(prior.private_read(prior.CONFIG)))
    key=prior.private_read(Path.home()/".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}",key):
        raise ReleaseGuard("protected signing key format invalid")
    key=""
    token=prior.private_read(Path.home()/".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}",token):
        raise ReleaseGuard("protected scoped PI postrelease token unavailable")
    token=""


def prepare_private_material(old_plist):
    prior.protected_dir(RELEASE)
    goose16.enforce_file(CANDIDATE,EXPECTED_SERVER_SHA)
    goose16.enforce_file(goose16.GOOSE,EXPECTED_GOOSE_SHA)
    if NEW_BINARY.exists():
        goose16.enforce_file(NEW_BINARY,EXPECTED_SERVER_SHA)
    else:
        prior.safe_write(NEW_BINARY,CANDIDATE.read_bytes())
        NEW_BINARY.chmod(0o700)
        goose16.enforce_file(NEW_BINARY,EXPECTED_SERVER_SHA)
    if ORIGINAL_PLIST.exists():
        if prior.private_read(ORIGINAL_PLIST)!=old_plist:
            raise ReleaseGuard("original launchd plist changed since preflight")
    else:
        prior.safe_write(ORIGINAL_PLIST,old_plist)


def signed_backup_and_restore():
    # Human Operator APPLY ONLY. Only on the *stopped* Goose16 default service.
    key=prior.private_read(Path.home()/".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}",key):
        raise ReleaseGuard("invalid signing key")
    env=prior.runtime_env()
    env.update({"QUAL_BACKUP_HMAC_KEY":key,
                "PATH":"/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"})
    directory=prior.protected_dir(prior.STATE_DIR/"backups")
    out=directory/("qual30-goose17-"+datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
                   +"-"+os.urandom(4).hex())
    try:
        backup=json.loads(safe_exec([str(goose16.OPS),"backup","-out",str(out)],env,timeout=360))
        verify=json.loads(safe_exec([str(goose16.OPS),"verify","-path",str(out)],env,timeout=90))
        drill=json.loads(safe_exec([str(goose16.OPS),"restore-drill","-path",str(out)],env,timeout=360))
        if (backup.get("verified") is not True or backup.get("database")!="qual"
            or backup.get("table_count",0)<38 or backup.get("object_count",0)<1
            or verify.get("verified") is not True or verify.get("database")!="qual"
            or drill.get("verified") is not True or drill.get("postgresql_restored") is not True
            or drill.get("isolated_s3_restored") is not True or drill.get("scratch_cleaned") is not True
            or drill.get("objects_restored")!=backup.get("object_count")):
            raise ReleaseGuard("fresh signed PG/S3 backup and random restore drill failed")
        receipt={"at_utc":datetime.now(timezone.utc).isoformat(),
                 "private_backup_path":str(out),
                 "goose_before":16,"verified":True,"isolated_restored":True,
                 "tables":backup["table_count"],"s3_objects":backup["object_count"],
                 "objects_restored":drill["objects_restored"],"scratch_cleaned":True}
        prior.safe_write(BACKUP_RECEIPT,(json.dumps(receipt,indent=2,sort_keys=True)+"\n").encode())
        return receipt
    finally:
        key=""
        env.pop("QUAL_BACKUP_HMAC_KEY",None)


def real_goose_version():
    return goose16.check_goose_schema()  # private runtime config: Operator only


def require_goose17_physical():
    if real_goose_version()!=17:
        raise ReleaseGuard("Goose17 ledger not fully applied")
    sql=("SELECT to_regclass('qual.external_events') IS NOT NULL "
         "AND to_regclass('qual.external_events_work_timeline_idx') IS NOT NULL "
         "AND to_regclass('qual.external_events_thread_timeline_idx') IS NOT NULL "
         "AND EXISTS (SELECT 1 FROM pg_trigger tg "
         "WHERE tg.tgrelid=to_regclass('qual.external_events') "
         "AND tg.tgname='external_events_append_only' "
         "AND tg.tgenabled<>'D' "
         "AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()')) "
         "AND (SELECT count(*) FROM public.goose_db_version "
         "WHERE version_id=17 AND is_applied=true)=1")
    query=["/usr/local/bin/docker","compose","-f",str(ROOT/"deploy/compose.dev.yaml"),
           "exec","-T","postgres","psql","-U","qual","-d","qual","-X","-A","-t",
           "-v","ON_ERROR_STOP=1","-c",sql]
    if safe_exec(query,timeout=20).strip()!="t":
        raise ReleaseGuard("Goose17 ledger/table/index/immutable trigger mismatch")


def migrate_goose17():
    goose16.enforce_file(goose16.GOOSE,EXPECTED_GOOSE_SHA)
    if real_goose_version()!=16:
        raise ReleaseGuard("must migrate from exactly Goose16")
    env=prior.runtime_env()
    env.update({"GOOSE_DRIVER":"postgres","GOOSE_DBSTRING":env["DATABASE_URL"],
                "GOOSE_MIGRATION_DIR":str(ROOT/"apps/server/migrations")})
    # Secret DB connection only in env, never argv nor log.
    safe_exec([str(goose16.GOOSE),"up-to","17"],env,timeout=160)
    require_goose17_physical()
    print("OPERATOR_GOOSE17_APPLIED_REAL_LEDGER_AND_PROTECTION_PASS")


def new_plist_bytes():
    old=plistlib.loads(prior.private_read(ORIGINAL_PLIST))
    if old.get("Label")!=LABEL or old.get("WorkingDirectory")!=str(ROOT):
        raise ReleaseGuard("saved original LaunchAgent identity drift")
    old["ProgramArguments"]=NEW_RUN
    return plistlib.dumps(old)


def boot_pinned():
    prior.launchctl("bootstrap","gui/%d"%os.getuid(),str(prior.PLIST))
    for _ in range(110):
        pid=prior.listening_pid(18080)
        if pid and prior.matches_binary(pid,NEW_BINARY) and prior.http_status(
                "http://127.0.0.1:18080/readyz")[0]==200:
            return pid
        time.sleep(.2)
    raise ReleaseGuard("Goose17 pinned candidate failed managed readyz/binary check")


def postrelease_http():
    # Human Operator APPLY ONLY. No source event is ever sent to this database.
    token=prior.private_read(Path.home()/".config/qual/qual-pi-agent.token").decode().strip()
    if not re.fullmatch(r"qual_pat_[0-9a-f]{64}",token):
        raise ReleaseGuard("postrelease scoped token unavailable")
    base="http://127.0.0.1:18080/api/v1/projects/"+PROJECT
    statuses={}
    for name,path in (
        ("work",f"/work-items/{WORK_ITEM}"),
        ("context",f"/work-items/{WORK_ITEM}/context?section=overview"),
        ("threads",f"/work-items/{WORK_ITEM}/threads"),
        ("runs",f"/work-items/{WORK_ITEM}/agent-runs"),
        ("events",f"/work-items/{WORK_ITEM}/external-events?limit=5"),
    ):
        status,_=prior.http_status(base+path,token)
        if status!=200:raise ReleaseGuard("Goose17 authorized "+name+" read failed")
        statuses[name]=status
    for name,path in (
        ("events_anonymous",f"/work-items/{WORK_ITEM}/external-events"),
        ("context_anonymous",f"/work-items/{WORK_ITEM}/context"),
    ):
        status,_=prior.http_status(base+path)
        if status!=401:raise ReleaseGuard("unauthorized "+name+" accepted")
        statuses[name]=status
    invalid_project=("http://127.0.0.1:18080/api/v1/projects/"
                     "00000000-0000-4000-8000-000000000001"
                     f"/work-items/{WORK_ITEM}/external-events")
    status,_=prior.http_status(invalid_project,token)
    if status!=404:raise ReleaseGuard("cross-project Event timeline leaked")
    statuses["cross_project_event"]=status
    # Public unauthenticated POST proves the new ingest route exists yet
    # refuses work *before* any Event body can be accepted or persisted.
    probe=urllib.request.Request(base+"/external-events",method="POST",
                                 data=b"{}",headers={"Content-Type":"application/json"})
    try:
        with urllib.request.urlopen(probe,timeout=5) as response:
            post_code=response.status
    except urllib.error.HTTPError as error:
        post_code=error.code
    if post_code!=401:
        raise ReleaseGuard("Goose17 Event registration did not require authentication")
    statuses["anonymous_event_registration"]=post_code
    artifact=base+"/artifacts/"+prior.ARTIFACT+"/content"
    status,payload=prior.http_status(artifact,token)
    if status!=200 or hashlib.sha256(payload).hexdigest()!=prior.ARTIFACT_SHA:
        raise ReleaseGuard("real historical S3 artifact SHA failed after Goose17")
    statuses["prior_s3_artifact"]=200
    token=""
    return statuses


def restore_previous_without_migration():
    if prior.is_managed():
        prior.launchctl("bootout",JOB)
    prior.wait_port(18080,None,seconds=18)
    if real_goose_version()!=16:
        raise ReleaseGuard("cannot restore Goose16 server after Goose17 migration")
    prior.safe_write(prior.PLIST,prior.private_read(ORIGINAL_PLIST),private_parent=False)
    prior.launchctl("bootstrap","gui/%d"%os.getuid(),str(prior.PLIST))
    oldpid=prior.wait_port(18080,"any",seconds=25)
    if not prior.matches_binary(oldpid,OLD_BINARY):
        raise ReleaseGuard("old binary identity mismatch during safe pre-migration restore")
    if prior.http_status("http://127.0.0.1:18080/readyz")[0]!=200:
        raise ReleaseGuard("restored Goose16 service not ready")
    print("PRE_GOOSE17_ABORT_GOOSE16_SERVICE_RESTORED",oldpid)


def apply(explicit=False, reviewed_sha=None):
    # Human Operator ONLY. No Mac Pi use, unattended CI or user masquerading.
    if not explicit or reviewed_sha!=REVIEWED_SOURCE:
        raise ReleaseGuard("Human Operator must explicitly approve apply --apply --reviewed-sha")
    enforce_source()
    preflight()  # read-only, fails closed if default source/schema drifted
    goose16.enforce_file(CANDIDATE,EXPECTED_SERVER_SHA)
    goose16.enforce_file(OLD_BINARY,EXPECTED_PREVIOUS_SHA)
    old_plist=current_plist()
    operator_secret_precheck()  # before downtime: HMAC/config/token only Human
    prior.protected_dir(RELEASE)
    prepare_private_material(old_plist)
    lock=RELEASE/"exclusive-goose17-release.lock"
    with lock.open("a+b") as fp:
        try: fcntl.flock(fp.fileno(),fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError as ex:raise ReleaseGuard("another release holds the lock") from ex
        old_pid=prior.listening_pid(18080)
        if not old_pid or not prior.matches_binary(old_pid,OLD_BINARY) or current_plist()!=old_plist:
            raise ReleaseGuard("Goose16 current service changed during preflight")
        try:
            prior.launchctl("bootout",JOB)
            prior.wait_port(18080,None,seconds=20)
            goose16.require_no_other_database_writers()
            before=goose16.fingerprint()
            backup=signed_backup_and_restore()
            if before!=goose16.fingerprint():
                raise ReleaseGuard("business facts changed during signed backup")
            migrate_goose17()
            if before!=goose16.fingerprint():
                raise ReleaseGuard("Goose17 migration rewrote authoritative Work/Review/Test facts")
            prior.safe_write(prior.PLIST,new_plist_bytes(),private_parent=False)
            if subprocess.run(["/usr/bin/plutil","-lint",str(prior.PLIST)],
                              capture_output=True,timeout=8).returncode:
                raise ReleaseGuard("Goose17 LaunchAgent plist syntax invalid")
            pid=boot_pinned()
            require_goose17_physical()
            checks=postrelease_http()
            if real_goose_version()!=17 or prior.listening_pid(18080)!=pid:
                raise ReleaseGuard("postrelease Goose17 schema/PID drift")
            receipt={
                "status":"PASS","at_utc":datetime.now(timezone.utc).isoformat(),
                "source_commit":enforce_source(),"reviewed_base":REVIEWED_SOURCE,
                "prior_goose":16,"new_goose":17,
                "prior_server_sha256":EXPECTED_PREVIOUS_SHA,
                "new_server_sha256":EXPECTED_SERVER_SHA,
                "goose_cli_sha256":EXPECTED_GOOSE_SHA,
                "prior_pid":old_pid,"new_pid":pid,"launchd_label":LABEL,
                "real_signed_backup_and_restore":backup,
                "authoritative_history_fingerprints_preserved":True,
                "actual_http_statuses":checks,
                "default_new_permission_grants":False,
                "operator_human_requested":True,
                "independent_human_qual_review_implied":False,
                "no_destructive_down":True,"local_loopback_only":True,
            }
            prior.safe_write(RECEIPT,(json.dumps(receipt,ensure_ascii=False,indent=2,sort_keys=True)+"\n").encode())
            print("HUMAN_OPERATOR_GOOSE17_LOCAL_RELEASE_PASS",
                  "goose",17,"pid",pid,"ready",200,"backup_and_restore",True,
                  "source_sha",EXPECTED_SERVER_SHA[:12])
        except Exception:
            print("GOOSE17_OPERATOR_RELEASE_UNFINISHED_NO_DESTRUCTIVE_DOWN")
            try:
                if real_goose_version()==16:
                    restore_previous_without_migration()
                elif not prior.is_managed() and prior.listening_pid(18080) is None:
                    # After any real 17 write never boot the old Goose16 binary.
                    # Launch reviewed new binary only if its hard fail-closed
                    # startup/ready has a coherent schema; do NOT mark accepted.
                    prior.safe_write(prior.PLIST,new_plist_bytes(),private_parent=False)
                    pid=boot_pinned()
                    print("UNACCEPTED_GOOS17_FORWARD_RECOVERY_PID",pid)
            except Exception as ex:
                print("HUMAN_OPERATOR_MANUAL_FORWARD_REPAIR_REQUIRED",type(ex).__name__)
            raise


def run_server():
    goose16.enforce_file(NEW_BINARY,EXPECTED_SERVER_SHA)
    # Go's inspectSchema independently rejects missing ledger/index/trigger.
    env=prior.runtime_env(18080)
    os.chdir(LIVE_ROOT)
    os.execve(str(NEW_BINARY),[str(NEW_BINARY)],env)


def status():
    pid=prior.listening_pid(18080)
    current=None
    try:
        current=guard.shell("/usr/local/bin/docker","exec","qual-dev-postgres-1",
                            "psql","-U","qual","-d","qual","-At","-c",
                            "SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied",
                            timeout=18)
    except Exception:
        current="unknown"
    new=bool(pid and NEW_BINARY.is_file() and prior.matches_binary(pid,NEW_BINARY))
    old=bool(pid and OLD_BINARY.is_file() and prior.matches_binary(pid,OLD_BINARY))
    print("QUAL30_GOOSE17_STATUS","schema",current,"pid",pid,
          "new_binary_running",new,"old_goose16_binary_running",old,
          "ready_http",prior.http_status("http://127.0.0.1:18080/readyz")[0],
          "human_release_receipt",RECEIPT.exists())


def main():
    p=argparse.ArgumentParser()
    p.add_argument("action",choices=["inspect","apply","run","status"])
    p.add_argument("--apply",action="store_true")
    p.add_argument("--reviewed-sha")
    a=p.parse_args()
    try:
        if a.action=="inspect":
            details=preflight()
            print("QUAL30_OPERATOR_READONLY_GOOSE17_PRECHECK_PASS",
                  "candidate",details["candidate_server_sha256"][:12],
                  "real_schema",details["real_default_goose"],
                  "human_operator_pending",True)
        elif a.action=="apply":apply(a.apply,a.reviewed_sha)
        elif a.action=="run":run_server()
        else:status()
    except (ReleaseGuard,guard.PreflightGuard,prior.GuardError,goose16.Guard,
            OSError,ValueError,KeyError,subprocess.TimeoutExpired) as exc:
        safe=str(exc) if isinstance(exc,(ReleaseGuard,guard.PreflightGuard,
                                        prior.GuardError,goose16.Guard)) else "operation rejected"
        print("QUAL30_GOOSE17_HUMAN_RELEASE_GUARD",type(exc).__name__,safe,file=sys.stderr)
        raise SystemExit(3)


if __name__=="__main__":
    main()
