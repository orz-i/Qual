-- +goose Up
-- QUAL-65: non-destructive historical TestRun -> WorkItem attribution.
--
-- Goose20 made test_runs.work_item_id immutable. Never rewrite an old Run or
-- disable test_runs_append_only to backfill it. Instead, preserve the exact
-- role/acceptance references used to infer a UNIQUE historical attribution.
-- The table is NOT an authorization relationship: WorkItem resource scopes
-- still require the actual immutable test_runs.work_item_id on new Runs.
CREATE TABLE qual.historical_test_run_attributions (
    project_id uuid NOT NULL,
    test_run_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    role_entry_ids uuid[] NOT NULL DEFAULT '{}'::uuid[],
    acceptance_ids uuid[] NOT NULL DEFAULT '{}'::uuid[],
    attribution_kind text NOT NULL DEFAULT 'unique_explicit_historical_reference'
        CHECK (attribution_kind = 'unique_explicit_historical_reference'),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id,test_run_id),
    FOREIGN KEY (project_id,test_run_id) REFERENCES qual.test_runs(project_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    CHECK (cardinality(role_entry_ids) + cardinality(acceptance_ids) > 0)
);
CREATE INDEX historical_test_run_attributions_work_idx
 ON qual.historical_test_run_attributions(project_id,work_item_id,test_run_id);
CREATE TRIGGER historical_test_run_attributions_append_only
 BEFORE UPDATE OR DELETE ON qual.historical_test_run_attributions
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- Accept only existing explicit task references. Coverage by shared TestCase,
-- similar titles, timestamps or a prior Agent are NOT proof of owning WorkItem.
-- Conflicting references are excluded, never resolved by arbitrary ordering.
WITH cited AS (
    SELECT r.project_id, r.id AS test_run_id, e.work_item_id,
           e.id AS role_id, NULL::uuid AS acceptance_id
    FROM qual.test_runs r
    JOIN qual.work_role_entries e
      ON e.project_id=r.project_id
     AND e.reference_kind='test_run'
     AND e.reference_value=r.id::text
    WHERE r.work_item_id IS NULL
    UNION ALL
    SELECT r.project_id, r.id, a.work_item_id,
           NULL::uuid, a.id
    FROM qual.test_runs r
    JOIN qual.work_acceptance_decisions a
      ON a.project_id=r.project_id
     AND a.reference_kind='test_run'
     AND a.reference_value=r.id::text
    WHERE r.work_item_id IS NULL
), unique_refs AS (
    SELECT project_id,test_run_id,
           MIN(work_item_id::text)::uuid AS work_item_id,
           COALESCE(
             ARRAY_AGG(DISTINCT role_id ORDER BY role_id)
               FILTER (WHERE role_id IS NOT NULL),
             '{}'::uuid[]) AS role_entry_ids,
           COALESCE(
             ARRAY_AGG(DISTINCT acceptance_id ORDER BY acceptance_id)
               FILTER (WHERE acceptance_id IS NOT NULL),
             '{}'::uuid[]) AS acceptance_ids
    FROM cited
    GROUP BY project_id,test_run_id
    HAVING COUNT(DISTINCT work_item_id)=1
)
INSERT INTO qual.historical_test_run_attributions(
    project_id,test_run_id,work_item_id,role_entry_ids,acceptance_ids)
SELECT project_id,test_run_id,work_item_id,role_entry_ids,acceptance_ids
FROM unique_refs;

COMMENT ON TABLE qual.historical_test_run_attributions IS
 'Append-only historical attribution from uniquely matching immutable RoleEntry/Acceptance references; not an authorization grant or a rewrite of the original TestRun';

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 RAISE EXCEPTION 'Goose21 historical TestRun attribution is immutable; destructive Down is forbidden. Use signed backup and forward recovery';
END
$$;
-- +goose StatementEnd
