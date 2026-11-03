-- +goose Up
-- Subscription secret is AES-256-GCM encrypted with deployment key (never plain SQL).
CREATE TABLE qual.webhook_subscriptions (
 id uuid PRIMARY KEY,
 project_id uuid NOT NULL REFERENCES qual.projects(id),
 endpoint text NOT NULL CHECK(length(endpoint) BETWEEN 12 AND 2048),
 event_types text[] NOT NULL CHECK(cardinality(event_types) BETWEEN 1 AND 32),
 secret_nonce bytea NOT NULL CHECK(octet_length(secret_nonce)=12),
 secret_ciphertext bytea NOT NULL CHECK(octet_length(secret_ciphertext)>=32),
 created_by uuid NOT NULL REFERENCES qual.principals(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 disabled_at timestamptz,
 UNIQUE(project_id,id)
);
CREATE INDEX webhook_active_project_idx ON qual.webhook_subscriptions(project_id,created_at) WHERE disabled_at IS NULL;

CREATE TABLE qual.webhook_deliveries (
 id uuid PRIMARY KEY,
 project_id uuid NOT NULL,
 subscription_id uuid NOT NULL,
 outbox_event_id uuid NOT NULL REFERENCES qual.outbox_events(id),
 status text NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','retrying','processing','delivered','dead','cancelled')),
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 5),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_until timestamptz,
 last_http_status integer,
 last_error_code text,
 delivered_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(project_id,subscription_id) REFERENCES qual.webhook_subscriptions(project_id,id),
 UNIQUE(subscription_id,outbox_event_id),
 UNIQUE(project_id,id)
);
CREATE INDEX webhook_due_idx ON qual.webhook_deliveries(next_attempt_at,id)
 WHERE status IN ('queued','retrying','processing');
CREATE INDEX webhook_project_history_idx ON qual.webhook_deliveries(project_id,created_at DESC,id DESC);

-- +goose Down
DROP TABLE qual.webhook_deliveries;
DROP TABLE qual.webhook_subscriptions;
