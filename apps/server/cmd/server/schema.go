package main

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Goose14 is provided by QUAL-29, Goose15 by QUAL-27 context indexes, and
// Goose16 by QUAL-32 external AgentRuns. Event ingestion from QUAL-30 must
// use a future migration; no fake event table or skipped version is allowed.
// A newer database does not silently activate routes absent from this binary.
const (
	minSupportedSchema = 12
	maxSupportedSchema = 21
)

var errSchemaMismatch = errors.New("database migration version mismatch")

type schemaFacts struct {
	Version                 int
	Applied14               bool
	Applied15               bool
	Applied16               bool
	Applied17               bool
	Applied18               bool
	Applied19               bool
	Applied20               bool
	Applied21               bool
	Threads                 bool
	ThreadLinks             bool
	ContextIndexes          bool
	AgentRuns               bool
	AgentRunReports         bool
	ExternalEvents          bool
	EventIndexes            bool
	EventAppendOnly         bool
	TaskPolicyTable         bool
	TaskPolicyHistory       bool
	TaskPolicyAppendOnly    bool
	WorkReviewAssignments   bool
	WorkReviewReviewerIndex bool
	BoundTestRunsColumn     bool
	BoundTestRunsIndex      bool
	BoundTestRunsImmutable  bool
	HistoricLinkTable       bool
	HistoricLinkIndex       bool
	HistoricLinkImmutable   bool
}

func (s schemaFacts) validate() error {
	if s.Version < minSupportedSchema || s.Version > maxSupportedSchema {
		return errSchemaMismatch
	}
	// Never rely solely on the largest Goose number: a skipped migration
	// or partly applied feature schema must fail closed.
	if s.Version >= 14 && (!s.Applied14 || !s.Threads || !s.ThreadLinks) {
		return errSchemaMismatch
	}
	if s.Version >= 15 && (!s.Applied15 || !s.ContextIndexes) {
		return errSchemaMismatch
	}
	if s.Version >= 16 && (!s.Applied16 || !s.AgentRuns || !s.AgentRunReports) {
		return errSchemaMismatch
	}
	if s.Version >= 17 && (!s.Applied17 || !s.ExternalEvents || !s.EventIndexes || !s.EventAppendOnly) {
		return errSchemaMismatch
	}
	if s.Version >= 18 && (!s.Applied18 || !s.TaskPolicyTable || !s.TaskPolicyHistory || !s.TaskPolicyAppendOnly) {
		return errSchemaMismatch
	}
	if s.Version >= 19 && (!s.Applied19 || !s.WorkReviewAssignments || !s.WorkReviewReviewerIndex) {
		return errSchemaMismatch
	}
	if s.Version >= 20 && (!s.Applied20 || !s.BoundTestRunsColumn || !s.BoundTestRunsIndex || !s.BoundTestRunsImmutable) {
		return errSchemaMismatch
	}
	if s.Version >= 21 && (!s.Applied21 || !s.HistoricLinkTable || !s.HistoricLinkIndex || !s.HistoricLinkImmutable) {
		return errSchemaMismatch
	}
	return nil
}

// allowsContext is stricter than general startup/readiness compatibility:
// old Goose13/14 is still supported, but must never expose the new context API.
func (s schemaFacts) allowsContext() bool {
	return s.Version >= 15 && s.Applied15 && s.ContextIndexes && s.validate() == nil
}

// Goose16 must not advertise an Event API before its real append-only table.
func (s schemaFacts) allowsEvents() bool {
	return s.Version >= 17 && s.Applied17 && s.ExternalEvents && s.EventIndexes && s.EventAppendOnly && s.validate() == nil
}

