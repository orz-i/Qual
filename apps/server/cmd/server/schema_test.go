package main

import (
	"errors"
	"testing"
)

func TestSchemaCompatibilityFailsClosed(t *testing.T) {
	g14 := schemaFacts{Version: 14, Applied14: true, Threads: true, ThreadLinks: true}
	g15 := g14
	g15.Version, g15.Applied15, g15.ContextIndexes = 15, true, true
	g16 := g15
	g16.Version, g16.Applied16, g16.AgentRuns, g16.AgentRunReports = 16, true, true, true
	g17 := g16
	g17.Version, g17.Applied17, g17.ExternalEvents, g17.EventIndexes, g17.EventAppendOnly = 17, true, true, true, true
	g18 := g17
	g18.Version, g18.Applied18, g18.TaskPolicyTable, g18.TaskPolicyHistory, g18.TaskPolicyAppendOnly = 18, true, true, true, true
	g19 := g18
	g19.Version, g19.Applied19, g19.WorkReviewAssignments, g19.WorkReviewReviewerIndex = 19, true, true, true
	g20 := g19
	g20.Version, g20.Applied20, g20.BoundTestRunsColumn, g20.BoundTestRunsIndex, g20.BoundTestRunsImmutable = 20, true, true, true, true
	g21 := g20
	g21.Version, g21.Applied21, g21.HistoricLinkTable, g21.HistoricLinkIndex, g21.HistoricLinkImmutable = 21, true, true, true, true

	cases := []struct {
		name      string
		facts     schemaFacts
		wantReady bool
	}{
		{"unmigrated", schemaFacts{}, false},
		{"old unsupported", schemaFacts{Version: 11}, false},
		{"legacy Goose12", schemaFacts{Version: 12}, true},
		{"current default Goose13", schemaFacts{Version: 13}, true},
		{"Goose14 without ledger", schemaFacts{Version: 14, Threads: true, ThreadLinks: true}, false},
		{"Goose14 missing Thread table", schemaFacts{Version: 14, Applied14: true, ThreadLinks: true}, false},
		{"Goose14 missing Link table", schemaFacts{Version: 14, Applied14: true, Threads: true}, false},
		{"Goose14 complete", g14, true},
		{"Goose15 without version 14", schemaFacts{Version: 15, Applied15: true, ContextIndexes: true}, false},
		{"Goose15 missing context index set", schemaFacts{Version: 15, Applied14: true, Applied15: true, Threads: true, ThreadLinks: true}, false},
		{"Goose15 complete", g15, true},
		{"Goose16 skipped Goose15", schemaFacts{Version: 16, Applied14: true, Applied16: true, Threads: true, ThreadLinks: true, AgentRuns: true, AgentRunReports: true}, false},
		{"Goose16 missing run tables", schemaFacts{Version: 16, Applied14: true, Applied15: true, Applied16: true, Threads: true, ThreadLinks: true, ContextIndexes: true}, false},
		{"Goose16 missing report table", schemaFacts{Version: 16, Applied14: true, Applied15: true, Applied16: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true}, false},
		{"Goose16 complete after Goose15", g16, true},
		{"Goose17 missing Event ledger/table", schemaFacts{Version: 17, Applied14: true, Applied15: true, Applied16: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true}, false},
		{"Goose17 Event table without ledger", schemaFacts{Version: 17, Applied14: true, Applied15: true, Applied16: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true}, false},
		{"Goose17 ledger without Event table", schemaFacts{Version: 17, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true}, false},
		{"Goose17 missing one/both timeline indexes", schemaFacts{Version: 17, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventAppendOnly: true}, false},
		{"Goose17 missing append-only trigger", schemaFacts{Version: 17, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true}, false},
		{"Goose17 complete after Goose16", g17, true},
		{"Goose18 missing policy ledger", schemaFacts{Version: 18, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true}, false},
		{"Goose18 missing append-only history", schemaFacts{Version: 18, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Applied18: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true, TaskPolicyTable: true, TaskPolicyHistory: true}, false},
		{"Goose18 complete after Goose17", g18, true},
		{"Goose19 missing assignment table", schemaFacts{Version: 19, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Applied18: true, Applied19: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true, TaskPolicyTable: true, TaskPolicyHistory: true, TaskPolicyAppendOnly: true}, false},
		{"Goose19 missing reviewer index", schemaFacts{Version: 19, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Applied18: true, Applied19: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true, TaskPolicyTable: true, TaskPolicyHistory: true, TaskPolicyAppendOnly: true, WorkReviewAssignments: true}, false},
		{"Goose19 complete", g19, true},
		{"Goose20 missing bound Run schema", schemaFacts{Version: 20, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Applied18: true, Applied19: true, Applied20: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true, TaskPolicyTable: true, TaskPolicyHistory: true, TaskPolicyAppendOnly: true, WorkReviewAssignments: true, WorkReviewReviewerIndex: true}, false},
		{"Goose20 missing immutable trigger", schemaFacts{Version: 20, Applied14: true, Applied15: true, Applied16: true, Applied17: true, Applied18: true, Applied19: true, Applied20: true, Threads: true, ThreadLinks: true, ContextIndexes: true, AgentRuns: true, AgentRunReports: true, ExternalEvents: true, EventIndexes: true, EventAppendOnly: true, TaskPolicyTable: true, TaskPolicyHistory: true, TaskPolicyAppendOnly: true, WorkReviewAssignments: true, WorkReviewReviewerIndex: true, BoundTestRunsColumn: true, BoundTestRunsIndex: true}, false},
		{"Goose20 complete", g20, true},
		{"Goose21 without attribution schema", schemaFacts{Version: 21, Applied21: true}, false},
		{"Goose21 without immutable trigger", func() schemaFacts { incomplete := g21; incomplete.HistoricLinkImmutable = false; return incomplete }(), false},
		{"Goose21 without index", func() schemaFacts { incomplete := g21; incomplete.HistoricLinkIndex = false; return incomplete }(), false},
		{"Goose21 complete", g21, true},
		{"unknown future Goose22", schemaFacts{Version: 22}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.facts.validate()
			if c.wantReady && err != nil {
				t.Fatalf("compatible migration rejected: %v", err)
			}
			if !c.wantReady && !errors.Is(err, errSchemaMismatch) {
				t.Fatalf("incomplete/unsupported schema accepted: %v", err)
			}
			wantContext := c.wantReady && c.facts.Version >= 15
			if got := c.facts.allowsContext(); got != wantContext {
				t.Fatalf("context enabled=%t, want %t for %+v", got, wantContext, c.facts)
			}
			wantEvents := c.wantReady && c.facts.Version >= 17
			if got := c.facts.allowsEvents(); got != wantEvents {
				t.Fatalf("events enabled=%t, want %t for %+v", got, wantEvents, c.facts)
			}
		})
	}
}
