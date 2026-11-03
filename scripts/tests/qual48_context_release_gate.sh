#!/usr/bin/env bash
# Real cmd/server process gate against isolated Goose13/14/15/16 databases.
# No default 'qual' DB, no real PI/Human credentials, no production server.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"
cd "$ROOT"
command -v docker >/dev/null
GOOSE="${QUAL_GOOSE_BIN:-/Users/mini/Coding/Qual/tmp/qual-governance/bin/goose}"
test -x "$GOOSE"
mkdir -p tmp/governance/qual48
DB="qual48_gate_$(python3 -c 'import secrets;print(secrets.token_hex(6))')"
[[ "$DB" =~ ^qual48_gate_[0-9a-f]{12}$ ]] || exit 3
BIN="$ROOT/tmp/governance/qual48/test-server"
LOG="$ROOT/tmp/governance/qual48/go-server.log"
PORT="$(python3 - <<'PY'
import socket
with socket.socket() as s:
    s.bind(('127.0.0.1',0))
    print(s.getsockname()[1])
PY
)"
PID=''
CREATED=0
TOKEN=''
WORK_TOKEN=''
cleanup() {
    if [[ -n "$PID" ]]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
        PID=''
    fi
    TOKEN=''
    WORK_TOKEN=''
    if [[ "$CREATED" = 1 ]]; then
        docker exec qual-dev-postgres-1 psql -U qual -d postgres -v ON_ERROR_STOP=1 \
            -c "DROP DATABASE $DB WITH (FORCE)" >/dev/null || echo 'FAILED_TO_CLEAN_SCRATCH_DB' >&2
    fi
}
trap cleanup EXIT
base=$(docker exec qual-dev-postgres-1 psql -U qual -d qual_phase5_test -Atc "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied")
[[ "$base" = 12 ]] || { echo "Scratch template has Goose $base, not 12"; exit 4; }
existing=$(docker exec qual-dev-postgres-1 psql -U qual -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname='$DB'")
[[ "$existing" = 0 ]] || exit 5
python3 -B scripts/ops/check_migration_sequence.py apps/server/migrations
(cd apps/server && go build -o "$BIN" ./cmd/server)
docker exec qual-dev-postgres-1 createdb -U qual -T qual_phase5_test "$DB"
CREATED=1
URL="postgres://qual:qual_dev_only@127.0.0.1:15432/$DB?sslmode=disable"
db() { docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 -Atc "$1"; }
migrate() {
    "$GOOSE" -dir "$ROOT/apps/server/migrations" postgres "$URL" up-to "$1" > "$ROOT/tmp/governance/qual48/goose-$1.log" 2>&1
    actual=$(db "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied")
    [[ "$actual" = "$1" ]] || { echo "Goose expected $1, got $actual";exit 7; }
}
migrate 13
record=$(db "SELECT w.id,w.project_id,p.workspace_id FROM qual.work_items w JOIN qual.projects p ON p.id=w.project_id LIMIT 1")
IFS='|' read -r ITEM PROJECT WS <<< "$record"
[[ "${#ITEM}" = 36 && "${#PROJECT}" = 36 && "${#WS}" = 36 ]] || exit 8

# Real AuthN/AuthZ is exercised with scratch-only, high-entropy PAT hashes.
create_scratch_identity() {
    local principal member credential scopes digest localtoken
    IFS='|' read -r principal member credential <<< "$(python3 -c 'import uuid;print("|".join(str(uuid.uuid4()) for _ in range(3)))')"
    localtoken="$(python3 -c 'import secrets;print("qual_pat_"+secrets.token_hex(32))')"
    digest="$(printf '%s' "$localtoken" | shasum -a 256 | awk '{print $1}')"
    if [[ "$1" = full ]]; then
        scopes="ARRAY['work:read','test:read']::text[]"
        TOKEN="$localtoken"
    else
        scopes="ARRAY['work:read']::text[]"
        WORK_TOKEN="$localtoken"
    fi
    db "INSERT INTO qual.principals(id,workspace_id,kind,display_name) VALUES('$principal','$WS','agent','Governance scratch identity');
    INSERT INTO qual.memberships(id,workspace_id,principal_id,project_id,role) VALUES('$member','$WS','$principal','$PROJECT','project_member');
    INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at) VALUES('$credential','$WS','$principal',decode('$digest','hex'),$scopes,now()+interval '15 minutes');" >/dev/null
    localtoken=''
}
create_scratch_identity full
create_scratch_identity workonly
BASE="http://127.0.0.1:$PORT/api/v1/projects/$PROJECT/work-items/$ITEM/context"
start_server() {
    DATABASE_URL="$URL" QUAL_HTTP_ADDR="127.0.0.1:$PORT" \
      QUAL_S3_ENDPOINT='http://127.0.0.1:18333' QUAL_S3_REGION='us-east-1' \
      QUAL_S3_ACCESS_KEY='qualdev' QUAL_S3_SECRET_KEY='qual_dev_only' QUAL_S3_BUCKET='qual-artifacts' \
      "$BIN" > "$LOG" 2>&1 &
    PID=$!
    for _ in $(seq 1 40); do
        if [[ "$(curl -s --max-time 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/readyz" || true)" = 200 ]]; then
            return 0
        fi
        if ! kill -0 "$PID" 2>/dev/null; then break; fi
        sleep 0.1
    done
    tail -12 "$LOG" >&2
    echo 'Scratch server could not reach readyz=200' >&2
    return 1
}
stop_server() {
    if [[ -n "$PID" ]]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
        PID=''
    fi
}
http() { curl -s --max-time 4 -o /dev/null -w '%{http_code}' "$@"; }
assert_status() {
    local got="$1" want="$2" label="$3"
    [[ "$got" = "$want" ]] || { echo "$label expected HTTP $want, got $got"; exit 11; }
    echo "$label=$got"
}
check_old() {
    local version="$1"
    start_server
    assert_status "$(http "$BASE?section=overview")" 404 "Goose$version anonymous Context"
    assert_status "$(http -H "Authorization: Bearer $TOKEN" "$BASE?section=overview")" 404 "Goose$version authorized Context"
    assert_status "$(http -H "Authorization: Bearer $TOKEN" "$BASE?section=attempts")" 404 "Goose$version indexed Context"
    assert_status "$(http "http://127.0.0.1:$PORT/readyz")" 200 "Goose$version legacy readyz"
    stop_server
}
check_new() {
    local version="$1"
    start_server
    assert_status "$(http "$BASE?section=overview")" 401 "Goose$version anonymous Context"
    assert_status "$(http -H "Authorization: Bearer $TOKEN" "$BASE?section=overview")" 200 "Goose$version authorized Context"
    assert_status "$(http -H "Authorization: Bearer $TOKEN" "$BASE?section=attempts")" 200 "Goose$version permitted attempts"
    assert_status "$(http -H "Authorization: Bearer $WORK_TOKEN" "$BASE?section=attempts")" 403 "Goose$version work-only attempts denial"
    assert_status "$(http -H "Authorization: Bearer $TOKEN" "$BASE?section=artifacts")" 403 "Goose$version artifact scope denial"
    assert_status "$(http "http://127.0.0.1:$PORT/readyz")" 200 "Goose$version readyz"
    stop_server
}
check_old 13
migrate 14
check_old 14
migrate 15
index=$(db "SELECT to_regclass('qual.test_attempts_project_ingested_context_idx') IS NOT NULL")
[[ "$index" = t ]] || exit 12
check_new 15
# An incoherent applied Goose15 is not allowed to look ready.
db 'DROP INDEX qual.test_attempts_project_ingested_context_idx' >/dev/null
DATABASE_URL="$URL" QUAL_HTTP_ADDR="127.0.0.1:$PORT" \
  QUAL_S3_ENDPOINT='http://127.0.0.1:18333' QUAL_S3_ACCESS_KEY='qualdev' \
  QUAL_S3_SECRET_KEY='qual_dev_only' QUAL_S3_BUCKET='qual-artifacts' \
  "$BIN" > "$LOG" 2>&1 &
PID=$!
for _ in $(seq 1 30); do
    if ! kill -0 "$PID" 2>/dev/null; then break; fi
    sleep 0.1
done
if kill -0 "$PID" 2>/dev/null; then
    echo 'Incomplete Goose15 server stayed running: FAIL' >&2
    exit 13
fi
wait "$PID" 2>/dev/null || true
PID=''
echo 'Goose15 missing required Context index: server startup rejected (PASS)'
db 'CREATE INDEX test_attempts_project_ingested_context_idx ON qual.test_attempts(project_id,ingested_at DESC,id DESC)' >/dev/null
check_new 15
migrate 16
check_new 16
echo 'QUAL-48 REAL-SERVER SCHEMA RELEASE GATE: PASS (Goose13,14,15,16; missing-index negative test)'