// inspectSchema is used at server startup (route registration) AND readiness.
// All probes use fixed SQL identifiers and local PostgreSQL, never external I/O.
// Schema15's required real indexes are checked. ExternalEvents are not
// implicitly enabled by an unrelated context migration.
func inspectSchema(ctx context.Context, pool *pgxpool.Pool) (schemaFacts, error) {
	var s schemaFacts
	err := pool.QueryRow(ctx, `SELECT
 COALESCE(MAX(version_id) FILTER (WHERE is_applied=true),0),
 COALESCE(BOOL_OR(version_id=14 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=15 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=16 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=17 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=18 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=19 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=20 AND is_applied=true),false),
 COALESCE(BOOL_OR(version_id=21 AND is_applied=true),false),
 to_regclass('qual.external_threads') IS NOT NULL,
 to_regclass('qual.work_thread_links') IS NOT NULL,
 to_regclass('qual.test_case_coverage_work_context_idx') IS NOT NULL
  AND to_regclass('qual.test_run_items_case_context_idx') IS NOT NULL
  AND to_regclass('qual.test_attempts_run_context_idx') IS NOT NULL
  AND to_regclass('qual.test_attempts_project_ingested_context_idx') IS NOT NULL,
 to_regclass('qual.agent_runs') IS NOT NULL,
 to_regclass('qual.agent_run_reports') IS NOT NULL,
 to_regclass('qual.external_events') IS NOT NULL,
 to_regclass('qual.external_events_work_timeline_idx') IS NOT NULL
   AND to_regclass('qual.external_events_thread_timeline_idx') IS NOT NULL,
 EXISTS (SELECT 1 FROM pg_trigger tg
  WHERE tg.tgrelid=to_regclass('qual.external_events')
   AND tg.tgname='external_events_append_only'
   AND tg.tgenabled <> 'D'
   AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()')),
 to_regclass('qual.project_task_authorization_policies') IS NOT NULL,
 to_regclass('qual.task_authorization_policy_changes') IS NOT NULL,
 EXISTS (SELECT 1 FROM pg_trigger tg
  WHERE tg.tgrelid=to_regclass('qual.task_authorization_policy_changes')
   AND tg.tgname='task_authorization_changes_append_only'
   AND tg.tgenabled <> 'D'
   AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()')),
 to_regclass('qual.work_review_assignments') IS NOT NULL,
 to_regclass('qual.work_review_assignments_reviewer_idx') IS NOT NULL,
 EXISTS(SELECT 1 FROM information_schema.columns
   WHERE table_schema='qual' AND table_name='test_runs' AND column_name='work_item_id'),
 to_regclass('qual.test_runs_bound_work_idx') IS NOT NULL,
 EXISTS (SELECT 1 FROM pg_trigger tg
 WHERE tg.tgrelid=to_regclass('qual.test_runs')
 AND tg.tgname='test_runs_append_only'
 AND tg.tgenabled <> 'D'
 AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()')),
 to_regclass('qual.historical_test_run_attributions') IS NOT NULL,
 to_regclass('qual.historical_test_run_attributions_work_idx') IS NOT NULL,
 EXISTS (SELECT 1 FROM pg_trigger tg
  WHERE tg.tgrelid=to_regclass('qual.historical_test_run_attributions')
   AND tg.tgname='historical_test_run_attributions_append_only'
   AND tg.tgenabled <> 'D'
   AND tg.tgfoid=to_regprocedure('qual.reject_history_mutation()'))
 FROM public.goose_db_version`).Scan(
		&s.Version, &s.Applied14, &s.Applied15, &s.Applied16, &s.Applied17, &s.Applied18, &s.Applied19, &s.Applied20, &s.Applied21,
		&s.Threads, &s.ThreadLinks, &s.ContextIndexes, &s.AgentRuns, &s.AgentRunReports, &s.ExternalEvents, &s.EventIndexes, &s.EventAppendOnly,
		&s.TaskPolicyTable, &s.TaskPolicyHistory, &s.TaskPolicyAppendOnly, &s.WorkReviewAssignments, &s.WorkReviewReviewerIndex, &s.BoundTestRunsColumn, &s.BoundTestRunsIndex, &s.BoundTestRunsImmutable,
		&s.HistoricLinkTable, &s.HistoricLinkIndex, &s.HistoricLinkImmutable,
	)
	if err != nil {
		return s, err
	}
	return s, s.validate()
}
