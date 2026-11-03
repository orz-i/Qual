-- +goose Up
-- QUAL-21 Stage B: store explicit review-policy and acceptance facts without
-- rewriting historical done WorkItems, role entries or any Audit history.
-- Existing projects get conservative effective defaults in the application;
-- migration never bulk-updates WorkItem state or grants permissions.

CREATE TABLE qual.project_review_policies (
    workspace_id uuid NOT NULL,
    project_id uuid PRIMARY KEY,
    default_mode text NOT NULL DEFAULT 'independent_review'
        CHECK (default_mode IN ('independent_review','owner_acceptance')),
    owner_acceptance_allowed boolean NOT NULL DEFAULT false,
    force_independent_for_high_risk boolean NOT NULL DEFAULT true,
    version bigint NOT NULL CHECK (version >= 1),
    updated_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,updated_by) REFERENCES qual.principals(workspace_id,id),
    CHECK (default_mode <> 'owner_acceptance' OR owner_acceptance_allowed)
);

CREATE TABLE qual.work_review_policy_overrides (
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('independent_review','owner_acceptance')),
    version bigint NOT NULL CHECK (version >= 1),
    changed_by uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id,work_item_id),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (workspace_id,changed_by) REFERENCES qual.principals(workspace_id,id)
);

-- Every project- and WorkItem-level policy change is retained separately.
CREATE TABLE qual.review_policy_changes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid,
    actor_id uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    old_mode text NOT NULL CHECK (old_mode IN ('independent_review','owner_acceptance')),
    new_mode text NOT NULL CHECK (new_mode IN ('independent_review','owner_acceptance')),
    old_owner_acceptance_allowed boolean NOT NULL,
    new_owner_acceptance_allowed boolean NOT NULL,
    old_force_independent_for_high_risk boolean NOT NULL,
    new_force_independent_for_high_risk boolean NOT NULL,
    old_version bigint NOT NULL CHECK (old_version >= 0),
    new_version bigint NOT NULL CHECK (new_version=old_version+1),
    reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 16 AND 2000),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (workspace_id,actor_id) REFERENCES qual.principals(workspace_id,id)
);
CREATE INDEX review_policy_changes_project_idx
 ON qual.review_policy_changes(project_id,occurred_at DESC,id DESC);
CREATE TRIGGER review_policy_changes_append_only
 BEFORE UPDATE OR DELETE ON qual.review_policy_changes
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- The only authoritative accepted completion is an append-only decision
-- written in the SAME transaction as WorkItem Done, Audit, Activity, Outbox
-- and Idempotency. Legacy done records deliberately have no matching row.
CREATE TABLE qual.work_acceptance_decisions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    credential_id uuid NOT NULL REFERENCES qual.credentials(id),
    acceptance_mode text NOT NULL
        CHECK (acceptance_mode IN ('independent_review','owner_acceptance')),
    policy_source text NOT NULL CHECK (policy_source IN ('project','work_item')),
    policy_version bigint NOT NULL CHECK (policy_version >= 1),
    reviewer_entry_id uuid REFERENCES qual.work_role_entries(id),
    independent boolean NOT NULL,
    explicit_self_review boolean NOT NULL,
    reason text NOT NULL DEFAULT ''
        CHECK (length(reason) <= 2000),
    reference_kind text NOT NULL
        CHECK (reference_kind IN ('test_run','artifact')),
    reference_value text NOT NULL CHECK (length(reference_value)=36),
    work_version_before bigint NOT NULL CHECK (work_version_before >= 1),
    work_version_after bigint NOT NULL
        CHECK (work_version_after=work_version_before+1),
    request_id text NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    accepted_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id),
    FOREIGN KEY (workspace_id,actor_id) REFERENCES qual.principals(workspace_id,id),
    UNIQUE(project_id,work_item_id),
    CHECK (
        (acceptance_mode='independent_review' AND independent=true
         AND explicit_self_review=false AND reviewer_entry_id IS NOT NULL)
        OR
        (acceptance_mode='owner_acceptance' AND independent=false
         AND explicit_self_review=true AND reviewer_entry_id IS NULL
         AND length(trim(reason)) >= 16)
    )
);
CREATE INDEX acceptance_decisions_project_idx
 ON qual.work_acceptance_decisions(project_id,accepted_at DESC,id DESC);
CREATE TRIGGER acceptance_decisions_append_only
 BEFORE UPDATE OR DELETE ON qual.work_acceptance_decisions
 FOR EACH ROW EXECUTE FUNCTION qual.reject_history_mutation();

-- +goose Down
-- Refuse destructive rollback once any genuine review-policy or acceptance
-- history exists. A live rollout with decisions must use an approved
-- forward-compatible binary/schema recovery, never drop immutable evidence.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM qual.work_acceptance_decisions)
       OR EXISTS (SELECT 1 FROM qual.review_policy_changes)
       OR EXISTS (SELECT 1 FROM qual.work_review_policy_overrides)
       OR EXISTS (SELECT 1 FROM qual.project_review_policies) THEN
        RAISE EXCEPTION 'Goose13 Down refused: review governance records exist; use forward-compatible recovery';
    END IF;
END
$$;
-- +goose StatementEnd
DROP TRIGGER acceptance_decisions_append_only ON qual.work_acceptance_decisions;
DROP TABLE qual.work_acceptance_decisions;
DROP TRIGGER review_policy_changes_append_only ON qual.review_policy_changes;
DROP TABLE qual.review_policy_changes;
DROP TABLE qual.work_review_policy_overrides;
DROP TABLE qual.project_review_policies;
