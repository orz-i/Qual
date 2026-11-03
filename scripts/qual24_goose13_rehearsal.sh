#!/usr/bin/env bash
# QUAL-24: repeatable test-only migration rehearsal. This script has NO path
# that applies schema changes to the default 'qual' database.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT"
DC="deploy/compose.dev.yaml"
MIGRATION="apps/server/migrations/00013_explicit_review_acceptance.sql"
SCRATCH="qual24_rehearsal_$(date +%s)_$$"
OUT="${1:-tmp/phase10/qual24-goose13-rehearsal.json}"
[[ "$OUT" != -* ]] || { echo 'Usage: qual24_goose13_rehearsal.sh [report-path]' >&2; exit 2; }
[[ "$SCRATCH" =~ ^qual24_rehearsal_[0-9]+_[0-9]+$ ]] || exit 2
[[ -f "$MIGRATION" ]] || exit 2
mkdir -p "$(dirname "$OUT")"

db() { docker compose -f "$DC" exec -T postgres psql -X -v ON_ERROR_STOP=1 -U qual -d "$1" "${@:2}"; }
cleanup() {
  docker compose -f "$DC" exec -T postgres dropdb -U qual --if-exists "$SCRATCH" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Do not infer authorization from a green readiness probe. Read-only default
# schema check is informational, never passed to the SQL mutation commands.
before_default="$(db qual -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$before_default" == "12" ]] || { echo 'Default Qual is not Goose12; refusing rehearsal until manually reviewed' >&2; exit 3; }

template_version="$(db qual_phase5_test -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$template_version" == "12" ]] || { echo 'Test-only migrated Goose12 template absent or invalid' >&2; exit 3; }
docker compose -f "$DC" exec -T postgres createdb -U qual -T qual_phase5_test "$SCRATCH"

fingerprints() {
  db "$SCRATCH" -Atc "
    SELECT 'work_items:' || count(*) || ':' || md5(coalesce(string_agg(md5(to_jsonb(t)::text), '|' ORDER BY id::text),'')) FROM qual.work_items t;
    SELECT 'work_role_entries:' || count(*) || ':' || md5(coalesce(string_agg(md5(to_jsonb(t)::text), '|' ORDER BY id::text),'')) FROM qual.work_role_entries t;
    SELECT 'audit_records:' || count(*) || ':' || md5(coalesce(string_agg(md5(to_jsonb(t)::text), '|' ORDER BY id::text),'')) FROM qual.audit_records t;
    SELECT 'test_attempts:' || count(*) || ':' || md5(coalesce(string_agg(md5(to_jsonb(t)::text), '|' ORDER BY id::text),'')) FROM qual.test_attempts t;
    SELECT 'artifacts:' || count(*) || ':' || md5(coalesce(string_agg(md5(to_jsonb(t)::text), '|' ORDER BY id::text),'')) FROM qual.artifacts t;" | tr -d '\r'
}
BEFORE="$(fingerprints)"
# Exactly one whole transaction. Generated SQL comes only from checked-in
# migration, not interpolation of user input or production database names.
python3 - "$MIGRATION" <<'PY' | db "$SCRATCH" -1 >/dev/null
from pathlib import Path
import sys
s=Path(sys.argv[1]).read_text()
assert s.count('-- +goose Up')==1 and s.count('-- +goose Down')==1
print(s.split('-- +goose Down')[0].replace('-- +goose Up',''))
print("INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(13,true);")
PY

up_version="$(db "$SCRATCH" -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$up_version" == "13" ]] || { echo 'Isolated Up migration did not reach Goose13' >&2; exit 4; }
fresh_counts="$(db "$SCRATCH" -Atc 'SELECT count(*) FROM qual.project_review_policies; SELECT count(*) FROM qual.work_review_policy_overrides; SELECT count(*) FROM qual.review_policy_changes; SELECT count(*) FROM qual.work_acceptance_decisions;' | tr -d '\r')"
[[ "$fresh_counts" == $'0\n0\n0\n0' ]] || { echo 'Migration unexpectedly populated policy or acceptance history' >&2; exit 4; }
triggers="$(db "$SCRATCH" -Atc "SELECT count(*) FROM pg_trigger WHERE tgname IN ('review_policy_changes_append_only','acceptance_decisions_append_only')" | tr -d '\r')"
[[ "$triggers" == "2" ]] || { echo 'Missing append-only historical protection' >&2; exit 4; }
[[ "$BEFORE" == "$(fingerprints)" ]] || { echo 'Schema Up changed historical business row fingerprints' >&2; exit 4; }

