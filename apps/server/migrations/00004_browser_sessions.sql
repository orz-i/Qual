-- +goose Up
-- Controlled loopback browser sign-in only. No password database or public signup.
CREATE TABLE qual.browser_login_codes (
    id uuid PRIMARY KEY,
    principal_id uuid NOT NULL REFERENCES qual.principals(id),
    code_hash bytea NOT NULL UNIQUE CHECK (octet_length(code_hash)=32),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE INDEX browser_login_codes_expiration_idx ON qual.browser_login_codes(expires_at)
    WHERE used_at IS NULL;
CREATE TABLE qual.browser_sessions (
    credential_id uuid PRIMARY KEY REFERENCES qual.credentials(id),
    principal_id uuid NOT NULL REFERENCES qual.principals(id),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE INDEX browser_sessions_principal_idx ON qual.browser_sessions(principal_id);

-- +goose Down
DROP TABLE qual.browser_sessions;
DROP TABLE qual.browser_login_codes;
