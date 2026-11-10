#!/usr/bin/env bash
# Controlled local/CI Phase 6 disaster recovery rehearsal.
# Creates only random QUAL pilot DB and scratch S3 resources; never restores
# to the source DB, does not run tests/Agents/CI jobs on behalf of users.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PORT="${QUAL_DB_PORT:-15432}"
S3_PORT="${QUAL_S3_PORT:-18333}"
DRILL_HTTP_PORT="${QUAL_DRILL_HTTP_PORT:-18081}"
if [[ ! "$DRILL_HTTP_PORT" =~ ^[0-9]{4,5}$ ]] || (( 10#$DRILL_HTTP_PORT < 1024 || 10#$DRILL_HTTP_PORT > 65535 ));then
  echo "Invalid isolated recovery drill HTTP port" >&2
  exit 2
fi
# Refuse to reuse an already running Qual service: its /readyz might be 200
# even if our new server failed to bind, causing writes to the wrong instance.
python3 - "$DRILL_HTTP_PORT" <<'PYPORT'
import socket, sys
with socket.socket() as sock:
    try:
        sock.bind(("127.0.0.1", int(sys.argv[1])))
    except OSError:
        raise SystemExit("Recovery drill port is occupied; no scratch resources created")
PYPORT
DB="qual_pilot_$(openssl rand -hex 6)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/qual-phase6-drill.XXXXXXXX")"
SERVER_PID=""
cleanup(){
  if [[ -n "$SERVER_PID" ]];then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  # The name is script-generated, never taken from user input/backup.
  if [[ "$DB" =~ ^qual_pilot_[a-f0-9]{12}$ ]];then
    docker compose -f deploy/compose.dev.yaml exec -T postgres \
      dropdb -U qual --if-exists --force "$DB" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$TMP"
}
trap cleanup EXIT INT TERM

export DATABASE_URL="postgres://qual:qual_dev_only@127.0.0.1:${PORT}/${DB}?sslmode=disable"
export QUAL_S3_ENDPOINT="http://127.0.0.1:${S3_PORT}"
export QUAL_S3_ACCESS_KEY="${QUAL_S3_ACCESS_KEY:-qualdev}"
export QUAL_S3_SECRET_KEY="${QUAL_S3_SECRET_KEY:-qual_dev_only}"
export QUAL_S3_BUCKET="${QUAL_S3_BUCKET:-qual-artifacts}"
export QUAL_API_URL="http://127.0.0.1:${DRILL_HTTP_PORT}"
export QUAL_WEB_ORIGIN="http://127.0.0.1:4173"
export QUAL_BACKUP_HMAC_KEY="$(openssl rand -hex 32)"

docker compose -f deploy/compose.dev.yaml exec -T postgres \
  createdb -U qual "$DB"
# The isolated Phase 6 API fixture is still the public Goose17 service.
# Goose18..21 has its own scratch forward-only migration and provenance gate.
"${GOOSE:-goose}" -dir apps/server/migrations postgres "$DATABASE_URL" up-to 17 >"$TMP/migrate.log" 2>&1
(cd apps/server
 go build -o "$TMP/qual-server" ./cmd/server
 go build -o "$TMP/qual-admin" ./cmd/qual-admin
 go build -o "$TMP/qual-cli" ./cmd/qual
 go build -o "$TMP/qual-ops" ./cmd/qual-ops)

DATABASE_URL="$DATABASE_URL" "$TMP/qual-admin" init \
  -workspace "pilot-$(openssl rand -hex 4)" -project DR -admin 'Recovery Operator' \
  >"$TMP/bootstrap.txt"
export QUAL_TOKEN="$(awk '/^Token \(displayed once; store securely\):/{print $NF}' "$TMP/bootstrap.txt")"
if [[ ${#QUAL_TOKEN} -lt 35 ]];then echo 'Bootstrap token missing' >&2; exit 1;fi

QUAL_HTTP_ADDR="127.0.0.1:${DRILL_HTTP_PORT}" "$TMP/qual-server" >"$TMP/server.log" 2>&1 &
SERVER_PID=$!
READY=0
for i in $(seq 1 30);do
  if ! kill -0 "$SERVER_PID" 2>/dev/null;then
    echo "Isolated drill server exited before readiness" >&2
    exit 1
  fi
  if curl -fsS "$QUAL_API_URL/readyz" >"$TMP/readiness.json" 2>/dev/null;then READY=1; break;fi
  sleep 1
done
if [[ "$READY" -ne 1 ]];then echo 'Pilot server readiness failed' >&2;exit 1;fi
"$TMP/qual-cli" create -project DR -title 'Recovery drill fixture task' \
  -description 'Durable task fact, no runtime execution.' >"$TMP/task.json"
ITEM_ID="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["id"])' "$TMP/task.json")"
printf 'Reported external evidence, not executable data.\n' >"$TMP/receipt.txt"
"$TMP/qual-cli" evidence upload -project DR -target-kind work_item \
  -target "$ITEM_ID" -file "$TMP/receipt.txt" >"$TMP/artifact.json"
"$TMP/qual-cli" archive export -project DR >"$TMP/export.json"

"$TMP/qual-ops" backup -out "$TMP/backup" >"$TMP/backup-result.json"
"$TMP/qual-ops" verify -path "$TMP/backup" >"$TMP/verify-result.json"
"$TMP/qual-ops" restore-drill -path "$TMP/backup" >"$TMP/drill-result.json"

# Fail-closed negative test: cannot use a different signing key.
ORIGINAL="$QUAL_BACKUP_HMAC_KEY"
export QUAL_BACKUP_HMAC_KEY="$(openssl rand -hex 32)"
if "$TMP/qual-ops" verify -path "$TMP/backup" >"$TMP/negative-output" 2>&1;then
  echo 'Wrong backup signing key was accepted' >&2
  exit 1
fi
export QUAL_BACKUP_HMAC_KEY="$ORIGINAL"

# Remove source *test* object bytes after the isolated restore is proven.
# Restrict keys to this signed test manifest; the server's project DB is
# subsequently dropped by the trap. Credentials go via curl stdin, not argv.
DELETED=0
while IFS= read -r OBJECT_KEY; do
  [[ -n "$OBJECT_KEY" ]] || continue
  printf 'user = "%s:%s"\n' "$QUAL_S3_ACCESS_KEY" "$QUAL_S3_SECRET_KEY" | \
    curl --config - --silent --show-error --fail \
      --aws-sigv4 "aws:amz:${QUAL_S3_REGION:-us-east-1}:s3" \
      -X DELETE -o /dev/null \
      "$QUAL_S3_ENDPOINT/$QUAL_S3_BUCKET/$OBJECT_KEY"
  DELETED=$((DELETED + 1))
done < <(python3 - "$TMP/backup/manifest.json" <<'PYKEY'
import json,sys
for item in json.load(open(sys.argv[1]))["objects"]:
 print(item["key"])
PYKEY
)
printf '%d' "$DELETED" > "$TMP/deleted.txt"

python3 - "$TMP" <<'PY'
import json,sys
from pathlib import Path
root=Path(sys.argv[1])
def read(name):
 return json.loads((root/name).read_text())
a=read("backup-result.json")
b=read("verify-result.json")
c=read("drill-result.json")
assert a["verified"] and b["verified"], "backup failed verification"
assert a["table_count"] >= 25 and a["object_count"] >= 2, "fixture not included"
assert c["verified"] and c["postgresql_restored"] and c["isolated_s3_restored"], "restore failed"
assert c["scratch_cleaned"] and c["tables_compared"]==a["table_count"], "scratch not cleaned"
assert c["objects_restored"]==a["object_count"], "restored bytes missing"
assert int((root/"deleted.txt").read_text())==a["object_count"], "source fixture objects not cleaned"
print(json.dumps({"phase":"6","backup_verified":True,
 "tables_compared":c["tables_compared"],"objects_restored":c["objects_restored"],
 "isolated_db_and_s3_verified":True,"scratch_cleaned":True,
 "invalid_HMAC_rejected":True,"source_fixture_objects_cleaned":True}))
PY
