#!/usr/bin/env python3
"""Local-only Qual macOS LaunchAgent runtime and guarded Goose12 cutover.

No secrets in plist, command arguments, Git, or report. Runtime credentials live
in a user-owned 0600 file outside this repository. The dev defaults are taken
from *checked-in* compose.dev.yaml, never another process's environment.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
LABEL = "local.qual.go-server"
NEW = ROOT / "tmp/phase10/qual22-release/qual-server-go1273"
OLD = ROOT / "tmp/phase10/qual19-server-go1273"
NEW_SHA = "975daa0424da15b885d0b35b4bcb79c8d5edf15de1331d42f798b424eec279c9"
OLD_SHA = "bd4ead46c0f5ce51300ff32e7ad3495cb5f8f5a3cf951a36f995a7cce0c5a595"
PROJECT = "e6560273-31f1-4c3f-b690-3b4586977700"
ARTIFACT = "276dedc9-da37-48de-9d3c-5458ca13176a"
ARTIFACT_SHA = "e08d450c5d3a181cd9e316e6ee2269359fb1c83729b93854e4ab6570dd8ebbed"
CONFIG = Path.home() / ".config/qual/qual-server-launchd.json"
PLIST = Path.home() / "Library/LaunchAgents/local.qual.go-server.plist"
LOG_DIR = Path.home() / "Library/Logs/Qual"
STATE_DIR = Path.home() / ".local/state/qual"
ALLOWED = {
    "DATABASE_URL", "QUAL_HTTP_ADDR", "QUAL_WEB_ORIGIN",
    "QUAL_S3_ENDPOINT", "QUAL_S3_REGION", "QUAL_S3_BUCKET",
    "QUAL_S3_ACCESS_KEY", "QUAL_S3_SECRET_KEY",
    "QUAL_WEBHOOK_ENCRYPTION_KEY", "QUAL_WEBHOOK_ALLOWED_HOSTS",
    "QUAL_METRICS_TOKEN",
}


class GuardError(Exception):
    pass


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for part in iter(lambda: f.read(1024 * 1024), b""):
            h.update(part)
    return h.hexdigest()


def protected_dir(path):
    path = Path(path)
    if path.is_symlink():
        raise GuardError("Private directory is a symlink")
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    info = path.lstat()
    if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
        raise GuardError("Private directory requires owner-only 0700 permissions")
    return path


def safe_write(path, data, private_parent=True):
    path = Path(path)
    if private_parent:
        protected_dir(path.parent)
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        info = path.parent.lstat()
        if path.parent.is_symlink() or info.st_uid != os.getuid() or (stat.S_IMODE(info.st_mode) & 0o022):
            raise GuardError("LaunchAgents directory must be owner-owned and not writable by other users")
    if path.is_symlink():
        raise GuardError("Refusing to replace a symlink")
    dest = path.with_name(path.name + ".tmp." + str(os.getpid()))
    fd = os.open(str(dest), os.O_CREAT | os.O_EXCL | os.O_WRONLY | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(dest, path)
    finally:
        if dest.exists():
            dest.unlink()


def private_read(path):
    path = Path(path)
    if path.is_symlink() or not path.exists():
        raise GuardError("Private runtime config missing or unsafe")
    protected_dir(path.parent)
    fd = os.open(str(path), os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size > 16384:
            raise GuardError("Runtime config must be a 0600 regular owner-owned file under 16 KiB")
        with os.fdopen(fd, "rb") as f:
            fd = -1
            return f.read()
    finally:
        if fd != -1:
            os.close(fd)


def compose_default(name):
    text = (ROOT / "deploy/compose.dev.yaml").read_text()
    match = re.search(r"\$\{" + re.escape(name) + r":-([^}]+)\}", text)
    if not match:
        raise GuardError("Checked-in dev Compose default missing for " + name)
    return match.group(1)


def valid_config(obj):
    if not isinstance(obj, dict) or obj.get("profile") != "local-dev-loopback" or not isinstance(obj.get("env"), dict):
        raise GuardError("Invalid runtime config shape")
    env = obj["env"]
    if set(env) - ALLOWED or any(not isinstance(v, str) for v in env.values()):
        raise GuardError("Unexpected runtime config fields")
    if env.get("QUAL_HTTP_ADDR") != "127.0.0.1:18080":
        raise GuardError("Server must bind local loopback port 18080")
    url = urllib.parse.urlsplit(env.get("DATABASE_URL", ""))
    if url.scheme not in ("postgres", "postgresql") or url.hostname != "127.0.0.1" or url.port != 15432 or url.path != "/qual" or url.username != "qual" or not url.password:
        raise GuardError("Runtime DB connection must target local Qual dev database")
    s3 = urllib.parse.urlsplit(env.get("QUAL_S3_ENDPOINT", ""))
    if s3.scheme != "http" or s3.hostname != "127.0.0.1" or s3.port != 18333 or s3.username or s3.password:
        raise GuardError("Runtime S3 must target dev loopback container")
    if not env.get("QUAL_S3_ACCESS_KEY") or not env.get("QUAL_S3_SECRET_KEY"):
        raise GuardError("Missing dev object store credentials")
    if env.get("QUAL_S3_BUCKET") != "qual-artifacts" or env.get("QUAL_WEB_ORIGIN") != "http://127.0.0.1:4173":
        raise GuardError("Unexpected dev web origin or bucket")
    for name in ("QUAL_WEBHOOK_ENCRYPTION_KEY", "QUAL_METRICS_TOKEN"):
        if env.get(name):
            raise GuardError(name + " requires separate operator-preserved configuration; dev fallback cannot silently replace it")
    return env


def runtime_env(port=18080):
    env = valid_config(json.loads(private_read(CONFIG)))
    result = {
        "HOME": str(Path.home()),
        "PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
        "LANG": "en_US.UTF-8",
        "TMPDIR": os.environ.get("TMPDIR", "/tmp"),
    }
    result.update(env)
    result["QUAL_HTTP_ADDR"] = f"127.0.0.1:{port}"
    return result


def require_binary(path, pinned_sha):
    if not path.is_file() or path.is_symlink() or path.stat().st_uid != os.getuid():
        raise GuardError("Release executable missing, linked or not owned by current user")
    if sha(path) != pinned_sha:
        raise GuardError("Release binary does not match reviewed SHA256")
    return str(path)


def run_server(old=False, port=18080):
    path = OLD if old else NEW
    pinned = OLD_SHA if old else NEW_SHA
    exe = require_binary(path, pinned)
    os.chdir(ROOT)
    os.execve(exe, [exe], runtime_env(port))


def prepare():
    protected_dir(CONFIG.parent)
    protected_dir(STATE_DIR)
    protected_dir(LOG_DIR)
    if CONFIG.exists():
        valid_config(json.loads(private_read(CONFIG)))
        print("private_config=existing_validated")
    else:
        pw = urllib.parse.quote(compose_default("QUAL_DB_PASSWORD"), safe="")
        env = {
            "DATABASE_URL": f"postgresql://qual:{pw}@127.0.0.1:15432/qual?sslmode=disable",
            "QUAL_HTTP_ADDR": "127.0.0.1:18080",
            "QUAL_WEB_ORIGIN": "http://127.0.0.1:4173",
            "QUAL_S3_ENDPOINT": "http://127.0.0.1:18333",
            "QUAL_S3_REGION": "us-east-1",
            "QUAL_S3_BUCKET": "qual-artifacts",
            "QUAL_S3_ACCESS_KEY": compose_default("QUAL_S3_ACCESS_KEY"),
            "QUAL_S3_SECRET_KEY": compose_default("QUAL_S3_SECRET_KEY"),
        }
        value = {"profile": "local-dev-loopback", "source": "checked-in Compose dev defaults, preflight validated", "env": env}
        valid_config(value)
        safe_write(CONFIG, (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode())
        print("private_config=created_dev_only_0600")
    require_binary(NEW, NEW_SHA)
    require_binary(OLD, OLD_SHA)
    PLIST.parent.mkdir(parents=True, exist_ok=True)
    if PLIST.parent.is_symlink() or PLIST.parent.stat().st_uid != os.getuid():
        raise GuardError("LaunchAgents directory is not user-owned")
    for name in ("server.stdout.log", "server.stderr.log"):
        path = LOG_DIR / name
        if path.is_symlink():
            raise GuardError("Refusing symlinked log path")
        fd = os.open(str(path), os.O_WRONLY | os.O_APPEND | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0), 0o600)
        os.close(fd)
        info = path.stat()
        if stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.getuid():
            raise GuardError("Existing logs must be owner-only 0600")
    data = {
        "Label": LABEL,
        "ProgramArguments": ["/usr/bin/python3", str(Path(__file__).resolve()), "run"],
        "WorkingDirectory": str(ROOT),
        "RunAtLoad": True,
        "KeepAlive": {"SuccessfulExit": False},
        "ThrottleInterval": 10,
        "Umask": 63,
        "StandardOutPath": str(LOG_DIR / "server.stdout.log"),
        "StandardErrorPath": str(LOG_DIR / "server.stderr.log"),
    }
    safe_write(PLIST, plistlib.dumps(data), private_parent=False)
    result = subprocess.run(["/usr/bin/plutil", "-lint", str(PLIST)], capture_output=True, text=True)
    if result.returncode:
        raise GuardError("Generated LaunchAgent plist did not validate")
    print("launchagent_plist=staged_validated_no_secrets")
    print("reviewed_release_sha_match=true")


def listening_pid(port):
    res = subprocess.run(["/usr/sbin/lsof", "-nP", f"-iTCP:{port}", "-sTCP:LISTEN", "-t"], capture_output=True, text=True)
    pids = [int(x) for x in res.stdout.split() if x.isdigit()]
    if len(pids) > 1:
        raise GuardError("Multiple processes listen on guarded port")
    return pids[0] if pids else None


def http_status(url, token=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers, method="GET"), timeout=2.5) as response:
            return response.status, response.read(5 * 1024 * 1024)
    except urllib.error.HTTPError as e:
        return e.code, b""
    except Exception:
        return 0, b""


def verify_read_only(port, expected_pid):
    if listening_pid(port) != expected_pid:
        raise GuardError("Preflight listener not owned by launched binary")
    health, _ = http_status(f"http://127.0.0.1:{port}/healthz")
    ready, _ = http_status(f"http://127.0.0.1:{port}/readyz")
    if health != 200 or ready != 200:
        raise GuardError("Candidate health and readiness probe failed")
    tmp = STATE_DIR / ("review-smoke-" + str(os.getpid()) + ".json")
    try:
        smoke = subprocess.run(
            [sys.executable, "-B", str(ROOT / "scripts/qual-review-lane-smoke.py"),
             "--base-url", f"http://127.0.0.1:{port}", "--build-id", NEW_SHA,
             "--require-resubmission", "QUAL-20", "--result", str(tmp)],
            cwd=ROOT, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=18)
        if smoke.returncode or not tmp.exists():
            raise GuardError("Real scoped PI review-queue regression did not pass")
        result = json.loads(tmp.read_text())
        if not result.get("passed") or result.get("work_item_count", 0) < 1:
            raise GuardError("Insufficient real scoped review-queue results")
        count = result["work_item_count"]
    finally:
        if tmp.exists():tmp.unlink()
    # Ensure S3 is really enabled, not only DB Ready, using existing immutable Ready artifact.
    token_path = Path.home() / ".config/qual/qual-pi-agent.token"
    token = private_read(token_path).decode().strip()
    if not re.fullmatch("qual_pat_[0-9a-f]{64}", token):
        raise GuardError("Scoped PI credential invalid")
    url = f"http://127.0.0.1:{port}/api/v1/projects/{PROJECT}/artifacts/{ARTIFACT}/content"
    status, data = http_status(url, token)
    if status != 200 or len(data) != 3125 or hashlib.sha256(data).hexdigest() != ARTIFACT_SHA:
        raise GuardError("S3 Ready artifact was not readable or hash-identical")
    status, _ = http_status(f"http://127.0.0.1:{port}/api/v1/projects/{PROJECT}/review-queue?limit=2")
    if status not in (401, 403):
        raise GuardError("Anonymous review queue was not refused")
    return count


def wait_port(port, wanted_pid=None, seconds=15):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        pid = listening_pid(port)
        if wanted_pid is None and pid is None:
            return None
        if wanted_pid == "any" and pid is not None:
            return pid
        if isinstance(wanted_pid, int) and pid == wanted_pid:
            return pid
        time.sleep(.2)
    raise GuardError("Listener transition failed or timed out")


def preflight():
    protected_dir(STATE_DIR)
    if listening_pid(18181) is not None:
        raise GuardError("Isolated preflight port 18181 is already occupied")
    require_binary(NEW, NEW_SHA)
    runtime_env(18181)  # Validate config before launching anything.
    with tempfile.TemporaryDirectory(prefix="qual-preflight-", dir=str(STATE_DIR)) as work:
        with open(Path(work) / "candidate.log", "wb") as log:
            proc = subprocess.Popen(["/usr/bin/python3", str(Path(__file__).resolve()), "run", "--port", "18181"],
                                    cwd=str(ROOT), stdin=subprocess.DEVNULL, stdout=log,
                                    stderr=subprocess.STDOUT, start_new_session=True)
            try:
                wait_port(18181, proc.pid)
                count = verify_read_only(18181, proc.pid)
                print(f"preflight=PASS candidate_pid_verified=true ready=200 scoped_items={count} s3_sha_match=true anonymous_denied=true")
                safe_write(STATE_DIR / "qual23-last-preflight.json",
                           (json.dumps({"passed": True, "sha256": NEW_SHA, "scoped_review_items": count,
                                        "at_unix": int(time.time()), "port": 18181}, indent=2) + "\n").encode())
                return count
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    try:proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait(timeout=5)
                try:wait_port(18181, None, seconds=4)
                except GuardError:raise GuardError("Preflight process not fully stopped")


def label_address():
    return f"gui/{os.getuid()}/{LABEL}"


def launchctl(*args):
    r = subprocess.run(["/bin/launchctl", *args], capture_output=True, text=True, timeout=15)
    if r.returncode:
        raise GuardError("launchctl operation failed: " + args[0])
    return r


def is_managed():
    return subprocess.run(["/bin/launchctl", "print", label_address()],
                          capture_output=True, timeout=10).returncode == 0


def matches_binary(pid, path):
    res = subprocess.run(["/usr/sbin/lsof", "-nP", "-a", "-p", str(pid), "-d", "txt", "-Fn"],
                         capture_output=True, text=True)
    return str(path.resolve()) in res.stdout


def rollback_old():
    # Used only after new job has been booted out and 18080 is free.
    if listening_pid(18080) is not None:
        raise GuardError("Cannot roll back while another process occupies 18080")
    require_binary(OLD, OLD_SHA)
    protected_dir(LOG_DIR)
    out = LOG_DIR / "rollback-old.log"
    with open(out, "ab", buffering=0) as log:
        p = subprocess.Popen(["/usr/bin/python3", str(Path(__file__).resolve()), "run", "--old"],
                             cwd=str(ROOT), stdin=subprocess.DEVNULL, stdout=log,
                             stderr=subprocess.STDOUT, start_new_session=True)
    try:
        wait_port(18080, p.pid, seconds=12)
        if http_status("http://127.0.0.1:18080/readyz")[0] != 200:
            raise GuardError("Old server rollback readiness failed")
        print(f"rollback_old=PASS pid={p.pid} readiness=200")
        return p.pid
    except Exception:
        if p.poll() is None:p.terminate()
        raise


def goose_version():
    cmd = ["/usr/local/bin/docker", "compose", "-f", str(ROOT / "deploy/compose.dev.yaml"),
           "exec", "-T", "postgres", "psql", "-U", "qual", "-d", "qual", "-X", "-A", "-t",
           "-c", "SELECT COALESCE(MAX(version_id),0) FROM public.goose_db_version WHERE is_applied=true;"]
    if not Path(cmd[0]).is_file():
        raise GuardError("Controlled Docker CLI not available")
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=12)
    if result.returncode != 0 or result.stdout.strip() != "12":
        raise GuardError("Default database schema is not reviewed Goose12")
    return 12


def verified_operator_backup():
    receipt = json.loads(private_read(STATE_DIR / "qual23-backup-receipt.json"))
    if receipt.get("signed_verify") != "PASS" or receipt.get("isolated_restore_drill") != "PASS" or receipt.get("goose_schema") != 12:
        raise GuardError("Signed backup and scratch restore drill must pass before cutover")
    when = datetime.fromisoformat(receipt["backup_created_utc"])
    if when.tzinfo is None or (datetime.now(timezone.utc) - when).total_seconds() > 6*3600:
        raise GuardError("Backup is too old for current local cutover")
    backup = Path(receipt["backup_dir"])
    if backup.is_symlink() or not backup.is_dir() or backup.parent != STATE_DIR / "backups":
        raise GuardError("Trusted private backup directory missing")
    key = private_read(Path.home() / ".config/qual/qual-backup-hmac.key").decode().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", key):
        raise GuardError("Signed backup verification key unavailable")
    ops = ROOT / "tmp/phase10/qual23-release/qual-ops"
    if not ops.is_file():
        raise GuardError("Local qual-ops verification binary missing")
    env = runtime_env()
    env.update({"QUAL_BACKUP_HMAC_KEY": key,
                "PATH": "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"})
    result = subprocess.run([str(ops), "verify", "-path", str(backup)], env=env,
                            cwd=str(ROOT), capture_output=True, text=True, timeout=30)
    if result.returncode != 0:
        raise GuardError("Fresh backup signature and manifest verification failed")
    meta = json.loads(result.stdout)
    if not meta.get("verified") or meta.get("object_count", 0) < 1:
        raise GuardError("Backup does not have verified private objects")
    print("precutover_signed_backup_verified=true isolated_restore_drill=PASS")


def cutover():
    goose_version()
    verified_operator_backup()
    if is_managed():
        raise GuardError("Existing launchd job must be inspected before replacing")
    if not PLIST.exists():
        raise GuardError("LaunchAgent not prepared")
    preflight()  # Fresh live data proof before any service stop.
    old_pid = listening_pid(18080)
    if not old_pid or not matches_binary(old_pid, OLD):
        raise GuardError("Running old server is not the reviewed binary; abort")
    if sha(OLD) != OLD_SHA:
        raise GuardError("Rollback binary SHA differs from reviewed manifest")
    print(f"precutover_old_pid_verified={old_pid} old_binary_sha_match=true")
    # Only after all guards have passed do we request graceful termination.
    os.kill(old_pid, signal.SIGTERM)
    try:
        wait_port(18080, None, seconds=16)
    except GuardError:
        raise GuardError("Old server did not free 18080; no forced kill or new server start")
    booted = False
    try:
        launchctl("bootstrap", f"gui/{os.getuid()}", str(PLIST))
        booted = True
        new_pid = wait_port(18080, "any", seconds=16)
        if not matches_binary(new_pid, NEW):
            raise GuardError("launchd listener is not the reviewed Go binary")
        count = verify_read_only(18080, new_pid)
        goose_version()  # Reconfirm no accidental migration.
        evidence = {
            "outcome": "PASS", "old_pid": old_pid, "new_pid": new_pid,
            "old_binary_sha256": OLD_SHA, "new_binary_sha256": NEW_SHA,
            "goose_schema_unchanged": 12, "launchd_label": LABEL,
            "review_queue_items": count, "ready_http": 200,
            "review_resubmission_contract": "PASS", "s3_artifact_sha_match": True,
            "anonymous_queue_denied": True, "completed_unix": int(time.time()),
            "credentials_logged": False,
        }
        safe_write(STATE_DIR / "qual23-live-cutover.json", (json.dumps(evidence, indent=2) + "\n").encode())
        print(f"cutover=PASS new_pid={new_pid} ready=200 review_queue=PASS old_binary_preserved=true")
    except Exception as ex:
        print("cutover_validation_failed_attempting_rollback=true")
        if booted:
            try:launchctl("bootout", label_address())
            except GuardError:pass
        try:wait_port(18080, None, seconds=12)
        except GuardError:
            raise GuardError("Rollback blocked: unknown listener remains on port 18080") from ex
        rollback_old()
        raise GuardError("New server failed validation; old binary restored") from ex


def operator_rollback():
    if not is_managed():
        raise GuardError("Managed Qual Go job is not registered; refusing ambiguous rollback")
    pid = listening_pid(18080)
    if pid is not None and not matches_binary(pid, NEW):
        raise GuardError("Listener is not the reviewed managed release; refusing rollback")
    launchctl("bootout", label_address())
    wait_port(18080, None, seconds=16)
    rollback_old()
    print("operator_rollback=PASS old_binary_restored=true")


def status():
    pid = listening_pid(18080)
    print("port18080_pid",pid,"launchagent_registered",is_managed(),
          "candidate_sha_valid",NEW.is_file() and sha(NEW)==NEW_SHA,
          "ready",http_status("http://127.0.0.1:18080/readyz")[0])
    if pid:
        print("listening_release", "new" if matches_binary(pid,NEW) else "old" if matches_binary(pid,OLD) else "other")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["prepare", "run", "preflight", "cutover", "rollback", "status"])
    parser.add_argument("--old", action="store_true", help="Rollback to pinned previous binary")
    parser.add_argument("--port", type=int, choices=[18080, 18181], default=18080)
    options = parser.parse_args()
    os.umask(0o077)
    try:
        if options.action == "prepare":prepare()
        elif options.action == "run":run_server(old=options.old, port=options.port)
        elif options.action == "preflight":preflight()
        elif options.action == "cutover":cutover()
        elif options.action == "rollback":operator_rollback()
        else:status()
    except (GuardError, OSError, ValueError, subprocess.TimeoutExpired) as error:
        # All error paths deliberately avoid values from the confidential env file.
        print("qual-launchd ERROR:",type(error).__name__, str(error),file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    main()
