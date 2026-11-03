#!/usr/bin/env bash
# PI adapter for the Qual local dogfood Project.
# Credential is never read into PI config or written to stdout/log files.
set -euo pipefail
set +x
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
MODE="${1:-}"
if [[ "$MODE" != "mcp" && "$MODE" != "cli" ]]; then
  printf 'Usage: scripts/qual-pi.sh mcp | cli [qual command flags]\n' >&2
  exit 2
fi
shift

# Never inherit a Human/Admin token accidentally left in the shell.
# For this controlled Mac-only pilot always use the specifically issued PI
# Agent credential, stored outside Git with owner-only file permissions.
unset QUAL_TOKEN
TOKEN_DIR="$HOME/.config/qual"
TOKEN_FILE="$TOKEN_DIR/qual-pi-agent.token"
if [[ ! -d "$TOKEN_DIR" || -L "$TOKEN_DIR" || "$(stat -f '%Lp' "$TOKEN_DIR" 2>/dev/null)" != "700" || "$(stat -f '%Su' "$TOKEN_DIR" 2>/dev/null)" != "$(id -un)" ]]; then
  echo 'Qual PI PAT directory must be a private owner-only 0700 directory (not a symlink)' >&2
  exit 3
fi
if [[ ! -f "$TOKEN_FILE" || -L "$TOKEN_FILE" ]]; then
  echo 'Qual PI PAT missing or unsafe: follow docs/pilot/phase7-qual-dogfood.md' >&2
  exit 3
fi
if [[ "$(stat -f '%Lp' "$TOKEN_FILE")" != "600" || "$(stat -f '%Su' "$TOKEN_FILE")" != "$(id -un)" ]]; then
  echo 'Qual PI PAT must be a private owner-only 0600 file (not a symlink)' >&2
  exit 3
fi
IFS= read -r QUAL_TOKEN < "$TOKEN_FILE" || true
export QUAL_TOKEN

if [[ ! "$QUAL_TOKEN" =~ ^qual_pat_[0-9a-f]{64}$ ]]; then
  echo 'Qual PI PAT invalid or missing (the token value is not logged)' >&2
  exit 3
fi

# PI's CLI / MCP must only send this pilot PAT to the local Qual service.
export QUAL_API_URL="http://127.0.0.1:18080"

case "$MODE" in
  mcp)
    if [[ $# -ne 0 ]]; then
      echo 'MCP command accepts no user arguments' >&2
      exit 2
    fi
    exec "$ROOT/bin/qual-mcp"
    ;;
  cli) exec "$ROOT/bin/qual" "$@" ;;
esac
