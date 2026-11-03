//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Only a truly disposable, separately migrated cross-module test database
// can prove the combined Goose 14/15/16/17 schema/ledger readiness predicate.
func TestQual2732RealSchemaGateOnScratchPG(t *testing.T) {
	url := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(url, "/qual27_ctx_integrated_") {
		t.Skip("only disposable qual27_ctx_integrated_* is supported")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	facts, err := inspectSchema(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if (facts.Version != 16 && facts.Version != 17) ||
		!facts.Applied14 || !facts.Applied15 || !facts.Applied16 ||
		!facts.Threads || !facts.ThreadLinks || !facts.ContextIndexes ||
		!facts.AgentRuns || !facts.AgentRunReports {
		t.Fatalf("incomplete combined schema incorrectly accepted: %+v", facts)
	}
	if facts.Version == 17 && (!facts.Applied17 || !facts.ExternalEvents ||
		!facts.EventIndexes || !facts.EventAppendOnly || !facts.allowsEvents()) {
		t.Fatalf("Goose17 Event ownership or immutable schema incomplete: %+v", facts)
	}
	if facts.Version == 16 && facts.allowsEvents() {
		t.Fatalf("Goose16 prematurely enables ExternalEvents: %+v", facts)
	}
}