# Negative regression: write ONE synthetic policy row in this disposable
# test database, prove Goose13 Down refuses to erase governance, then delete
# the fixture and prove empty-table Down is still possible. The historical
# business row fingerprints must stay unchanged throughout.
fixture_count="$(db "$SCRATCH" -Atc "
  WITH inserted AS (
    INSERT INTO qual.project_review_policies
      (workspace_id,project_id,default_mode,owner_acceptance_allowed,
       force_independent_for_high_risk,version,updated_by)
    SELECT p.workspace_id,p.id,'independent_review',false,true,1,pr.id
      FROM qual.projects p
      JOIN qual.principals pr ON pr.workspace_id=p.workspace_id
      ORDER BY p.id,pr.id LIMIT 1
    RETURNING project_id
  )
  SELECT count(*) FROM inserted;" | tr -d '\r')"
[[ "$fixture_count" == "1" ]] || { echo 'Cannot create one synthetic policy row in isolated test DB' >&2; exit 4; }
set +e
python3 - "$MIGRATION" <<'PY' | db "$SCRATCH" -1 >tmp/phase10/qual24-down-guard-rejection.log 2>&1
from pathlib import Path
import sys
print(Path(sys.argv[1]).read_text().split('-- +goose Down')[1])
print("UPDATE public.goose_db_version SET is_applied=false WHERE version_id=13 AND is_applied=true;")
PY
guard_status=$?
set -e
[[ "$guard_status" -ne 0 ]] || { echo 'CRITICAL: Goose13 Down erased existing governance history' >&2; exit 4; }
grep -q 'Goose13 Down refused: review governance records exist' tmp/phase10/qual24-down-guard-rejection.log || {
  echo 'Down failed for an unexpected reason; protection was not validated' >&2; exit 4;
}
guard_version="$(db "$SCRATCH" -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$guard_version" == "13" ]] || { echo 'Rejected Down changed goose version despite rollback' >&2; exit 4; }
db "$SCRATCH" -c 'DELETE FROM qual.project_review_policies;' >/dev/null
[[ "$BEFORE" == "$(fingerprints)" ]] || { echo 'Guard regression touched historical business rows' >&2; exit 4; }

# Down succeeds ONLY while all four governance tables are empty; once a
# genuine decision/history exists production rollback must stay forward-only.
python3 - "$MIGRATION" <<'PY' | db "$SCRATCH" -1 >/dev/null
from pathlib import Path
import sys
print(Path(sys.argv[1]).read_text().split('-- +goose Down')[1])
print("UPDATE public.goose_db_version SET is_applied=false WHERE version_id=13 AND is_applied=true;")
PY
down_version="$(db "$SCRATCH" -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$down_version" == "12" ]] || { echo 'Disposable schema Down did not return to Goose12' >&2; exit 5; }
remaining="$(db "$SCRATCH" -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='qual' AND tablename IN ('project_review_policies','work_review_policy_overrides','review_policy_changes','work_acceptance_decisions')" | tr -d '\r')"
[[ "$remaining" == "0" && "$BEFORE" == "$(fingerprints)" ]] || { echo 'Down did not preserve historical business data' >&2; exit 5; }
cleanup
trap - EXIT

after_default="$(db qual -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true' | tr -d '\r')"
[[ "$after_default" == "12" ]] || { echo 'Default schema changed; operator investigation required' >&2; exit 6; }
migration_sha="$(shasum -a 256 "$MIGRATION" | awk '{print $1}')"
export QUAL24_REPORT="$OUT" QUAL24_FINGERPRINT="$BEFORE" QUAL24_MIGRATION_SHA="$migration_sha"
python3 - <<'PY'
from pathlib import Path
from datetime import datetime,timezone
import json,os
p=Path(os.environ['QUAL24_REPORT'])
fingerprints={}
for line in os.environ['QUAL24_FINGERPRINT'].splitlines():
    table,count,digest=line.split(':',2)
    fingerprints[table]={'rows':int(count),'md5_of_ordered_json_row_hashes':digest}
r={
 'verified_utc':datetime.now(timezone.utc).isoformat(),
 'scope':'disposable migrated TEST database clone ONLY; never default Qual',
 'result':'PASS','migration_file':'apps/server/migrations/00013_explicit_review_acceptance.sql',
 'migration_sha256':os.environ['QUAL24_MIGRATION_SHA'],
 'default_database_schema_before':12,'default_database_schema_after':12,
 'test_template_schema':12,
 'isolated_goose13_up':True,'isolated_goose12_down_before_real_acceptances':True,
 'new_review_policy_and_acceptance_rows_after_up':0,
 'append_only_history_triggers_after_up':2,
 'destructive_goose13_down_blocked_after_synthetic_policy_write':True,
 'historic_business_row_fingerprints_preserved':True,
 'historic_fingerprints':fingerprints,
 'scratch_database_dropped':True,
 'actual_default_database_migrated':False,
 'actual_human_operator_claimed_task':False,
 'production_down_after_any_acceptance_is_unsafe':True,
 'does_not_read_or_write_privileged_human_session_tokens':True
}
p.write_text(json.dumps(r,ensure_ascii=False,indent=2)+'\n')
print('QUAL-24 safe Goose13 test-only migration rehearsal PASS; report:',p)
PY
