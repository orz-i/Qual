#!/usr/bin/env bash
# CI-only Goose18..21 forward compatibility and historical attribution proof.
# This NEVER connects to or migrates the official local `qual` database.
set -euo pipefail
umask 077
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$ROOT"
: "${GITHUB_RUN_ID:?unique CI run ID required}"
: "${GITHUB_RUN_ATTEMPT:?unique CI attempt required}"
[[ "$GITHUB_RUN_ID" =~ ^[0-9]{1,25}$ ]] || exit 2
[[ "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]{1,5}$ ]] || exit 2
DB="qual21_forward_ci_${GITHUB_RUN_ID}_${GITHUB_RUN_ATTEMPT}"
[[ "$DB" =~ ^qual21_forward_ci_[0-9]+_[0-9]+$ ]] || exit 2
GOOSE_BIN="${GOOSE:-$(go env GOPATH)/bin/goose}"
[[ -x "$GOOSE_BIN" ]] || { echo 'Missing executable Goose binary' >&2; exit 3; }

pg() {
  docker compose -f deploy/compose.dev.yaml exec -T postgres \
    psql -U qual -d postgres -X -A -t -v ON_ERROR_STOP=1 "$@"
}
psql_db() {
  docker compose -f deploy/compose.dev.yaml exec -T postgres \
    psql -U qual -d "$DB" -X -A -t -v ON_ERROR_STOP=1 "$@"
}
created=0
cleanup() {
  result=$?
  trap - EXIT
  if [[ "$created" = 1 ]]; then
    if ! pg -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null; then
      echo 'SCRATCH_GOOSE21_CLEANUP_FAILED' >&2
      exit 1
    fi
    echo 'SCRATCH_GOOSE21_DATABASE_REMOVED=true'
  fi
  exit "$result"
}
trap cleanup EXIT
[[ "$(pg -c "SELECT COUNT(*) FROM pg_database WHERE datname='$DB'")" = 0 ]] || {
  echo 'Disposable database already exists; refuse reuse' >&2
  exit 4
}
pg -c "CREATE DATABASE $DB" >/dev/null
created=1
SCRATCH="postgres://qual:qual_dev_only@127.0.0.1:15432/$DB?sslmode=disable"
# Baseline: migrate existing schema without editing old source facts.
"$GOOSE_BIN" -dir apps/server/migrations postgres "$SCRATCH" up-to 20 >/dev/null
[[ "$(psql_db -c 'SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied')" = 20 ]] || exit 5

# Only disposable synthetic IDs. Seed three unbound pre-Goose21 Runs:
# one uniquely cited, one with competing Task citations, one uncited.
# None has an actual TestAttempt status and no source row is ever rewritten.
psql_db >/dev/null <<'SQL'
INSERT INTO qual.workspaces(id,slug,name)
 VALUES('11111111-1111-4111-8111-111111111111','ci-forward','CI forward migration');
INSERT INTO qual.projects(id,workspace_id,key,name)
 VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','FWD','Synthetic forward upgrade');
INSERT INTO qual.principals(id,workspace_id,kind,display_name)
 VALUES('33333333-3333-4333-8333-333333333333','11111111-1111-4111-8111-111111111111','agent','Synthetic CI Agent');
INSERT INTO qual.credentials(id,workspace_id,principal_id,token_sha256,scopes,expires_at)
 VALUES('44444444-4444-4444-8444-444444444444','11111111-1111-4111-8111-111111111111',
        '33333333-3333-4333-8333-333333333333',decode(repeat('ab',32),'hex'),
        ARRAY['work:read'],now()+interval '1 day');
INSERT INTO qual.work_items(id,workspace_id,project_id,number,key,kind,title,created_by)
 VALUES
 ('55555555-5555-4555-8555-555555555555','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',1,'FWD-1','task','Unique historical', '33333333-3333-4333-8333-333333333333'),
 ('55555555-5555-4555-8555-555555555556','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',2,'FWD-2','task','Competing historical','33333333-3333-4333-8333-333333333333');
INSERT INTO qual.test_runs(id,project_id,name,source_principal_id)
 VALUES
 ('66666666-6666-4666-8666-666666666666','22222222-2222-4222-8222-222222222222','Unique old run','33333333-3333-4333-8333-333333333333'),
 ('88888888-8888-4888-8888-888888888888','22222222-2222-4222-8222-222222222222','Ambiguous old run','33333333-3333-4333-8333-333333333333'),
 ('99999999-9999-4999-8999-999999999999','22222222-2222-4222-8222-222222222222','Uncited old run','33333333-3333-4333-8333-333333333333');
INSERT INTO qual.work_role_entries(id,workspace_id,project_id,work_item_id,actor_id,credential_id,
 capacity,outcome,summary,reference_kind,reference_value,work_version,work_status,request_id)
 VALUES
 ('77777777-7777-4777-8777-777777777771','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','55555555-5555-4555-8555-555555555555','33333333-3333-4333-8333-333333333333','44444444-4444-4444-8444-444444444444','tester','observed','Only old source reference A','test_run','66666666-6666-4666-8666-666666666666',1,'backlog','ci-unique'),
 ('77777777-7777-4777-8777-777777777772','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','55555555-5555-4555-8555-555555555555','33333333-3333-4333-8333-333333333333','44444444-4444-4444-8444-444444444444','tester','observed','Conflicting source claim A','test_run','88888888-8888-4888-8888-888888888888',1,'backlog','ci-ambiguous-a'),
 ('77777777-7777-4777-8777-777777777773','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','55555555-5555-4555-8555-555555555556','33333333-3333-4333-8333-333333333333','44444444-4444-4444-8444-444444444444','tester','observed','Conflicting source claim B','test_run','88888888-8888-4888-8888-888888888888',1,'backlog','ci-ambiguous-b');
SQL
BEFORE="$(psql_db -c "SELECT string_agg(id::text || ':' || coalesce(work_item_id::text,'NULL'), ',' ORDER BY id) FROM qual.test_runs")"
[[ "$(psql_db -c 'SELECT COUNT(*) FROM qual.test_runs')" = 3 ]] || exit 6
[[ "$(psql_db -c 'SELECT COUNT(*) FROM qual.project_task_authorization_policies')" = 0 ]] || exit 6
"$GOOSE_BIN" -dir apps/server/migrations postgres "$SCRATCH" up-to 21 >/dev/null
[[ "$(psql_db -c 'SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied')" = 21 ]] || exit 7
AFTER="$(psql_db -c "SELECT string_agg(id::text || ':' || coalesce(work_item_id::text,'NULL'), ',' ORDER BY id) FROM qual.test_runs")"
[[ "$AFTER" = "$BEFORE" ]] || { echo 'Original unbound TestRuns were rewritten' >&2; exit 8; }
[[ "$(psql_db -c 'SELECT count(*) FROM qual.historical_test_run_attributions')" = 1 ]] || exit 9
[[ "$(psql_db -c "SELECT work_item_id::text FROM qual.historical_test_run_attributions WHERE test_run_id='66666666-6666-4666-8666-666666666666'")" = '55555555-5555-4555-8555-555555555555' ]] || exit 9
[[ "$(psql_db -c "SELECT role_entry_ids[1]::text FROM qual.historical_test_run_attributions WHERE test_run_id='66666666-6666-4666-8666-666666666666'")" = '77777777-7777-4777-8777-777777777771' ]] || exit 9
[[ "$(psql_db -c "SELECT COUNT(*) FROM qual.historical_test_run_attributions WHERE test_run_id IN ('88888888-8888-4888-8888-888888888888','99999999-9999-4999-8999-999999999999')")" = 0 ]] || exit 9
[[ "$(psql_db -c 'SELECT count(*) FROM qual.project_task_authorization_policies')" = 0 ]] || { echo 'Migration auto-activated task authorization' >&2; exit 10; }
# Actual non-destructive forward-only invariants: old TestRun, appended
# provenance, and schema Down must all refuse destructive changes.
if psql_db -c "UPDATE qual.test_runs SET name='corrupted' WHERE id='66666666-6666-4666-8666-666666666666'" >/dev/null 2>&1; then
 echo 'Immutable old Run unexpectedly mutable' >&2;exit 11
fi
if psql_db -c "DELETE FROM qual.historical_test_run_attributions" >/dev/null 2>&1; then
 echo 'Append-only attribution unexpectedly deletable' >&2;exit 11
fi
if "$GOOSE_BIN" -dir apps/server/migrations postgres "$SCRATCH" down >/dev/null 2>&1; then
 echo 'Forbidden destructive Goose21 Down unexpectedly accepted' >&2;exit 12
fi
[[ "$(psql_db -c 'SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied')" = 21 ]] || exit 12
[[ "$(psql_db -c 'SELECT COUNT(*) FROM qual.historical_test_run_attributions')" = 1 ]] || exit 12
printf 'QUAL75_GOOSE21_FORWARD_ISOLATED_PASS old_runs_unchanged=true unique=1 ambiguous=0 uncited=0 task_mode_not_auto_enabled=true immutable=true forbidden_down_refused=true\n'
