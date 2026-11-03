-- +goose Up
-- WorkItem is the shared Task/Bug aggregate; histories are append-only.
CREATE TABLE qual.work_items (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    number bigint NOT NULL CHECK (number > 0),
    key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('task','bug')),
    title text NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 240),
    description text NOT NULL DEFAULT '',
    acceptance_criteria jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(acceptance_criteria) = 'array'),
    status text NOT NULL DEFAULT 'backlog'
        CHECK (status IN ('backlog','todo','in_progress','in_review','done','cancelled')),
    priority text NOT NULL DEFAULT 'medium'
        CHECK (priority IN ('urgent','high','medium','low')),
    assignee_id uuid,
    accountable_owner_id uuid,
    created_by uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id, assignee_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (workspace_id, accountable_owner_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (workspace_id, created_by) REFERENCES qual.principals(workspace_id,id),
    UNIQUE (project_id, number),
    UNIQUE (workspace_id, key),
    UNIQUE (project_id, id)
);
CREATE INDEX work_items_board_idx ON qual.work_items(project_id, status, priority, updated_at DESC);
CREATE INDEX work_items_assignee_idx ON qual.work_items(project_id, assignee_id, status);

CREATE TABLE qual.bug_details (
    work_item_id uuid PRIMARY KEY REFERENCES qual.work_items(id),
    severity text NOT NULL DEFAULT 'major'
        CHECK (severity IN ('blocker','critical','major','minor','trivial')),
    reproduction_steps text NOT NULL DEFAULT '',
    expected_behavior text NOT NULL DEFAULT '',
    actual_behavior text NOT NULL DEFAULT '',
    environment text NOT NULL DEFAULT ''
);

CREATE TABLE qual.work_comments (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    author_id uuid NOT NULL,
    body text NOT NULL CHECK (length(trim(body)) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id, work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (workspace_id, author_id) REFERENCES qual.principals(workspace_id,id)
);
CREATE INDEX work_comments_work_idx ON qual.work_comments(work_item_id,created_at,id);

CREATE TABLE qual.work_dependencies (
    project_id uuid NOT NULL,
    from_item_id uuid NOT NULL,
    to_item_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id,from_item_id,to_item_id),
    FOREIGN KEY (project_id,from_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (project_id,to_item_id) REFERENCES qual.work_items(project_id,id),
    CHECK (from_item_id <> to_item_id)
);

CREATE TABLE qual.audit_records (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    credential_id uuid NOT NULL,
    request_id text NOT NULL,
    operation text NOT NULL,
    entity_id uuid NOT NULL,
    old_version bigint,
    new_version bigint NOT NULL,
    diff jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,actor_id) REFERENCES qual.principals(workspace_id,id),
    FOREIGN KEY (credential_id) REFERENCES qual.credentials(id)
);
CREATE INDEX audit_project_idx ON qual.audit_records(project_id,occurred_at DESC,id);
CREATE INDEX audit_entity_idx ON qual.audit_records(entity_id,occurred_at DESC);

CREATE TABLE qual.activity_records (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES qual.projects(id),
    audit_id uuid NOT NULL UNIQUE REFERENCES qual.audit_records(id),
    actor_id uuid NOT NULL REFERENCES qual.principals(id),
    operation text NOT NULL,
    entity_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activity_project_idx ON qual.activity_records(project_id,occurred_at DESC,id);

CREATE TABLE qual.outbox_events (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES qual.projects(id),
    audit_id uuid NOT NULL UNIQUE REFERENCES qual.audit_records(id),
    event_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    delivered_at timestamptz
);
CREATE INDEX outbox_pending_idx ON qual.outbox_events(created_at)
    WHERE delivered_at IS NULL;

CREATE TABLE qual.idempotency_records (
    project_id uuid NOT NULL REFERENCES qual.projects(id),
    actor_id uuid NOT NULL REFERENCES qual.principals(id),
    operation text NOT NULL,
    request_key text NOT NULL CHECK (length(request_key) BETWEEN 1 AND 128),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response_json jsonb NOT NULL DEFAULT '{}'::jsonb,
    response_status integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id,actor_id,operation,request_key)
);

-- Append-only audit/activity: application roles cannot edit already emitted history.
-- +goose StatementBegin
CREATE FUNCTION qual.reject_history_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'immutable history: %', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER no_modify_audit BEFORE UPDATE OR DELETE ON qual.audit_records
    FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();
CREATE TRIGGER no_modify_activity BEFORE UPDATE OR DELETE ON qual.activity_records
    FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
DROP TRIGGER no_modify_activity ON qual.activity_records;
DROP TRIGGER no_modify_audit ON qual.audit_records;
DROP FUNCTION qual.reject_history_mutation();
DROP TABLE qual.idempotency_records;
DROP TABLE qual.outbox_events;
DROP TABLE qual.activity_records;
DROP TABLE qual.audit_records;
DROP TABLE qual.work_dependencies;
DROP TABLE qual.work_comments;
DROP TABLE qual.bug_details;
DROP TABLE qual.work_items;
