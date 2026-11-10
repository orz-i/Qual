-- +goose Up
-- QUAL-57: active independent reviewer assignments are *current* relations.
-- Historical review and actor facts remain in append-only Work Role/Audit.
-- No existing WorkItem, Principal, Credential or RoleEntry is rewritten.
CREATE TABLE qual.work_review_assignments (
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    reviewer_id uuid NOT NULL,
    claimed_work_version bigint NOT NULL CHECK (claimed_work_version >= 2),
    claimed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, work_item_id),
    FOREIGN KEY (workspace_id, project_id) REFERENCES qual.projects(workspace_id, id),
    FOREIGN KEY (project_id, work_item_id) REFERENCES qual.work_items(project_id, id),
    FOREIGN KEY (workspace_id, reviewer_id) REFERENCES qual.principals(workspace_id, id)
);
CREATE INDEX work_review_assignments_reviewer_idx
    ON qual.work_review_assignments(project_id, reviewer_id, claimed_at DESC);

-- +goose Down
-- Historical audit references an active relationship's life cycle; do not
-- destroy schema without an explicitly reviewed forward-only migration.
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'Goose19 active review assignment history cannot be destructively dropped';
END
$$;
-- +goose StatementEnd
