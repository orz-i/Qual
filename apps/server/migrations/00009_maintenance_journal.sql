-- +goose Up
-- System maintenance is not performed on behalf of the original uploader.
-- Keep a separate immutable journal instead of misattributing a system cleanup
-- to the human/agent credential that created the pending record.
CREATE TABLE qual.artifact_maintenance_events (
 id uuid PRIMARY KEY,
 project_id uuid NOT NULL REFERENCES qual.projects(id),
 artifact_id uuid NOT NULL REFERENCES qual.artifacts(id),
 action text NOT NULL CHECK(action IN ('expired_pending_cleanup')),
 detail text NOT NULL DEFAULT '',
 occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX artifact_maintenance_project_idx ON qual.artifact_maintenance_events(project_id,occurred_at DESC,id);
CREATE TRIGGER artifact_maintenance_immutable BEFORE UPDATE OR DELETE ON qual.artifact_maintenance_events
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
DROP TRIGGER artifact_maintenance_immutable ON qual.artifact_maintenance_events;
DROP TABLE qual.artifact_maintenance_events;
