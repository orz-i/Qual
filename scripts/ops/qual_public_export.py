#!/usr/bin/env python3
"""Create an intentionally clean public source mirror; never publish local history.

A public repository does NOT receive internal Human screenshots, local pilot
evidence, operator reports, or the 357 local commit ancestors. A separate
public/main branch receives a snapshot commit, with stable parent history on
later exports. Only explicitly named protected source paths are rewritten.
No remote push occurs here; that is a distinct reviewed step.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
PUBLIC_REF = "refs/heads/public/main"
EXCLUDED = ("docs/design", "docs/pilot", "docs/engineering", "docs/public")
TEMPLATES = {
    "README.md": "docs/public/README.md",
    "AGENTS.md": "docs/public/AGENTS.md",
    "docs/README.md": "docs/public/docs-index.md",
}
BLOCKED = {
    "qual_human_login": rb"qlc_[0-9a-f]{40,}",
    "qual_pat": rb"qual_pat_[0-9a-f]{40,}",
    "github_pat": rb"(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{35,})",
    "openai_key": rb"sk-(?:proj-)?[A-Za-z0-9_-]{25,}",
    "aws_key": rb"AKIA[0-9A-Z]{16}",
    "private_key": rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----",
    "operator_hmac": rb"QUAL_BACKUP_HMAC_KEY\s*[=:]\s*[0-9a-f]{64}",
    "webhook_secret": rb"QUAL_WEBHOOK_ENCRYPTION_KEY\s*[=:]\s*[0-9a-f]{64}",
}
BIN_SUFFIXES = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".sqlite"}


class ExportGuard(Exception):
    pass


def git(*args: str, env=None, input_bytes: bytes | None = None) -> bytes:
    proc = subprocess.run(
        ["git", "-C", str(ROOT), *args],
        input=input_bytes, capture_output=True, env=env,
        timeout=50,
    )
    if proc.returncode:
        raise ExportGuard("git " + args[0] + " returned nonzero")
    return proc.stdout


def verify_root() -> str:
    head = git("rev-parse", "HEAD").decode().strip()
    branch = git("symbolic-ref", "--quiet", "--short", "HEAD").decode().strip()
    if branch != "main":
        raise ExportGuard("export only from clean local main")
    if git("status", "--porcelain", "--untracked-files=no").strip():
        raise ExportGuard("tracked local source is dirty")
    if not all((ROOT / src).is_file() for src in TEMPLATES.values()):
        raise ExportGuard("public README/AGENTS templates are missing")
    return head


def staged_public_tree(source: str, index_env: dict[str, str]) -> tuple[str, int]:
    git("read-tree", source, env=index_env)
    git("rm", "--cached", "-r", "-q", "--ignore-unmatch", "--", *EXCLUDED, env=index_env)
    for path, template in TEMPLATES.items():
        raw = (ROOT / template).read_text(encoding="utf-8")
        if "{{SOURCE_COMMIT}}" not in raw:
            raise ExportGuard("template source revision placeholder missing: " + template)
        raw = raw.replace("{{SOURCE_COMMIT}}", source)
        object_sha = git("hash-object", "-w", "--stdin", input_bytes=raw.encode()).decode().strip()
        git("update-index", "--add", "--cacheinfo", "100644", object_sha, path, env=index_env)
    tree = git("write-tree", env=index_env).decode().strip()
    entries = git("ls-tree", "-r", "-z", tree).split(b"\x00")
    count = 0
    for entry in entries:
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        parts = meta.decode().split()
        if len(parts) != 3 or parts[1] != "blob":
            raise ExportGuard("non-blob public export path rejected")
        path = name.decode("utf-8")
        if any(path == p or path.startswith(p + "/") for p in EXCLUDED):
            raise ExportGuard("internal document escaped exclusion: " + path)
        if Path(path).suffix.lower() in BIN_SUFFIXES:
            raise ExportGuard("binary capture escaped exclusion: " + path)
        if parts[0] not in ("100644", "100755"):
            raise ExportGuard("symlink or unsupported mode rejected: " + path)
        data = git("cat-file", "blob", parts[2])
        for label, pattern in BLOCKED.items():
            if re.search(pattern, data):
                raise ExportGuard("possible " + label + " found in public candidate path " + path)
        count += 1
    if count < 200:
        raise ExportGuard("export unexpectedly small; refusing incomplete snapshot")
    return tree, count


def export(apply: bool) -> dict:
    source = verify_root()
    old = None
    try:
        old = git("rev-parse", "--verify", PUBLIC_REF).decode().strip()
    except ExportGuard:
        pass
    (ROOT / "tmp").mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="qual-public-index-", dir=ROOT / "tmp") as folder:
        env = dict(os.environ, GIT_INDEX_FILE=str(Path(folder) / "public.index"))
        tree, count = staged_public_tree(source, env)
    if old:
        old_tree = git("rev-parse", old + "^{tree}").decode().strip()
        if tree == old_tree:
            return {"status": "unchanged", "source_sha": source, "public_sha": old, "files": count}
    if not apply:
        return {"status": "dry_run", "source_sha": source, "tree_sha": tree, "files": count,
                "old_public_sha": old, "no_remote_write": True}
    msg = (
        "chore(public): publish screened Qual source snapshot\n\n"
        "Local source SHA: " + source + "\n"
        "No original commit ancestry, internal Human screenshots, or pilot docs.\n"
    )
    args = ["commit-tree", tree]
    if old:
        args += ["-p", old]
    args += ["-F", "-"]
    author = dict(os.environ,
                  GIT_AUTHOR_NAME="Qual Public Snapshot",
                  GIT_AUTHOR_EMAIL="public-snapshot@users.noreply.github.com",
                  GIT_COMMITTER_NAME="Qual Public Snapshot",
                  GIT_COMMITTER_EMAIL="public-snapshot@users.noreply.github.com")
    new = git(*args, env=author, input_bytes=msg.encode()).decode().strip()
    old_value = old or "0" * 40
    git("update-ref", PUBLIC_REF, new, old_value)
    return {"status": "created", "source_sha": source, "public_sha": new,
            "files": count, "private_ancestors_exported": False,
            "sensitive_screenshot_paths_exported": False, "remote_push_performed": False}


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--apply", action="store_true",
                   help="Create/update only refs/heads/public/main; does not push")
    args = p.parse_args()
    try:
        result = export(args.apply)
        print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    except (ExportGuard, ValueError, UnicodeError, OSError) as exc:
        print("PUBLIC_EXPORT_REFUSED", type(exc).__name__, str(exc))
        raise SystemExit(3)


if __name__ == "__main__":
    main()
