#!/usr/bin/env bash
set -euo pipefail
# Operator-invoked, local-only reproduction of the QUAL-27 P1 regression.
# Never touch the default database or the shared fixture's contents.
cd "$(dirname "$0")/../.."
TEMPLATE="${QUAL27_PERF_TEMPLATE_DB:-qual_phase5_test}"
[[ "$TEMPLATE" =~ ^qual_phase[0-9a-z_]*_test$ ]] || { echo 'only known disposable test templates allowed' >&2; exit 3; }
DB="qual27_ctx_perf_$(python3 -c 'import uuid;print(uuid.uuid4().hex[:12])')"
OUT="${QUAL27_PERF_OUTPUT:-tmp/qual27-perf/plan.json}"
mkdir -p "$(dirname "$OUT")"
VER=$(docker exec qual-dev-postgres-1 psql -U qual -d "$TEMPLATE" -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true')
[ "$VER" = '12' ] || { echo "expected disposable fixture Goose12, got $VER" >&2; exit 3; }
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname='$DB'")" = '0' ] || { echo 'scratch name collision'; exit 3; }
created=0
cleanup(){ [ "$created" = 0 ] || { docker exec qual-dev-postgres-1 psql -U qual -d postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE $DB WITH (FORCE)" >/dev/null && echo 'SCRATCH_PERF_DB_CLEANED'; }; }
trap cleanup EXIT
docker exec qual-dev-postgres-1 createdb -U qual -T "$TEMPLATE" "$DB"; created=1
for m in 00013_explicit_review_acceptance.sql 00015_work_context_attempts_indexes.sql; do
  awk '/^-- \+goose Up$/{isup=1;next} /^-- \+goose Down$/{isup=0} isup{print}' "apps/server/migrations/$m" | docker exec -i qual-dev-postgres-1 psql -U qual -d "$DB" -q -v ON_ERROR_STOP=1 >/dev/null
done
ROW=$(docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -AtF '|' -c "SELECT ri.project_id,ri.id,ri.test_case_id,w.id,r.source_principal_id FROM qual.test_run_items ri JOIN qual.test_runs r ON r.id=ri.run_id AND r.project_id=ri.project_id JOIN LATERAL (SELECT id FROM qual.work_items w WHERE w.project_id=ri.project_id LIMIT 1) w ON true LIMIT 1")
IFS='|' read -r PROJ RI CASE_ID WORK REPORTER <<< "$ROW"
[ "${#PROJ}" = 36 ] || { echo 'missing fixture'; exit 3; }
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -q -v ON_ERROR_STOP=1 -c "INSERT INTO qual.test_case_coverage(project_id,test_case_id,work_item_id) VALUES ('$PROJ','$CASE_ID','$WORK') ON CONFLICT DO NOTHING"
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -q -v ON_ERROR_STOP=1 -c "INSERT INTO qual.test_attempts(id,project_id,run_item_id,attempt_no,status,note,observed_at,ingested_at,reported_by,source_kind) SELECT gen_random_uuid(),'$PROJ','$RI',100000+g,'passed','synthetic isolated perf test',now(),now() - g * interval '1 millisecond','$REPORTER','manual' FROM generate_series(1,50000) AS g"
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -q -v ON_ERROR_STOP=1 -c 'ANALYZE qual.test_attempts; ANALYZE qual.test_run_items; ANALYZE qual.test_case_coverage;'
# Actual NARROW read path: <=32 RunItems, bounded LATERAL per RunItem.
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -At -v ON_ERROR_STOP=1 -c "EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON)
SELECT jsonb_build_object('source','test_attempts','source_id',a.id,'run_id',r.id,
 'run_item_id',ri.id,'test_case_id',ri.test_case_id,'test_case_version_id',ri.test_case_version_id,
 'attempt_no',a.attempt_no,'status',a.status,'source_kind',a.source_kind,
 'observed_at',a.observed_at,'ingested_at',a.ingested_at,'excerpt',left(a.note,240))
FROM qual.test_case_coverage cv
JOIN qual.test_run_items ri ON ri.project_id=cv.project_id AND ri.test_case_id=cv.test_case_id
JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id
JOIN LATERAL (
 SELECT a.id,a.attempt_no,a.status,a.source_kind,a.observed_at,a.ingested_at,a.note
 FROM qual.test_attempts a
 WHERE a.project_id=ri.project_id AND a.run_item_id=ri.id
  AND (NULL::timestamptz IS NULL OR (a.ingested_at,a.id)<(NULL::timestamptz,'00000000-0000-0000-0000-000000000000'::uuid))
 ORDER BY a.ingested_at DESC,a.id DESC LIMIT 21
) a ON true
WHERE cv.project_id='$PROJ' AND cv.work_item_id='$WORK'
ORDER BY a.ingested_at DESC,a.id DESC LIMIT 21" > "$OUT"
python3 - "$OUT" <<'PY'
import json,sys
p=json.load(open(sys.argv[1]))[0]
rows=[]
def walk(n):
 rows.append((n['Node Type'],n.get('Index Name',''),n.get('Actual Rows'),n.get('Actual Loops')))
 for x in n.get('Plans',[]):walk(x)
walk(p['Plan'])
print('EXECUTION_MS',p['Execution Time'])
for node,idx,ct,loops in rows:
 if 'test_attempts' in idx or node in ('Limit','Sort'):
  print('PLAN',node,idx,'rows',ct,'loops',loops)
if not any(idx=='test_attempts_run_context_idx' and ct <= 22 and loops <= 2 for node,idx,ct,loops in rows):
 raise RuntimeError('missing bounded indexed scan for 50000 attempts; F1 still open')
print('NARROW_PERF_PLAN_GATE=PASS')
PY
