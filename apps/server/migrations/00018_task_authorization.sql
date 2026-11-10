-- +goose Up
-- QUAL-55: project-local task authorization mode, legacy unless explicitly opted in.
-- No existing principal, credential, WorkItem or permission is modified.
CREATE TABLE qual.project_task_authorization_policies (
    workspace_id uuid NOT NULL,
    project_id uuid PRIMARY KEY,
    mode text NOT NULL CHECK (mode IN ('legacy','task_scoped')),
    version bigint NOT NULL CHECK (version >= 1),
    changed_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,changed_by) REFERENCES qual.principals(workspace_id,id)
);
CREATE TABLE qual.task_authorization_policy_changes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    old_mode text NOT NULL CHECK (old_mode IN ('legacy','task_scoped')),
    new_mode text NOT NULL CHECK (new_mode IN ('legacy','task_scoped')),
    old_version bigint NOT NULL CHECK (old_version >= 0),
    new_version bigint NOT NULL CHECK (new_version = old_version + 1),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 16 AND 2000),
    changed_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,actor_id) REFERENCES qual.principals(workspace_id,id)
);
CREATE INDEX task_authorization_changes_idx ON qual.task_authorization_policy_changes(project_id, changed_at DESC, id);
CREATE TRIGGER task_authorization_changes_append_only
 BEFORE UPDATE OR DELETE ON qual.task_authorization_policy_changes
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'Goose18 task authorization policy has immutable history; destructive Down prohibited';
END
$$;
-- +goose StatementEnd
