#!/usr/bin/env bash
# One-time, idempotent local QUAL self-dogfood provisioning.
# Creates credentials outside the repo; NEVER edits PI settings or starts PI.
set -euo pipefail
set +x
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT"
STATE="$HOME/.config/qual"
DB_URL='postgres://qual:qual_dev_only@127.0.0.1:15432/qual?sslmode=disable'
WORKSPACE='qual-self'
PROJECT='QUAL'
BASE='http://127.0.0.1:18080'

if [[ "${DATABASE_URL:-$DB_URL}" != "$DB_URL" ]]; then
  echo 'Refusing Phase 7 bootstrap against a non-default database. Use controlled localhost dev DB.' >&2
  exit 2
fi
if [[ -L "$STATE" ]];then echo 'Unsafe state directory symlink' >&2;exit 2;fi
mkdir -p -m 700 "$STATE"
chmod 700 "$STATE"
if ! mkdir -m 700 "$STATE/.bootstrap.lock" 2>/dev/null;then
  echo 'Another bootstrap may be in progress. Remove stale .bootstrap.lock only after review.' >&2
  exit 2
fi
INIT_FILE=''
ISSUE_FILE=''
cleanup(){
  if [[ -n "$INIT_FILE" ]];then rm -f -- "$INIT_FILE";fi
  if [[ -n "$ISSUE_FILE" ]];then rm -f -- "$ISSUE_FILE";fi
  rmdir "$STATE/.bootstrap.lock" 2>/dev/null || true
}
trap cleanup EXIT

if [[ "$(curl --max-time 5 -fsS "$BASE/readyz")" != '{"status":"ready"}' ]];then
  echo 'Qual local service is not ready. Start the Phase 6 Go server and S3 before provisioning.' >&2
  exit 3
fi

mkdir -p bin
(cd apps/server && go build -o ../../bin/qual-admin ./cmd/qual-admin &&
 go build -o ../../bin/qual ./cmd/qual &&
 go build -o ../../bin/qual-mcp ./cmd/qual-mcp)

db_query(){
  docker compose -f deploy/compose.dev.yaml exec -T postgres psql -U qual -d qual -Atc "$1"
}
owner_file="$STATE/qual-self-owner.token"
pi_file="$STATE/qual-pi-agent.token"
owner_count="$(db_query "SELECT count(*) FROM qual.workspaces WHERE slug='$WORKSPACE'")"
if [[ "$owner_count" != 0 && "$owner_count" != 1 ]];then
  echo 'Unexpected duplicate Qual Workspace records; abort' >&2;exit 3
fi

if [[ "$owner_count" == 0 ]];then
  if [[ -e "$owner_file" ]];then
    echo 'Owner token file exists while workspace is absent; refusing to overwrite.' >&2
    exit 3
  fi
  INIT_FILE="$(mktemp "$STATE/.qual-init.XXXXXX")"
  DATABASE_URL="$DB_URL" bin/qual-admin init \
    -workspace "$WORKSPACE" -project "$PROJECT" -admin 'Qual Local Owner' \
    > "$INIT_FILE"
  owner_token="$(awk '/^Token \(displayed once; store securely\):/{print $NF}' "$INIT_FILE")"
  if [[ ! "$owner_token" =~ ^qual_pat_[0-9a-f]{64}$ ]];then
    echo 'Admin bootstrap returned no valid token; stop and inspect local DB.' >&2;exit 3
  fi
  (set -C; printf '%s\n' "$owner_token" > "$owner_file")
  chmod 600 "$owner_file"
  unset owner_token
  rm -f -- "$INIT_FILE"; INIT_FILE=''
  echo 'Created Qual Local Owner in new workspace.'
elif [[ ! -f "$owner_file" && ! -f "$STATE/phase7-seed-state.json" ]];then
  echo 'Workspace exists but neither bootstrap Owner PAT nor prior seed state exists. Refusing recovery without Human review.' >&2
  exit 3
fi

project_id="$(db_query "SELECT p.id FROM qual.projects p JOIN qual.workspaces w ON w.id=p.workspace_id WHERE w.slug='$WORKSPACE' AND p.key='$PROJECT'")"
owner_id="$(db_query "SELECT r.id FROM qual.principals r JOIN qual.workspaces w ON w.id=r.workspace_id WHERE w.slug='$WORKSPACE' AND r.display_name='Qual Local Owner' AND r.kind='human' AND r.disabled_at IS NULL")"
if [[ ! "$project_id" =~ ^[0-9a-f-]{36}$ || ! "$owner_id" =~ ^[0-9a-f-]{36}$ ]];then
  echo 'Qual project/owner metadata missing, refusing to seed.' >&2;exit 3
