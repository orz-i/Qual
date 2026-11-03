#!/usr/bin/env python3
"""QUAL-28 local CLI/MCP client-only rollout, no server, DB, or secrets.

The v2 CLI/MCP binaries are built from a separately reviewed source commit.
This operation never changes a launchd job, PAT, HMAC key, or running process.
Existing MCP stdio sessions still require the user to reload/reconnect.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import secrets
import stat
import subprocess
import sys
from datetime import datetime, timezone
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[2]
BIN_DIR = ROOT / "bin"
PRIVATE_DIR = ROOT / "tmp/phase10/qual28-client-rollout"
SOURCE_COMMIT = "cbf7f19bb915129d3a9e7c5fd26bd013ca7b30f2"
REVIEWED_MAIN = "2e7f687b8921b4696feb4a44fbd5f63ef5f828b9"
SOURCE_CANDIDATES = ROOT / "tmp/worktrees/qual28-client-release/tmp/qual28-client-release"
BINARIES = {
    "qual": {
        "old": "aeebdb83968c15b5342e3991839a8de8b5af1561889c1bb907c8780b3b4decb7",
        "new": "f73ac59c827d4e87decb1a62b822d94bb83d8fc918f04091dc1f86e361675120",
    },
    "qual-mcp": {
        "old": "27501b2dfb504574facf41fb0f1da568f12e85a0d6f737388a2406d1e63e3b9c",
        "new": "4aef8bdfd2a76e295017b598ad55d13620d56676b54e350da09f3bb5f01a2b8d",
    },
}
REPORT = PRIVATE_DIR / "installed-receipt.json"


class ReleaseGuard(Exception):
    pass


def sha(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def owned_regular(path: Path) -> bool:
    try:
        mode = path.lstat()
    except FileNotFoundError:
        return False
    return stat.S_ISREG(mode.st_mode) and not stat.S_ISLNK(mode.st_mode) and mode.st_uid == os.getuid()


def safe_private_dir(path: Path) -> None:
    if path.is_symlink():
        raise ReleaseGuard("private release directory cannot be symlink")
    if not path.exists():
        path.mkdir(mode=0o700, parents=True)
    mode = path.stat()
    if not stat.S_ISDIR(mode.st_mode) or mode.st_uid != os.getuid() or stat.S_IMODE(mode.st_mode) != 0o700:
        raise ReleaseGuard("private release directory ownership/mode invalid")


def check_source() -> str:
    tip = subprocess.check_output(["git", "-C", str(ROOT), "rev-parse", "HEAD"], text=True).strip()
    for parent in (SOURCE_COMMIT, REVIEWED_MAIN):
        if subprocess.run(["git", "-C", str(ROOT), "merge-base", "--is-ancestor", parent, tip],
                          capture_output=True).returncode:
            raise ReleaseGuard("main lacks reviewed client source or audited main baseline")
    changed = subprocess.check_output(["git", "-C", str(ROOT), "diff", "--name-only",
                                      SOURCE_COMMIT, tip, "--",
                                      "apps/server/cmd/qual", "apps/server/cmd/qual-mcp",
                                      "apps/server/internal/integration/mcpserver",
                                      "apps/server/internal/workcontext"], text=True)
    if changed.strip():
        raise ReleaseGuard("reviewed client source changed after verified compile")
    if subprocess.check_output(["git", "-C", str(ROOT), "status", "--porcelain",
                                "--untracked-files=no"], text=True).strip():
        raise ReleaseGuard("main has uncommitted tracked edits")
    return tip


def inspect() -> dict:
    tip = check_source()
    if BIN_DIR.is_symlink() or not BIN_DIR.is_dir() or BIN_DIR.stat().st_uid != os.getuid():
        raise ReleaseGuard("untrusted bin directory")
    with urlopen("http://127.0.0.1:18080/readyz", timeout=4) as r:
        if r.status != 200:
            raise ReleaseGuard("default Qual backend is not ready")
    outcome = {}
    for name, hashes in BINARIES.items():
        source = SOURCE_CANDIDATES / name
        installed = BIN_DIR / name
        if not owned_regular(source) or sha(source) != hashes["new"]:
            raise ReleaseGuard(f"candidate {name} missing, symlinked or not reviewed")
        if not owned_regular(installed):
            raise ReleaseGuard(f"installed {name} missing, symlinked or unowned")
        old = sha(installed)
        if old not in (hashes["old"], hashes["new"]):
            raise ReleaseGuard(f"installed {name} differs from old and approved new SHA")
        outcome[name] = {"current_sha256": old, "new_sha256": hashes["new"],
                         "already_new": old == hashes["new"]}
    return {"current_main": tip, "no_server_migration": True, "ready_http": 200, "binaries": outcome}


def _atomic_copy(source: Path, target: Path, mode: int, expected_sha: str):
    if not owned_regular(source) or sha(source) != expected_sha:
        raise ReleaseGuard(f"source failed SHA integrity: {source.name}")
    tmp = target.parent / (".qual28-release-" + secrets.token_hex(10))
    if tmp.exists():
        raise ReleaseGuard("temporary staging path already exists")
    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, mode)
        with os.fdopen(fd, "wb") as out, source.open("rb") as inp:
            while chunk := inp.read(1024 * 1024):
                out.write(chunk)
            out.flush()
            os.fsync(out.fileno())
        os.chmod(tmp, mode)
        if sha(tmp) != expected_sha:
            raise ReleaseGuard("staged copy SHA mismatch")
        os.replace(tmp, target)
        if sha(target) != expected_sha:
            raise ReleaseGuard("installed binary SHA mismatch")
    finally:
        if tmp.exists():
            tmp.unlink()


def apply(approved: bool) -> dict:
    if not approved:
        raise ReleaseGuard("client-only rollout requires explicit --apply")
    safe_private_dir(PRIVATE_DIR)
    lock = PRIVATE_DIR / "exclusive.lock"
    if lock.is_symlink():
        raise ReleaseGuard("refuse linked release lock")
    with lock.open("a+b") as fp:
        try:
            fcntl.flock(fp.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as e:
            raise ReleaseGuard("another client rollout is active") from e
        before = inspect()
        for name, hashes in BINARIES.items():
            saved = PRIVATE_DIR / ("previous-" + name)
            installed = BIN_DIR / name
            if saved.exists() and (not owned_regular(saved) or sha(saved) != hashes["old"]):
                raise ReleaseGuard(f"untrusted backup for {name}")
            if sha(installed) == hashes["new"] and not saved.exists():
                raise ReleaseGuard(f"{name} new binary lacks reviewed rollback backup")
        installed_this_turn = []
        try:
            for name, hashes in BINARIES.items():
                source = SOURCE_CANDIDATES / name
                target = BIN_DIR / name
                old = PRIVATE_DIR / ("previous-" + name)
                if not old.exists():
                    _atomic_copy(target, old, 0o600, hashes["old"])
                if sha(target) == hashes["new"]:
                    continue
                installed_this_turn.append(name)
                _atomic_copy(source, target, 0o755, hashes["new"])
            after = inspect()
        except Exception:
            for name in reversed(installed_this_turn):
                hashes = BINARIES[name]
                if sha(BIN_DIR / name) == hashes["new"]:
                    _atomic_copy(PRIVATE_DIR / ("previous-" + name), BIN_DIR / name, 0o755, hashes["old"])
            raise
        receipt = {
            "at_utc": datetime.now(timezone.utc).isoformat(),
            "reviewed_source_commit": SOURCE_COMMIT,
            "main_commit": after["current_main"],
            "ready_http": 200,
            "previous_binaries": {x: y["old"] for x, y in BINARIES.items()},
            "installed_binaries": {x: y["new"] for x, y in BINARIES.items()},
            "prior_mcp_processes_automatically_reloaded": False,
            "existing_qual_server_or_database_changed": False,
            "new_sessions_must_reconnect_to_see_optional_mcp_v2_fields": True,
            "operator_hmac_or_pat_read": False,
        }
        raw = (json.dumps(receipt, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode()
        if REPORT.is_symlink():
            raise ReleaseGuard("unsafe report target")
        temp = PRIVATE_DIR / (".receipt-" + secrets.token_hex(5))
        fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "wb") as f:
            f.write(raw)
            f.flush()
            os.fsync(f.fileno())
        os.replace(temp, REPORT)
        return receipt


def rollback(approved: bool) -> dict:
    if not approved:
        raise ReleaseGuard("client-only rollback requires explicit --apply")
    safe_private_dir(PRIVATE_DIR)
    with (PRIVATE_DIR / "exclusive.lock").open("a+b") as fp:
        try:
            fcntl.flock(fp.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as e:
            raise ReleaseGuard("another client rollout holds the lock") from e
        before = inspect()
        for name, hashes in BINARIES.items():
            previous = PRIVATE_DIR / ("previous-" + name)
            if not owned_regular(previous) or sha(previous) != hashes["old"]:
                raise ReleaseGuard(f"verified old client binary missing: {name}")
        for name, hashes in BINARIES.items():
            if sha(BIN_DIR / name) == hashes["new"]:
                _atomic_copy(PRIVATE_DIR / ("previous-" + name), BIN_DIR / name,
                             0o755, hashes["old"])
        after = inspect()
        if any(v["already_new"] for v in after["binaries"].values()):
            raise ReleaseGuard("binary rollback was not complete")
        return {"main": before["current_main"], "old_clients_restored": True,
                "default_service_and_database_unchanged": True}


def main():
    p = argparse.ArgumentParser()
    p.add_argument("action", choices=["inspect", "apply", "status", "rollback"])
    p.add_argument("--apply", action="store_true")
    opts = p.parse_args()
    try:
        if opts.action == "apply":
            details = apply(opts.apply)
            print("QUAL28_CLIENT_INSTALL_PASS", "main", details["main_commit"][:12],
                  "cli_sha", details["installed_binaries"]["qual"][:12],
                  "mcp_sha", details["installed_binaries"]["qual-mcp"][:12])
        elif opts.action == "rollback":
            details = rollback(opts.apply)
            print("QUAL28_CLIENT_ROLLBACK_PASS", "main", details["main"][:12])
        else:
            data = inspect()
            print("QUAL28_CLIENT_" + opts.action.upper(),
                  "main", data["current_main"][:12],
                  "ready_http", data["ready_http"],
                  *(f"{name}={meta['current_sha256'][:12]}" for name, meta in data["binaries"].items()))
    except (ReleaseGuard, OSError, subprocess.CalledProcessError) as e:
        print("QUAL28_CLIENT_RELEASE_GUARD", type(e).__name__,
              str(e) if isinstance(e, ReleaseGuard) else "release preflight failed",
              file=sys.stderr)
        raise SystemExit(3)


if __name__ == "__main__":
    main()
