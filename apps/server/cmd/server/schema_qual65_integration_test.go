//go:build integration

package main

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Only the fixed local/docker CI scratch database from
// scripts/tests/ci_goose21_forward_guarded.sh may be tested. Never point this
// test at the official qual database or run it against an arbitrary DSN.
func TestQual65Goose21ProvenanceSchemaReadyAndTriggerFailClosed(t *testing.T) {
	raw := os.Getenv("QUAL_TEST_DATABASE_URL")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Port() != "15432" ||
		!regexp.MustCompile(`^/qual21_forward_ci_[0-9]+_[0-9]+$`).MatchString(parsed.Path) {
		t.Skip("Goose21 readiness integration requires disposable qual21_forward_ci_* PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	facts, err := inspectSchema(ctx, pool)
	if err != nil || facts.Version != 21 || !facts.Applied18 || !facts.Applied19 ||
		!facts.Applied20 || !facts.Applied21 || !facts.TaskPolicyTable ||
		!facts.TaskPolicyAppendOnly || !facts.WorkReviewAssignments ||
		!facts.BoundTestRunsImmutable || !facts.HistoricLinkTable ||
		!facts.HistoricLinkIndex || !facts.HistoricLinkImmutable {
		t.Fatalf("actual Goose21 is not backed by the required intact schema: facts=%+v err=%v", facts, err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE qual.historical_test_run_attributions
        DISABLE TRIGGER historical_test_run_attributions_append_only`); err != nil {
		t.Fatalf("scratch-only trigger negative preflight: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `ALTER TABLE qual.historical_test_run_attributions
            ENABLE TRIGGER historical_test_run_attributions_append_only`); err != nil {
			t.Errorf("failed to restore scratch trigger after negative probe: %v", err)
		}
	}()
	if _, err := inspectSchema(ctx, pool); err == nil {
		t.Fatal("disabled immutable historical attribution trigger was accepted by server readiness")
	}
	t.Log("Real Goose21 schema accepted and disabled history trigger refused; synthetic scratch database only")
}
