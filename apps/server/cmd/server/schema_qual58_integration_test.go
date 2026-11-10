//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQual58RealGoose20BoundRunSchemaReadiness(t *testing.T) {
	url := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(url, "/qual58_schema20_") {
		t.Skip("only disposable qual58_schema20_* databases may be inspected")
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
	if facts.Version != 20 || !facts.Applied20 || !facts.BoundTestRunsColumn ||
		!facts.BoundTestRunsIndex || !facts.BoundTestRunsImmutable || facts.validate() != nil {
		t.Fatalf("Goose20 schema validation incomplete: %+v", facts)
	}
	t.Log("Goose20 real applied ledger, immutable TestRun work_item_id and lookup index recognized")
}
