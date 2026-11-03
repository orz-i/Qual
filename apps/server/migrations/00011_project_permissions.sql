-- +goose Up
-- Effective project permissions = valid principal + active credential Scope
-- + project membership + (explicit override or conservative role defaults).
-- No migration automatically grants an existing Agent privileged abilities.
CREATE TABLE qual.project_permission_overrides (
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    permission text NOT NULL CHECK (permission IN (
      'work:read','work:write','work:claim','work:review','audit:read',
      'test:read','test:write','artifact:read','artifact:write',
      'archive:export','webhook:manage','access:manage'
    )),
    effect text NOT NULL CHECK (effect IN ('inherit','allow','deny')),
    version bigint NOT NULL CHECK (version >= 1),
    changed_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id,principal_id,permission),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,principal_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (workspace_id,changed_by) REFERENCES qual.principals(workspace_id,id),
    UNIQUE (workspace_id,project_id,principal_id,permission)
);
CREATE INDEX permission_overrides_principal_idx
 ON qual.project_permission_overrides(workspace_id,principal_id,project_id);

CREATE TABLE qual.project_permission_changes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    permission text NOT NULL,
    before_effect text NOT NULL CHECK (before_effect IN ('inherit','allow','deny')),
    after_effect text NOT NULL CHECK (after_effect IN ('inherit','allow','deny')),
    old_version bigint NOT NULL CHECK (old_version >= 0),
    new_version bigint NOT NULL CHECK (new_version = old_version + 1),
    changed_by uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 3 AND 500),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,principal_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (workspace_id,changed_by) REFERENCES qual.principals(workspace_id,id),
    UNIQUE(project_id,principal_id,permission,new_version)
);
CREATE INDEX permission_changes_project_idx ON qual.project_permission_changes(project_id,occurred_at DESC,id);
CREATE TRIGGER permission_changes_append_only
 BEFORE UPDATE OR DELETE ON qual.project_permission_changes
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
DROP TRIGGER permission_changes_append_only ON qual.project_permission_changes;
DROP TABLE qual.project_permission_changes;
DROP TABLE qual.project_permission_overrides;
