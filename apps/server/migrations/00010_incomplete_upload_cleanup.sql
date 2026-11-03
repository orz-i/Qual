-- +goose Up
-- Expired incomplete uploads can be pending metadata or already uploaded bytes.
ALTER TABLE qual.artifact_maintenance_events
 DROP CONSTRAINT artifact_maintenance_events_action_check;
ALTER TABLE qual.artifact_maintenance_events
 ADD CONSTRAINT artifact_maintenance_events_action_check
 CHECK(action IN ('expired_pending_cleanup','expired_uploaded_cleanup'));

-- +goose Down
ALTER TABLE qual.artifact_maintenance_events
 DROP CONSTRAINT artifact_maintenance_events_action_check;
ALTER TABLE qual.artifact_maintenance_events
 ADD CONSTRAINT artifact_maintenance_events_action_check
 CHECK(action IN ('expired_pending_cleanup'));
