#!/usr/bin/env python3
"""Fail closed on missing/duplicate Goose migrations before operator upgrade.

This is a read-only filesystem preflight. It does not run Goose, inspect
credentials, start Qual, or migrate a database.
"""

from __future__ import annotations

import argparse
from pathlib import Path
import re

MIGRATION_NAME = re.compile(r"^([0-9]{5})_[a-z0-9_]+\.sql$")

# Release-level ownership: allocated versions must not silently change when
# other worktrees merge. QUAL-30 ExternalEvent must use a future version.
FROZEN_MIGRATIONS = {
    14: "00014_external_thread_links.sql",   # QUAL-29
    15: "00015_work_context_attempts_indexes.sql",  # QUAL-27
    16: "00016_agent_runs.sql",               # QUAL-32
    17: "00017_external_events.sql",          # QUAL-30; never reuse 15
    18: "00018_task_authorization.sql",       # QUAL-55; projects remain legacy by default
    19: "00019_work_review_assignments.sql",  # QUAL-57; active reviewer relation
    20: "00020_bound_test_runs.sql",          # QUAL-58; immutable task binding
    21: "00021_historical_test_run_attributions.sql", # QUAL-65; source-only provenance
}


def check_sequence(root: Path) -> tuple[int, int]:
    if not root.is_dir():
        raise ValueError("migration directory not found")
    files = sorted(root.glob("*.sql"))
    if not files:
        raise ValueError("no SQL migration files")
    names: dict[int, str] = {}
    for path in files:
        match = MIGRATION_NAME.fullmatch(path.name)
        if match is None:
            raise ValueError("invalid Goose filename: " + path.name)
        number = int(match.group(1))
        if number in names:
            raise ValueError(
                f"duplicate Goose version {number}: {names[number]} and {path.name}"
            )
        names[number] = path.name
    max_version = max(names)
    missing = [number for number in range(1, max_version + 1) if number not in names]
    if missing:
        raise ValueError("missing Goose migrations: " + ", ".join(map(str, missing)))
    for number, expected in FROZEN_MIGRATIONS.items():
        if number <= max_version and names[number] != expected:
            raise ValueError(
                f"Goose {number} is allocated to {expected}, found {names[number]}"
            )
    return 1, max_version


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("migration_dir", type=Path, nargs="?", default=Path("apps/server/migrations"))
    args = parser.parse_args()
    try:
        first, last = check_sequence(args.migration_dir)
    except ValueError as error:
        parser.exit(2, f"migration sequence rejected: {error}\n")
    print(f"Goose filesystem sequence OK: {first}..{last}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
