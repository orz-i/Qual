-- +goose Up
-- Reserve an owned namespace for Qual domain tables added in Phase 2+.
CREATE SCHEMA IF NOT EXISTS qual;
COMMENT ON SCHEMA qual IS 'Qual domain tables, owned and migrated by the Qual service';

-- +goose Down
DROP SCHEMA IF EXISTS qual RESTRICT;