fi

pi_principal_count="$(db_query "SELECT count(*) FROM qual.principals r JOIN qual.workspaces w ON w.id=r.workspace_id WHERE w.slug='$WORKSPACE' AND r.display_name='Local PI Agent' AND r.kind='agent' AND r.disabled_at IS NULL")"
if [[ "$pi_principal_count" == 0 ]];then
  if [[ -e "$pi_file" ]];then echo 'PI token file already exists; refusing overwrite' >&2;exit 3;fi
  ISSUE_FILE="$(mktemp "$STATE/.qual-pi-issue.XXXXXX")"
  DATABASE_URL="$DB_URL" bin/qual-admin issue \
   -workspace "$WORKSPACE" -project "$PROJECT" \
   -name 'Local PI Agent' -kind agent -role project_member \
   -scopes work:read,work:write,work:claim,test:read,test:write,artifact:read,artifact:write \
   -days 7 > "$ISSUE_FILE"
  pi_token="$(awk '/^Token \(displayed once; store securely\):/{print $NF}' "$ISSUE_FILE")"
  if [[ ! "$pi_token" =~ ^qual_pat_[0-9a-f]{64}$ ]];then
    echo 'Agent PAT issuance failed; review/revoke incomplete Principal before retrying.' >&2;exit 3
  fi
  (set -C; printf '%s\n' "$pi_token" > "$pi_file")
  chmod 600 "$pi_file"
  unset pi_token
  rm -f -- "$ISSUE_FILE"; ISSUE_FILE=''
  echo 'Created seven-day project-scoped Local PI Agent credential.'
elif [[ "$pi_principal_count" != 1 || ! -f "$pi_file" ]];then
  echo 'PI Principal or token file mismatch. Rotate or recover manually, never duplicate silently.' >&2
  exit 3
fi

for f in "$pi_file";do
  if [[ -L "$f" || "$(stat -f '%Lp' "$f")" != 600 || "$(stat -f '%Su' "$f")" != "$(id -un)" ]];then
    echo 'One or more pilot PAT files have unsafe ownership or permissions.' >&2;exit 3
  fi
done

if [[ -f "$owner_file" ]];then
  if [[ -L "$owner_file" || "$(stat -f '%Lp' "$owner_file")" != 600 || "$(stat -f '%Su' "$owner_file")" != "$(id -un)" ]];then
    echo 'Owner bootstrap credential file is unsafe' >&2;exit 3
  fi
  python3 scripts/phase7_seed.py --project-id "$project_id" --owner-token-file "$owner_file" \
    --output "$STATE/phase7-seed-state.json"
else
  echo 'Using already-seeded QUAL records; Owner bootstrap PAT previously revoked.'
fi

# Self-check the same real service through PI's Agent credential, without
# claiming PI itself was already configured or used.
scripts/qual-pi.sh cli projects > "$STATE/phase7-agent-projects.json"
python3 - "$STATE/phase7-agent-projects.json" "$project_id" <<'PY'
import json,sys
items=json.load(open(sys.argv[1])).get("items",[])
expected=sys.argv[2]
if len(items)!=1 or items[0].get("id")!=expected or items[0].get("key")!="QUAL":
  raise SystemExit("Agent scope mismatch: only QUAL must be visible")
print("Agent REST scoped project check: PASS (exactly QUAL).")
PY

python3 scripts/phase7_verify.py >/dev/null

# Limit PI's ability to obtain accidental Human admin privileges: the
# one-use bootstrap Owner PAT is not retained after seeding. Human still signs
# in with an offline five-minute browser code; no Agent may run admin tools.
if [[ -f "$owner_file" ]];then
  owner_credential="$(db_query "SELECT id FROM qual.credentials WHERE principal_id='$owner_id' AND revoked_at IS NULL ORDER BY expires_at DESC LIMIT 1")"
  if [[ ! "$owner_credential" =~ ^[0-9a-f-]{36}$ ]];then
    echo 'Could not locate Owner bootstrap credential to revoke' >&2;exit 3
  fi
  DATABASE_URL="$DB_URL" bin/qual-admin revoke -credential "$owner_credential" >/dev/null
  rm -f -- "$owner_file"
  echo 'One-use Human Owner bootstrap PAT revoked and its local file deleted.'
fi

printf 'Workspace: %s | Project: %s | Project UUID: %s\n' "$WORKSPACE" "$PROJECT" "$project_id"
printf 'Owner Principal UUID: %s\n' "$owner_id"
printf 'PI Agent PAT is private outside Git: %s\n' "$STATE/qual-pi-agent.token"
printf 'PI registration: pending user action; instructions in integrations/pi/README.md\n'
