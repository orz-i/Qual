#!/usr/bin/env bash
# CI-only durable-data guard regressions; never runs against the default qual.
# Every test writes exclusively to a newly created, exactly named disposable DB.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$ROOT"
: "${GITHUB_RUN_ID:?CI unique run ID required}"
: "${GITHUB_RUN_ATTEMPT:?CI attempt ID required}"
[[ "$GITHUB_RUN_ID" =~ ^[0-9]{1,25}$ ]] || exit 2
[[ "$GITHUB_RUN_ATTEMPT" =~ ^[0-9]{1,5}$ ]] || exit 2
GOOSE_BIN="${GOOSE:-$(go env GOPATH)/bin/goose}"
[[ -x "$GOOSE_BIN" ]] || { echo "Missing verified Goose tool" >&2; exit 2; }

pg() {
  docker compose -f deploy/compose.dev.yaml exec -T postgres \
    psql -U qual -d postgres -X -A -t -v ON_ERROR_STOP=1 "$@"
}

run_scoped() (
  set -euo pipefail
  local group="$1" prefix="$2" version="$3" pattern="$4" want_pass="$5" want_skip="$6"
  local db="${prefix}_ci_${GITHUB_RUN_ID}_${GITHUB_RUN_ATTEMPT}"
  [[ "$prefix" =~ ^(qual27_ctx_integrated|qual32_run|qual21_gate|qual21_history12|qual36_adapter|qual30_event)$ ]] || exit 3
  [[ "$db" =~ ^(qual27_ctx_integrated|qual32_run|qual21_gate|qual21_history12|qual36_adapter|qual30_event)_ci_[0-9]+_[0-9]+$ ]] || exit 3
  [[ "$(pg -c "SELECT COUNT(*) FROM pg_database WHERE datname='$db'")" = 0 ]] || {
    echo "Disposable DB already exists; refuse to reuse" >&2
    exit 4
  }
  local created=0 log=""
  cleanup() {
    [[ -z "$log" ]] || rm -f "$log"
    if [[ "$created" = 1 ]]; then
      # Only this subprocess's scratch database, never the default qual.
      pg -c "DROP DATABASE $db WITH (FORCE)" >/dev/null
    fi
  }
  trap cleanup EXIT
  pg -c "CREATE DATABASE $db" >/dev/null
  created=1
  local url="postgres://qual:qual_dev_only@127.0.0.1:15432/${db}?sslmode=disable"
  "$GOOSE_BIN" -dir apps/server/migrations postgres "$url" up-to "$version" >/dev/null
  local observed
  observed="$(docker compose -f deploy/compose.dev.yaml exec -T postgres \
    psql -U qual -d "$db" -X -A -t -v ON_ERROR_STOP=1 \
    -c "SELECT MAX(version_id) FROM public.goose_db_version WHERE is_applied")"
  [[ "$observed" = "$version" ]] || { echo "Wrong scratch Goose version" >&2; exit 5; }
  log="$(mktemp "${TMPDIR:-/tmp}/qual-guarded-${group}.XXXXXX")"
  if ! (
    cd apps/server
    QUAL_TEST_DATABASE_URL="$url" go test -race -tags=integration -count=1 \
      -run "$pattern" -v ./integration
  ) >"$log" 2>&1; then
    echo "Guarded integration group $group failed" >&2
    # CI fixtures are synthetic. Keep output bounded and show only failing
    # Go test locations; do not print credential/HTTP request bodies.
    grep -E '^--- FAIL:|^    [^[:space:]]+_test.go:[0-9]+:|^FAIL' "$log" | tail -25 >&2 || true
    exit 6
  fi
  local passed skipped
  passed="$(grep -c '^--- PASS:' "$log" || true)"
  skipped="$(grep -c '^--- SKIP:' "$log" || true)"
  [[ "$passed" = "$want_pass" && "$skipped" = "$want_skip" ]] || {
    echo "Guarded group $group incomplete: PASS=$passed SKIP=$skipped" >&2
    exit 7
  }
  echo "PASS $group Goose$version: $passed top-level tests, $skipped expected skip"
)

run_scoped external-events qual30_event 17 '^TestQual30ExternalEventAppendOnlyIdentityScopeAndTimeline$' 1 0
run_scoped github-adapter qual36_adapter 17 '^TestQual36ExternalAdapterSignedSyntheticPRToRealGoose17PI$' 1 0
run_scoped context qual27_ctx_integrated 17 '^TestQual27Context' 3 0
run_scoped runs-and-archive qual32_run 17 '^(TestAgentRun|TestQual4344AuditMultiRunArchiveCompleteness|TestQual34Attention|TestQual37CrossContext)' 6 0
run_scoped done-gate qual21_gate 17 '^TestPhase11' 9 1
# The historical Goose12 case MUST execute against an actual Goose12 DB.
run_scoped historical-goose12 qual21_history12 12 \
  '^TestPhase11ReadAcceptanceHistoryFromPreMigrationGoose12$' 1 0

echo 'All 21 guarded ExternalEvent/GitHubAdapter/Context/AgentRun/Archive/Attention/QUAL37/DoneGate/Goose12 tests passed'
