#!/usr/bin/env bash
set -euo pipefail
# Reproduce fanout workloads only against random, disposable PostgreSQL clones.
# No default qual migration, no service restart, no fake Qual TestRun.
cd "$(dirname "$0")/../.."
DB="qual27_ctx_fanout_$(python3 -c 'import uuid; print(uuid.uuid4().hex[:10])')"
COUNT=${COUNT:-10000}
[[ $COUNT =~ ^[0-9]+$ && $COUNT -ge 100 && $COUNT -le 20000 ]] || { echo 'bounded count required';exit 4; }
[ "$(docker exec qual-dev-postgres-1 psql -U qual -d postgres -Atc "SELECT count(*) FROM pg_database WHERE datname='$DB'")" = '0' ] || { echo 'Scratch DB exists; refused'; exit 5; }
CREATED=0
cleanup(){ if [ "$CREATED" = 1 ]; then docker exec qual-dev-postgres-1 psql -U qual -d postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE $DB WITH (FORCE)" >/dev/null && echo 'SCRATCH_CLEANED';fi;}
trap cleanup EXIT
VER=$(docker exec qual-dev-postgres-1 psql -U qual -d qual_phase5_test -Atc 'SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied=true')
[ "$VER" = 12 ] || { echo 'wrong template version';exit 6; }
docker exec qual-dev-postgres-1 createdb -U qual -T qual_phase5_test "$DB"; CREATED=1
for m in 00013_explicit_review_acceptance.sql 00015_work_context_attempts_indexes.sql; do
 awk '/^-- \+goose Up$/{a=1;next} /^-- \+goose Down$/{a=0} a{print}' "apps/server/migrations/$m" | docker exec -i qual-dev-postgres-1 psql -U qual -d "$DB" -q -v ON_ERROR_STOP=1 >/dev/null
done
ROW=$(docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -AtF '|' -c "SELECT ri.project_id,ri.test_case_id,ri.test_case_version_id,w.id,r.source_principal_id FROM qual.test_run_items ri JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id JOIN LATERAL (SELECT w.id FROM qual.work_items w WHERE w.project_id=ri.project_id LIMIT 1) w ON true LIMIT 1")
IFS='|' read -r PROJ CASE_ID VERSION WORK REPORTER <<< "$ROW"
[ "${#PROJ}" = 36 ] || { echo 'missing fixture'; exit 7; }
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 -q -c "INSERT INTO qual.test_case_coverage(project_id,test_case_id,work_item_id) VALUES ('$PROJ','$CASE_ID','$WORK') ON CONFLICT DO NOTHING"
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -v ON_ERROR_STOP=1 -q -c "BEGIN;
CREATE TEMP TABLE audit_generated AS SELECT g,gen_random_uuid() run_id,gen_random_uuid() run_item_id FROM generate_series(1,$COUNT) g;
INSERT INTO qual.test_runs(id,project_id,name,source_principal_id) SELECT run_id,'$PROJ','audit synthetic '||g,'$REPORTER' FROM audit_generated;
INSERT INTO qual.test_run_items(id,project_id,run_id,test_case_id,test_case_version_id,snapshot_sha256) SELECT run_item_id,'$PROJ',run_id,'$CASE_ID','$VERSION',decode(repeat('aa',32),'hex') FROM audit_generated;
INSERT INTO qual.test_attempts(id,project_id,run_item_id,attempt_no,status,note,observed_at,ingested_at,reported_by,source_kind) SELECT gen_random_uuid(),'$PROJ',run_item_id,1,'passed','isolated run fanout',now(),now()-g*interval '1 millisecond','$REPORTER','manual' FROM audit_generated;
COMMIT;
ANALYZE qual.test_runs;
ANALYZE qual.test_run_items;
ANALYZE qual.test_attempts;
ANALYZE qual.test_case_coverage;"
OUT="${QUAL27_FANOUT_OUTPUT:-tmp/qual27-perf/fanout-plan.json}"
mkdir -p "$(dirname "$OUT")"
# The MATERIALIZED candidate CTE matches the production wide-path SQL.
# We explain only the first bounded batch; an HTTP call repeats batches if
# fewer than limit+1 authorized facts are found, under a strict scan budget.
docker exec qual-dev-postgres-1 psql -U qual -d "$DB" -At -v ON_ERROR_STOP=1 -c "EXPLAIN (ANALYZE, BUFFERS, TIMING OFF, FORMAT JSON)
WITH candidate AS MATERIALIZED (
 SELECT a.id,a.project_id,a.run_item_id,a.attempt_no,a.status,a.source_kind,a.observed_at,a.ingested_at,a.note
 FROM qual.test_attempts a WHERE a.project_id='$PROJ'
  AND (NULL::timestamptz IS NULL OR (a.ingested_at,a.id)<(NULL::timestamptz,'00000000-0000-0000-0000-000000000000'::uuid))
 ORDER BY a.ingested_at DESC,a.id DESC LIMIT 512
)
SELECT a.ingested_at,a.id,CASE WHEN cv.work_item_id IS NULL THEN NULL ELSE
 jsonb_build_object('source','test_attempts','source_id',a.id,'run_id',r.id,'run_item_id',ri.id,
 'test_case_id',ri.test_case_id,'test_case_version_id',ri.test_case_version_id,
 'attempt_no',a.attempt_no,'status',a.status,'source_kind',a.source_kind,
 'observed_at',a.observed_at,'ingested_at',a.ingested_at,'excerpt',left(a.note,240)) END
FROM candidate a JOIN qual.test_run_items ri ON ri.project_id=a.project_id AND ri.id=a.run_item_id
 JOIN qual.test_runs r ON r.project_id=ri.project_id AND r.id=ri.run_id
 LEFT JOIN qual.test_case_coverage cv ON cv.project_id=a.project_id AND cv.work_item_id='$WORK' AND cv.test_case_id=ri.test_case_id
ORDER BY a.ingested_at DESC,a.id DESC" > "$OUT"
python3 - "$OUT" <<'PY'
import json,sys
j=json.load(open(sys.argv[1]))[0]
print('FANOUT_RUN_ITEMS',int(__import__('os').environ.get('COUNT','10000')))
print('FANOUT_SQL_EXECUTION_MS',j['Execution Time'])
print('FANOUT_SHARED_HITS',j['Plan'].get('Shared Hit Blocks',-1))
index_probes=[]
def walk(n):
 if n.get('Index Name','').startswith('test_attempts_'):
  index_probes.append((n.get('Index Name'),n.get('Actual Rows'),n.get('Actual Loops')))
  print('ATTEMPT_INDEX_SCAN',n.get('Index Name'),'ROWS_PER_LOOP',n.get('Actual Rows'),'LOOPS',n.get('Actual Loops'))
 if n['Node Type']=='Sort':
  print('SORT_ROWS',n.get('Actual Rows'),'INPUT_ROWS',[(c.get('Actual Rows'),c.get('Actual Loops')) for c in n.get('Plans',[])])
 for ch in n.get('Plans',[]):walk(ch)
walk(j['Plan'])
if not any(name=='test_attempts_project_ingested_context_idx' and rows<=512 and loops<=2 for name,rows,loops in index_probes):
 raise SystemExit('FANOUT_PLAN_GATE=FAIL: global top-N index not used or too many rows read')
if j['Plan'].get('Actual Rows')!=512:
 raise SystemExit('FANOUT_PLAN_GATE=FAIL: page does not contain the expected rows')
print('FANOUT_PLAN_GATE=PASS')
PY
