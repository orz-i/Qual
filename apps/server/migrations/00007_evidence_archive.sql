-- +goose Up
-- Attachment bytes are stored in private S3, never in this database.
CREATE TABLE qual.artifacts (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  target_kind text NOT NULL CHECK(target_kind IN ('work_item','test_case','test_run','test_attempt')),
  target_id uuid NOT NULL,
  object_key text NOT NULL UNIQUE,
  filename text NOT NULL CHECK(length(filename) BETWEEN 1 AND 160),
  media_type text NOT NULL CHECK(length(media_type) BETWEEN 1 AND 120),
  size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 10485760),
  sha256 bytea NOT NULL CHECK(octet_length(sha256)=32),
  state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','uploaded','ready','failed')),
  created_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  uploaded_at timestamptz,
  ready_at timestamptz,
  failure_reason text,
  FOREIGN KEY(workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
  FOREIGN KEY(workspace_id,created_by) REFERENCES qual.principals(workspace_id,id),
  UNIQUE(project_id,id),
  CHECK((state <> 'ready' OR (uploaded_at IS NOT NULL AND ready_at IS NOT NULL)) AND
        (state <> 'uploaded' OR uploaded_at IS NOT NULL))
);
CREATE INDEX artifacts_target_idx ON qual.artifacts(project_id,target_kind,target_id,created_at DESC);
CREATE INDEX artifacts_pending_idx ON qual.artifacts(created_at) WHERE state IN ('pending','failed');

CREATE TABLE qual.external_references (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES qual.projects(id),
  target_kind text NOT NULL CHECK(target_kind IN ('work_item','test_case','test_run','test_attempt')),
  target_id uuid NOT NULL,
  provider text NOT NULL CHECK(length(trim(provider)) BETWEEN 1 AND 60),
  kind text NOT NULL CHECK(length(trim(kind)) BETWEEN 1 AND 60),
  external_id text NOT NULL CHECK(length(trim(external_id)) BETWEEN 1 AND 240),
  url text NOT NULL CHECK(length(url) BETWEEN 8 AND 2048),
  created_by uuid NOT NULL REFERENCES qual.principals(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id,provider,kind,external_id),
  UNIQUE(project_id,id)
);
CREATE INDEX external_refs_target_idx ON qual.external_references(project_id,target_kind,target_id);

CREATE TABLE qual.archive_exports (
  id uuid PRIMARY KEY,
  project_id uuid NOT NULL REFERENCES qual.projects(id),
  requested_by uuid NOT NULL REFERENCES qual.principals(id),
  object_key text NOT NULL UNIQUE,
  archive_sha256 bytea NOT NULL CHECK(octet_length(archive_sha256)=32),
  size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 33554432),
  manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object'),
  snapshot_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(project_id,id)
);
CREATE INDEX archive_exports_project_idx ON qual.archive_exports(project_id,created_at DESC,id DESC);

-- +goose StatementBegin
CREATE FUNCTION qual.reject_archive_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'immutable project export: %',TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER archive_immutable BEFORE UPDATE OR DELETE ON qual.archive_exports
  FOR EACH ROW EXECUTE FUNCTION qual.reject_archive_mutation();

-- +goose Down
DROP TRIGGER archive_immutable ON qual.archive_exports;
DROP FUNCTION qual.reject_archive_mutation();
DROP TABLE qual.archive_exports;
DROP TABLE qual.external_references;
DROP TABLE qual.artifacts;
