-- +goose Up
ALTER TABLE qual.projects
    ADD COLUMN next_test_case_number bigint NOT NULL DEFAULT 1
    CHECK (next_test_case_number > 0);

CREATE TABLE qual.test_suites (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 160),
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (workspace_id,created_by) REFERENCES qual.principals(workspace_id,id),
    UNIQUE (project_id,name),
    UNIQUE (project_id,id)
);

CREATE TABLE qual.test_cases (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    number bigint NOT NULL CHECK(number > 0),
    key text NOT NULL,
    suite_id uuid,
    draft jsonb NOT NULL CHECK (jsonb_typeof(draft) = 'object'),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    published_version_id uuid,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (workspace_id,project_id) REFERENCES qual.projects(workspace_id,id),
    FOREIGN KEY (project_id,suite_id) REFERENCES qual.test_suites(project_id,id),
    FOREIGN KEY (workspace_id,created_by) REFERENCES qual.principals(workspace_id,id),
    UNIQUE(project_id,number), UNIQUE(workspace_id,key), UNIQUE(project_id,id)
);
CREATE INDEX test_cases_project_suite_idx ON qual.test_cases(project_id,suite_id,updated_at DESC);

CREATE TABLE qual.test_case_versions (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL,
    test_case_id uuid NOT NULL,
    version_no integer NOT NULL CHECK(version_no > 0),
    source_revision bigint NOT NULL CHECK(source_revision > 0),
    specification jsonb NOT NULL CHECK (jsonb_typeof(specification)='object'),
    sha256 bytea NOT NULL CHECK(octet_length(sha256)=32),
    published_by uuid NOT NULL REFERENCES qual.principals(id),
    published_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (project_id,test_case_id) REFERENCES qual.test_cases(project_id,id),
    UNIQUE(test_case_id,version_no),
    UNIQUE(test_case_id,source_revision),
    UNIQUE(project_id,id),
    UNIQUE(test_case_id,id)
);
CREATE INDEX test_case_versions_latest_idx ON qual.test_case_versions(test_case_id,version_no DESC);
ALTER TABLE qual.test_cases ADD CONSTRAINT test_cases_published_version_fk
    FOREIGN KEY (id,published_version_id) REFERENCES qual.test_case_versions(test_case_id,id);

CREATE TABLE qual.test_case_coverage (
    project_id uuid NOT NULL,
    test_case_id uuid NOT NULL,
    work_item_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(project_id,test_case_id,work_item_id),
    FOREIGN KEY (project_id,test_case_id) REFERENCES qual.test_cases(project_id,id),
    FOREIGN KEY (project_id,work_item_id) REFERENCES qual.work_items(project_id,id)
);

CREATE TABLE qual.test_runs (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES qual.projects(id),
    name text NOT NULL CHECK(length(trim(name)) BETWEEN 1 AND 240),
    source_principal_id uuid NOT NULL REFERENCES qual.principals(id),
    source_system text NOT NULL DEFAULT 'manual' CHECK(length(trim(source_system)) BETWEEN 1 AND 80),
    external_run_key text CHECK(length(external_run_key) BETWEEN 1 AND 240),
    environment text NOT NULL DEFAULT '',
    build_ref text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(project_id,id),
    UNIQUE(project_id,source_principal_id,source_system,external_run_key)
);
CREATE INDEX test_runs_project_idx ON qual.test_runs(project_id,created_at DESC,id DESC);

CREATE TABLE qual.test_run_items (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL,
    run_id uuid NOT NULL,
    test_case_id uuid NOT NULL,
    test_case_version_id uuid NOT NULL,
    snapshot_sha256 bytea NOT NULL CHECK(octet_length(snapshot_sha256)=32),
    next_attempt_no integer NOT NULL DEFAULT 1 CHECK(next_attempt_no > 0),
    FOREIGN KEY (project_id,run_id) REFERENCES qual.test_runs(project_id,id),
    FOREIGN KEY (project_id,test_case_id) REFERENCES qual.test_cases(project_id,id),
    FOREIGN KEY (test_case_id,test_case_version_id) REFERENCES qual.test_case_versions(test_case_id,id),
    UNIQUE(run_id,test_case_id), UNIQUE(project_id,id)
);
CREATE INDEX test_run_items_run_idx ON qual.test_run_items(run_id);

