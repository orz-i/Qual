-- +goose Up
-- QUAL-27: strictly read-only context query indexes. Goose 00014 is reserved
-- by QUAL-29; Goose 00016 by QUAL-32. Do not touch either parallel migration.
-- These indexes do not alter any historical Attempt, Coverage or WorkItem.
CREATE INDEX test_case_coverage_work_context_idx
  ON qual.test_case_coverage(project_id,work_item_id,test_case_id);
CREATE INDEX test_run_items_case_context_idx
  ON qual.test_run_items(project_id,test_case_id,id);
CREATE INDEX test_attempts_run_context_idx
  ON qual.test_attempts(project_id,run_item_id,ingested_at DESC,id DESC);
-- Globally ordered read of all project Attempts. The query filters
-- by authorized WorkItem Coverage before returning any fact.
CREATE INDEX test_attempts_project_ingested_context_idx
  ON qual.test_attempts(project_id,ingested_at DESC,id DESC);

-- +goose Down
DROP INDEX IF EXISTS qual.test_attempts_project_ingested_context_idx;
DROP INDEX IF EXISTS qual.test_attempts_run_context_idx;
DROP INDEX IF EXISTS qual.test_run_items_case_context_idx;
DROP INDEX IF EXISTS qual.test_case_coverage_work_context_idx;
