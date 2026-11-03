-- +goose Up
-- Qual only receives external run reports. This schema NEVER schedules agents.
-- Separate state projection from append-only source facts and operator audit.
-- Append the explicit permission without rewriting the historical Goose11 migration.
-- Goose16 must be rolled out after reserved Goose14/15; never skip those
-- lower migrations. Preserve QUAL-30 event:write permissions if installed.
ALTER TABLE qual.project_permission_overrides
 DROP CONSTRAINT project_permission_overrides_permission_check;
ALTER TABLE qual.project_permission_overrides
 ADD CONSTRAINT project_permission_overrides_permission_check CHECK(permission IN (
  'work:read','work:write','work:claim','work:review','audit:read',
  'test:read','test:write','artifact:read','artifact:write','event:write','run:report',
  'archive:export','webhook:manage','access:manage'));
CREATE TABLE qual.agent_runs (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    source text NOT NULL CHECK(length(source) BETWEEN 1 AND 64),
    external_run_id text NOT NULL CHECK(length(external_run_id) BETWEEN 1 AND 160),
    host text NOT NULL CHECK(length(host) BETWEEN 1 AND 200),
    reporter_principal_id uuid NOT NULL,
    registration_credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    source_status text NOT NULL DEFAULT 'queued'
      CHECK(source_status IN ('queued','running','blocked','succeeded','failed','cancelled')),
    version bigint NOT NULL DEFAULT 1 CHECK(version >= 1),
    report_sequence bigint NOT NULL DEFAULT 0 CHECK(report_sequence >= 0),
    last_status_occurred_at timestamptz,
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY(project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY(workspace_id,reporter_principal_id) REFERENCES qual.principals(workspace_id,id),
    UNIQUE(project_id,source,external_run_id),
    UNIQUE(project_id,id)
);
CREATE INDEX agent_runs_work_idx ON qual.agent_runs(project_id,work_item_id,created_at DESC,id DESC);

CREATE TABLE qual.agent_run_reports (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    agent_run_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK(sequence>=1),
    source_report_id text NOT NULL CHECK(length(source_report_id) BETWEEN 1 AND 160),
    report_hash bytea NOT NULL CHECK(octet_length(report_hash)=32),
    kind text NOT NULL CHECK(kind IN ('status','heartbeat')),
    source_status text CHECK(source_status IN ('queued','running','blocked','succeeded','failed','cancelled')),
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    applied boolean NOT NULL,
    run_version bigint NOT NULL CHECK(run_version>=1),
    reporter_principal_id uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    error_reason text NOT NULL DEFAULT '' CHECK(length(error_reason)<=500),
    evidence_artifact_id uuid,
    external_reference_id uuid,
    FOREIGN KEY(workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY(project_id,agent_run_id) REFERENCES qual.agent_runs(project_id,id),
    FOREIGN KEY(workspace_id,reporter_principal_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY(project_id,evidence_artifact_id) REFERENCES qual.artifacts(project_id,id),
    FOREIGN KEY(project_id,external_reference_id) REFERENCES qual.external_references(project_id,id),
    UNIQUE(agent_run_id,sequence),
    UNIQUE(agent_run_id,source_report_id),
    CHECK ((kind='status' AND source_status IS NOT NULL) OR (kind='heartbeat' AND source_status IS NULL))
);
CREATE INDEX agent_run_reports_order_idx ON qual.agent_run_reports(project_id,agent_run_id,sequence DESC);
CREATE TRIGGER agent_run_reports_append_only BEFORE UPDATE OR DELETE ON qual.agent_run_reports
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
-- Destructive rollback is forbidden once an external fact has been reported.
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM qual.agent_runs) THEN
   RAISE EXCEPTION 'agent run history must be retained; disable routes instead of dropping facts';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER agent_run_reports_append_only ON qual.agent_run_reports;
DROP TABLE qual.agent_run_reports;
DROP TABLE qual.agent_runs;
ALTER TABLE qual.project_permission_overrides
 DROP CONSTRAINT project_permission_overrides_permission_check;
ALTER TABLE qual.project_permission_overrides
 ADD CONSTRAINT project_permission_overrides_permission_check CHECK(permission IN (
  'work:read','work:write','work:claim','work:review','audit:read',
  'test:read','test:write','artifact:read','artifact:write','event:write',
  'archive:export','webhook:manage','access:manage'));
