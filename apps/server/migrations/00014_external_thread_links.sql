-- +goose Up
-- QUAL-29: explicit project-local source identities; legacy references remain untouched.
CREATE TABLE qual.external_threads (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  provider text NOT NULL CHECK(provider ~ '^[a-z][a-z0-9_-]{0,59}$'),
  thread_kind text NOT NULL CHECK(thread_kind ~ '^[a-z][a-z0-9_-]{0,59}$'),
  external_thread_id text NOT NULL CHECK(
    length(external_thread_id) BETWEEN 1 AND 240 AND
    external_thread_id = btrim(external_thread_id) AND
    external_thread_id !~ '[[:cntrl:]]' AND
    -- Scoped source identity is required even for direct SQL writes.
    external_thread_id ~ '^[^/[:space:]]+(/[^/[:space:]]+)+$' AND
    (provider <> 'github' OR thread_kind NOT IN ('pull_request','issue') OR
      (thread_kind = 'pull_request' AND external_thread_id ~ '^[^/]+/[^/]+/pull/[1-9][0-9]*$') OR
      (thread_kind = 'issue' AND external_thread_id ~ '^[^/]+/[^/]+/issues/[1-9][0-9]*$'))
  ),
  url text CHECK(url IS NULL OR (length(url) BETWEEN 9 AND 2048 AND url LIKE 'https://%')),
  created_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY(workspace_id, project_id) REFERENCES qual.projects(workspace_id,id),
  FOREIGN KEY(workspace_id, created_by) REFERENCES qual.principals(workspace_id,id),
  UNIQUE(project_id, provider, thread_kind, external_thread_id),
  UNIQUE(project_id,id)
);
CREATE INDEX external_threads_project_created_idx ON qual.external_threads(project_id,created_at DESC,id);

-- Historical rows persist after unlink, with a partial uniqueness constraint on active links.
CREATE TABLE qual.work_thread_links (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  thread_id uuid NOT NULL,
  work_item_id uuid NOT NULL,
  source_reference_id uuid,
  created_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  removed_by uuid,
  removed_at timestamptz,
  FOREIGN KEY(workspace_id, project_id) REFERENCES qual.projects(workspace_id,id),
  FOREIGN KEY(project_id, thread_id) REFERENCES qual.external_threads(project_id,id),
  FOREIGN KEY(project_id, work_item_id) REFERENCES qual.work_items(project_id,id),
  FOREIGN KEY(project_id, source_reference_id) REFERENCES qual.external_references(project_id,id),
  FOREIGN KEY(workspace_id, created_by) REFERENCES qual.principals(workspace_id,id),
  FOREIGN KEY(workspace_id, removed_by) REFERENCES qual.principals(workspace_id,id),
  CHECK ((removed_by IS NULL) = (removed_at IS NULL)),
  UNIQUE(project_id,id)
);
CREATE UNIQUE INDEX work_thread_links_active_unique ON qual.work_thread_links(project_id,thread_id,work_item_id) WHERE removed_at IS NULL;
CREATE INDEX work_thread_links_item_active_idx ON qual.work_thread_links(project_id,work_item_id,id) WHERE removed_at IS NULL;
CREATE INDEX work_thread_links_thread_active_idx ON qual.work_thread_links(project_id,thread_id,id) WHERE removed_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION qual.protect_external_thread() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'immutable external thread identity';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER external_thread_immutable BEFORE UPDATE OR DELETE ON qual.external_threads
  FOR EACH ROW EXECUTE FUNCTION qual.protect_external_thread();

-- +goose StatementBegin
CREATE FUNCTION qual.protect_work_thread_link() RETURNS trigger AS $$
BEGIN
  IF TG_OP='DELETE' THEN
    RAISE EXCEPTION 'immutable link history: physical delete forbidden';
  END IF;
  IF OLD.removed_at IS NOT NULL OR NEW.removed_at IS NULL OR NEW.removed_by IS NULL
     OR ROW(NEW.id,NEW.workspace_id,NEW.project_id,NEW.thread_id,NEW.work_item_id,
            NEW.source_reference_id,NEW.created_by,NEW.created_at)
        IS DISTINCT FROM
        ROW(OLD.id,OLD.workspace_id,OLD.project_id,OLD.thread_id,OLD.work_item_id,
            OLD.source_reference_id,OLD.created_by,OLD.created_at) THEN
    RAISE EXCEPTION 'immutable link history: only first unlink allowed';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER work_thread_link_protect BEFORE UPDATE OR DELETE ON qual.work_thread_links
  FOR EACH ROW EXECUTE FUNCTION qual.protect_work_thread_link();

-- +goose Down
-- No destructive rollback: links and their provenance are project history.
-- +goose StatementBegin
DO $$
BEGIN
  RAISE EXCEPTION 'Goose14 down prohibited: external thread/link history must not be deleted; disable routes and preserve tables';
END;
$$;
-- +goose StatementEnd
