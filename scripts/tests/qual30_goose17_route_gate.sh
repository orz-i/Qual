#!/usr/bin/env bash
# QUAL-30: only randomly named disposable DB and temporary loopback service.
# NEVER migrates the managed/default "qual" database or launchd process.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
cd "$ROOT"
GOOSE_BIN="/Users/mini/Coding/Qual/tmp/qual-governance/bin/goose"
[ -x "$GOOSE_BIN" ]
mkdir -p tmp/qual30/route-gate
DB="qual30_route_gate_$(python3 -B -c 'import secrets;print(secrets.token_hex(5))')"
[[ "$DB" =~ ^qual30_route_gate_[0-9a-f]{10}$ ]] || exit 3
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname='$DB'")" = 0 ] || exit 4
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d qual -Atc 'SELECT max(version_id) FROM public.goose_db_version WHERE is_applied')" = 16 ] || exit 5
BIN="$ROOT/tmp/qual30/route-gate/candidate-server"
# A Human release can separately precompile and hash-pin the *exact* binary.
# Refuse arbitrary file paths so the scratch gate cannot become an execution
# trampoline for unreviewed content.
if [ -n "${QUAL30_CANDIDATE_BIN:-}" ]; then
  [ "$QUAL30_CANDIDATE_BIN" = "$ROOT/tmp/goose17-release/qual-server" ] || exit 18
  [ -f "$QUAL30_CANDIDATE_BIN" ] && [ ! -L "$QUAL30_CANDIDATE_BIN" ] || exit 19
  [ "$(shasum -a 256 "$QUAL30_CANDIDATE_BIN" | awk '{print $1}')" = 'acc0060d1f7bd16538e6d94e026560c8af8eb741ef9868972139454b5ddbd710' ] || exit 20
  BIN="$QUAL30_CANDIDATE_BIN"
fi
PORT="$(python3 -B - <<'PY'
import socket
with socket.socket() as s:
    s.bind(('127.0.0.1',0))
    print(s.getsockname()[1])
PY
)"
PID=''
CREATED=0
cleanup() {
  if [ -n "$PID" ]; then kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; PID=''; fi
  if [ "$CREATED" = 1 ]; then
    docker exec qual-dev-postgres-1 psql -U qual -d postgres -v ON_ERROR_STOP=1 \
      -c "DROP DATABASE $DB WITH (FORCE)" >/dev/null || echo 'ERROR: SCRATCH DB CLEANUP FAILED' >&2
  fi
}
trap cleanup EXIT

if [ -z "${QUAL30_CANDIDATE_BIN:-}" ]; then
  (cd apps/server && go build -trimpath -o "$BIN" ./cmd/server)
fi
docker exec qual-dev-postgres-1 createdb -U qual -T qual_phase5_test "$DB"
CREATED=1
URL="postgres://qual:qual_dev_only@127.0.0.1:15432/$DB?sslmode=disable"
export DATABASE_URL="$URL" QUAL_HTTP_ADDR="127.0.0.1:$PORT"
export QUAL_S3_ENDPOINT="http://127.0.0.1:18333" QUAL_S3_REGION="us-east-1"
export QUAL_S3_BUCKET="qual-artifacts" QUAL_S3_ACCESS_KEY="qualdev" QUAL_S3_SECRET_KEY="qual_dev_only"
base="http://127.0.0.1:$PORT"
event_url="$base/api/v1/projects/00000000-0000-4000-8000-000000000001/external-events"

stop_server(){
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
    PID=''
  fi
}
start_server(){
  "$BIN" > tmp/qual30/route-gate/candidate.log 2>&1 &
  PID=$!
  for i in $(seq 1 80); do
    if ! kill -0 "$PID" 2>/dev/null; then echo 'Candidate aborted before ready' >&2; return 1; fi
    if [ "$(curl --silent --max-time 1 -o /dev/null -w '%{http_code}' "$base/readyz" || true)" = 200 ]; then
      return 0
    fi
    sleep .15
  done
  echo 'Candidate readiness timeout' >&2
  return 1
}
assert_code(){
  local label="$1" uri="$2" want="$3"
  local code
  code="$(curl --silent --max-time 3 -o /dev/null -w '%{http_code}' "$uri")"
  if [ "$code" != "$want" ]; then echo "FAILED $label HTTP=$code expected=$want" >&2; exit 11; fi
  echo "PASS $label HTTP$code"
}
"$GOOSE_BIN" -dir apps/server/migrations postgres "$URL" up-to 16 > tmp/qual30/route-gate/goose16.log 2>&1
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -Atc 'SELECT max(version_id) FROM public.goose_db_version WHERE is_applied')" = 16 ]
start_server
assert_code "Goose16 pre-Event legacy readyz" "$base/readyz" 200
assert_code "Goose16 Event route disabled" "$event_url" 404
stop_server
"$GOOSE_BIN" -dir apps/server/migrations postgres "$URL" up-to 17 > tmp/qual30/route-gate/goose17.log 2>&1
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -Atc 'SELECT max(version_id) FROM public.goose_db_version WHERE is_applied')" = 17 ]
start_server
assert_code "Goose17 GET on POST-only Event route" "$event_url" 405
# Verify real Event registration route requires AuthN with anonymous POST.
posted="$(curl --silent --max-time 3 -X POST -H 'Content-Type: application/json' \
  -d '{}' -o /dev/null -w '%{http_code}' "$event_url")"
[ "$posted" = 401 ] || { echo "Goose17 POST expected401 observed$posted" >&2; exit 12; }
echo 'PASS Goose17 Event POST requires real Bearer HTTP401'
stop_server

# Negative partial migration: missing physical index, disabled history
# protection trigger, and absent table all fail closed even with Goose17 ledger.
reject_partial(){
  local label="$1"
  "$BIN" > "tmp/qual30/route-gate/invalid-${label}.log" 2>&1 &
  PID=$!
  for i in $(seq 1 50); do
    if ! kill -0 "$PID" 2>/dev/null; then break; fi
    sleep .1
  done
  if kill -0 "$PID" 2>/dev/null; then
    echo "FAIL Goose17 missing ${label} allowed startup" >&2
    exit 13
  fi
  wait "$PID" 2>/dev/null || true
  PID=''
  echo "PASS Goose17 missing ${label} rejected on startup"
}
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 \
  -c 'DROP INDEX qual.external_events_work_timeline_idx' >/dev/null
reject_partial work-timeline-index
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 \
  -c 'CREATE INDEX external_events_work_timeline_idx ON qual.external_events(project_id,work_item_id,received_at DESC,id DESC)' >/dev/null
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 \
  -c 'ALTER TABLE qual.external_events DISABLE TRIGGER external_events_append_only' >/dev/null
reject_partial append-only-trigger
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 \
  -c 'ALTER TABLE qual.external_events ENABLE TRIGGER external_events_append_only' >/dev/null
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 \
  -c 'DROP TABLE qual.external_events' >/dev/null
reject_partial event-table
echo 'QUAL30 REAL GOOSE16->17 EVENT ROUTE GATE PASS'
