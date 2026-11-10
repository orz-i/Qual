//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Goose18 readiness must validate its real policy table, append-only history
// and migration ledger, not infer safety merely from MAX(goose_version).
func TestQual55RealGoose18SchemaReadiness(t *testing.T) {
	url := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(url, "/qual55_verify18_") {
		t.Skip("only disposable qual55_verify18_* supported")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	facts, err := inspectSchema(context.Background(), pool)
	if err != nil {
		t.Fatalf("real Goose18 startup readiness failed: %v", err)
	}
	if facts.Version != 18 || !facts.Applied18 || !facts.TaskPolicyTable ||
		!facts.TaskPolicyHistory || !facts.TaskPolicyAppendOnly {
		t.Fatalf("Goose18 readiness facts incomplete: %+v", facts)
	}
	t.Log("Goose18 real schema, policy tables and immutable trigger recognized")
}
