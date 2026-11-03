-- +goose Up
-- Identity and workspace rows. Principal, token and memberships are separate.
CREATE TABLE qual.workspaces (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][a-z0-9-]{1,47}$'),
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 160),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE qual.projects (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES qual.workspaces(id),
    key text NOT NULL CHECK (key ~ '^[A-Z][A-Z0-9]{1,15}$'),
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 160),
    next_work_item_number bigint NOT NULL DEFAULT 1 CHECK (next_work_item_number > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, key),
    UNIQUE (workspace_id, id)
);

CREATE TABLE qual.principals (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES qual.workspaces(id),
    kind text NOT NULL CHECK (kind IN ('human', 'agent', 'integration')),
    display_name text NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 160),
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE qual.memberships (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES qual.workspaces(id),
    project_id uuid,
    principal_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('workspace_admin', 'project_maintainer', 'project_member', 'project_viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, principal_id)
        REFERENCES qual.principals(workspace_id, id),
    FOREIGN KEY (workspace_id, project_id)
        REFERENCES qual.projects(workspace_id, id),
    CONSTRAINT memberships_role_scope CHECK (
        (role = 'workspace_admin' AND project_id IS NULL)
        OR (role <> 'workspace_admin' AND project_id IS NOT NULL)
    ),
    UNIQUE NULLS NOT DISTINCT (principal_id, project_id)
);
CREATE INDEX memberships_project_principal_idx
    ON qual.memberships (project_id, principal_id);

CREATE TABLE qual.credentials (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    token_sha256 bytea NOT NULL UNIQUE CHECK (octet_length(token_sha256) = 32),
    scopes text[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 16),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id, principal_id)
        REFERENCES qual.principals(workspace_id, id),
    CHECK (expires_at > created_at)
);
CREATE INDEX credentials_principal_idx ON qual.credentials (principal_id);

-- +goose Down
DROP TABLE qual.credentials;
DROP TABLE qual.memberships;
DROP TABLE qual.principals;
DROP TABLE qual.projects;
DROP TABLE qual.workspaces;
