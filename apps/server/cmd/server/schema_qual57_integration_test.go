//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQual57RealGoose19ReviewAssignmentSchemaReadiness(t *testing.T) {
	url := os.Getenv("QUAL_TEST_DATABASE_URL")
	if !strings.Contains(url, "/qual57_schema19_") {
		t.Skip("only isolated qual57_schema19_* may probe Goose19 readiness")
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
	if facts.Version != 19 || !facts.Applied19 || !facts.WorkReviewAssignments ||
		!facts.WorkReviewReviewerIndex || facts.validate() != nil {
		t.Fatalf("Goose19 incomplete review schema allowed %+v", facts)
	}
	t.Log("Goose19 ledger, current reviewer relation and index are available for scoped review")
}