CREATE TABLE qual.test_attempts (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL,
    run_item_id uuid NOT NULL,
    attempt_no integer NOT NULL CHECK(attempt_no > 0),
    status text NOT NULL CHECK(status IN ('passed','failed','blocked','skipped')),
    note text NOT NULL DEFAULT '' CHECK(length(note)<=20000),
    observed_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    reported_by uuid NOT NULL REFERENCES qual.principals(id),
    source_kind text NOT NULL CHECK(source_kind IN ('manual','external','junit')),
    external_attempt_key text CHECK(length(external_attempt_key) BETWEEN 1 AND 240),
    provenance jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(provenance)='object'),
    FOREIGN KEY (project_id,run_item_id) REFERENCES qual.test_run_items(project_id,id),
    UNIQUE(run_item_id,attempt_no), UNIQUE(project_id,id),
    UNIQUE(run_item_id,external_attempt_key)
);
CREATE INDEX test_attempts_latest_idx ON qual.test_attempts(run_item_id,attempt_no DESC);
CREATE INDEX test_attempts_project_status_idx ON qual.test_attempts(project_id,status,ingested_at DESC);

CREATE TABLE qual.attempt_defects (
    project_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    bug_work_item_id uuid NOT NULL,
    linked_by uuid NOT NULL REFERENCES qual.principals(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(project_id,attempt_id,bug_work_item_id),
    FOREIGN KEY (project_id,attempt_id) REFERENCES qual.test_attempts(project_id,id),
    FOREIGN KEY (project_id,bug_work_item_id) REFERENCES qual.work_items(project_id,id)
);

CREATE TABLE qual.test_report_imports (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL,
    run_id uuid NOT NULL,
    reported_by uuid NOT NULL REFERENCES qual.principals(id),
    filename text NOT NULL,
    report_sha256 bytea NOT NULL CHECK(octet_length(report_sha256)=32),
    raw_xml bytea NOT NULL CHECK(octet_length(raw_xml) BETWEEN 1 AND 1048576),
    case_count integer NOT NULL CHECK(case_count BETWEEN 1 AND 500),
    imported_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (project_id,run_id) REFERENCES qual.test_runs(project_id,id)
);

-- +goose StatementBegin
CREATE FUNCTION qual.reject_testing_history_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'immutable testing history: %', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER no_change_case_versions BEFORE UPDATE OR DELETE ON qual.test_case_versions
  FOR EACH ROW EXECUTE FUNCTION qual.reject_testing_history_mutation();
CREATE TRIGGER no_change_test_run_items BEFORE UPDATE OF test_case_id,test_case_version_id,snapshot_sha256,run_id,project_id OR DELETE ON qual.test_run_items
  FOR EACH ROW EXECUTE FUNCTION qual.reject_testing_history_mutation();
CREATE TRIGGER no_change_test_attempts BEFORE UPDATE OR DELETE ON qual.test_attempts
  FOR EACH ROW EXECUTE FUNCTION qual.reject_testing_history_mutation();
CREATE TRIGGER no_change_attempt_defects BEFORE UPDATE OR DELETE ON qual.attempt_defects
  FOR EACH ROW EXECUTE FUNCTION qual.reject_testing_history_mutation();
CREATE TRIGGER no_change_test_report_imports BEFORE UPDATE OR DELETE ON qual.test_report_imports
  FOR EACH ROW EXECUTE FUNCTION qual.reject_testing_history_mutation();

-- +goose Down
DROP TABLE qual.test_report_imports;
DROP TABLE qual.attempt_defects;
DROP TABLE qual.test_attempts;
DROP TABLE qual.test_run_items;
DROP TABLE qual.test_runs;
DROP TABLE qual.test_case_coverage;
ALTER TABLE qual.test_cases DROP CONSTRAINT test_cases_published_version_fk;
DROP TABLE qual.test_case_versions;
DROP TABLE qual.test_cases;
DROP TABLE qual.test_suites;
DROP FUNCTION qual.reject_testing_history_mutation();
ALTER TABLE qual.projects DROP COLUMN next_test_case_number;
