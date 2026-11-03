-- +goose Up
-- Prevent the same external report from being replayed under another key or reporter.
ALTER TABLE qual.test_report_imports
 ADD CONSTRAINT test_report_run_sha_unique UNIQUE(run_id,report_sha256);

-- Database defense-in-depth: only failed Attempts may link to actual Bug work items.
-- +goose StatementBegin
CREATE FUNCTION qual.validate_attempt_defect() RETURNS trigger AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM qual.test_attempts a
      WHERE a.id=NEW.attempt_id AND a.project_id=NEW.project_id AND a.status='failed') THEN
   RAISE EXCEPTION 'defect link requires a failed attempt';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM qual.work_items w
      WHERE w.id=NEW.bug_work_item_id AND w.project_id=NEW.project_id AND w.kind='bug') THEN
   RAISE EXCEPTION 'defect link requires a bug work item';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER validate_attempt_defect_insert
 BEFORE INSERT ON qual.attempt_defects
 FOR EACH ROW EXECUTE FUNCTION qual.validate_attempt_defect();

-- +goose Down
DROP TRIGGER validate_attempt_defect_insert ON qual.attempt_defects;
DROP FUNCTION qual.validate_attempt_defect();
ALTER TABLE qual.test_report_imports DROP CONSTRAINT test_report_run_sha_unique;
