-- +goose Up
-- QUAL-30: adapter-reported ExternalEvent is an immutable fact, NEVER a
-- WorkItem status change, TestAttempt or Agent executor. Existing Goose16
-- permission table already allows event:write but does not grant it by default.
CREATE TABLE qual.external_events (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  work_item_id uuid NOT NULL,
  thread_id uuid NOT NULL,
  source text NOT NULL CHECK(source ~ '^[a-z][a-z0-9_-]{0,63}$'),
  external_event_id text NOT NULL CHECK(length(external_event_id) BETWEEN 1 AND 160),
  event_type text NOT NULL CHECK(event_type ~ '^[a-z][a-z0-9_.-]{0,79}$'),
  occurred_at timestamptz NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now(),
  payload_sha256 bytea NOT NULL CHECK(octet_length(payload_sha256)=32),
  summary text NOT NULL CHECK(length(summary) BETWEEN 1 AND 240 AND summary !~ '[[:cntrl:]]'),
  reporter_principal_id uuid NOT NULL,
  registration_credential_id uuid NOT NULL REFERENCES qual.credentials(id),
  FOREIGN KEY(workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
  FOREIGN KEY(project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
  FOREIGN KEY(project_id,thread_id) REFERENCES qual.external_threads(project_id,id),
  FOREIGN KEY(workspace_id,reporter_principal_id) REFERENCES qual.principals(workspace_id,id),
  UNIQUE(project_id,source,external_event_id),
  UNIQUE(project_id,id)
);
CREATE INDEX external_events_work_timeline_idx
  ON qual.external_events(project_id,work_item_id,received_at DESC,id DESC);
CREATE INDEX external_events_thread_timeline_idx
  ON qual.external_events(project_id,thread_id,received_at DESC,id DESC);
CREATE TRIGGER external_events_append_only BEFORE UPDATE OR DELETE ON qual.external_events
  FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 RAISE EXCEPTION 'Goose17 ExternalEvent facts are immutable: disable routes; do not drop persisted history';
END
$$;
-- +goose StatementEnd
