-- +goose Up
-- QUAL-58: an explicitly task-bound TestRun has one immutable owning
-- WorkItem. Legacy/unbound project runs remain NULL for compatibility.
ALTER TABLE qual.test_runs
 ADD COLUMN work_item_id uuid,
 ADD CONSTRAINT test_runs_bound_work_fkey
   FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id);
CREATE INDEX test_runs_bound_work_idx
 ON qual.test_runs(project_id,work_item_id,id)
 WHERE work_item_id IS NOT NULL;

-- The binding and frozen run metadata are historical facts: no actor may
-- re-target an existing Run after it has been cited by TestAttempt/DoneGate.
CREATE TRIGGER test_runs_append_only BEFORE UPDATE OR DELETE ON qual.test_runs
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 RAISE EXCEPTION 'Goose20 task-bound immutable TestRun provenance must not be dropped';
END
$$;
-- +goose StatementEnd
