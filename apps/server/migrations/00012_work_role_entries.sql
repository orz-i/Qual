-- +goose Up
-- Role/capacity is a labeled event from one real authenticated Principal,
-- never an alternate Agent identity. Records are immutable.
CREATE TABLE qual.work_role_entries (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    credential_id uuid NOT NULL,
    capacity text NOT NULL CHECK(capacity IN ('executor','tester','reviewer','auditor')),
    outcome text NOT NULL CHECK(outcome IN ('observed','passed','failed','needs_work')),
    summary text NOT NULL CHECK(length(trim(summary)) BETWEEN 1 AND 5000),
    reference_kind text NOT NULL CHECK(reference_kind IN ('none','git_commit','test_run','artifact')),
    reference_value text NOT NULL CHECK(length(reference_value) <= 128),
    work_version bigint NOT NULL CHECK(work_version >= 1),
    work_status text NOT NULL CHECK(work_status IN ('backlog','todo','in_progress','in_review','done','cancelled')),
    self_review boolean NOT NULL DEFAULT false,
    request_id text NOT NULL CHECK(length(request_id) BETWEEN 1 AND 128),
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((reference_kind='none' AND reference_value='') OR (reference_kind<>'none' AND length(reference_value)>0)),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (workspace_id,actor_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (credential_id) REFERENCES qual.credentials(id)
);
CREATE INDEX role_entries_work_idx ON qual.work_role_entries(project_id,work_item_id,recorded_at DESC,id DESC);
CREATE INDEX role_entries_principal_idx ON qual.work_role_entries(project_id,actor_id,recorded_at DESC);
CREATE TRIGGER role_entries_append_only BEFORE UPDATE OR DELETE ON qual.work_role_entries
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
DROP TRIGGER role_entries_append_only ON qual.work_role_entries;
DROP TABLE qual.work_role_entries;
