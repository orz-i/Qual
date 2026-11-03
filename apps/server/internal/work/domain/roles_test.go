package domain

import (
	"testing"
)

func TestRoleEntryInputValidation(t *testing.T) {
	valid := RoleEntryInput{Capacity: Executor, Outcome: Observed, Summary: "Actual Git diff reviewed", ReferenceKind: NoReference, ExpectedVersion: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	good := []RoleEntryInput{
		{Capacity: Tester, Outcome: Passed, Summary: "Actual test run reported pass", ReferenceKind: TestRunReference, ReferenceValue: "7648c6ea-eda1-4664-910c-dcc26e495afa", ExpectedVersion: 2},
		{Capacity: Reviewer, Outcome: Passed, Summary: "Self review, not independent", ReferenceKind: GitCommit, ReferenceValue: "abcdef12345", ExpectedVersion: 3},
		{Capacity: Auditor, Outcome: Observed, Summary: "Inspect immutable audit records", ReferenceKind: NoReference, ExpectedVersion: 1},
	}
	for _, v := range good {
		if err := v.Validate(); err != nil {
			t.Fatalf("valid %v: %v", v, err)
		}
	}
	bad := []RoleEntryInput{
		{Capacity: "agent-b", Outcome: Observed, Summary: "Fake Principal", ReferenceKind: NoReference, ExpectedVersion: 1},
		{Capacity: Executor, Outcome: "unknown", Summary: "Missing", ReferenceKind: NoReference, ExpectedVersion: 1},
		{Capacity: Executor, Outcome: Observed, Summary: " ", ReferenceKind: NoReference, ExpectedVersion: 1},
		{Capacity: Executor, Outcome: Observed, Summary: "ok", ReferenceKind: NoReference, ExpectedVersion: 0},
		{Capacity: Tester, Outcome: Passed, Summary: "Unverified test", ReferenceKind: NoReference, ExpectedVersion: 1},
		{Capacity: Reviewer, Outcome: Failed, Summary: "Unverified review", ReferenceKind: NoReference, ExpectedVersion: 1},
		{Capacity: Tester, Outcome: Observed, Summary: "Cross project", ReferenceKind: ArtifactReference, ReferenceValue: "not-uuid", ExpectedVersion: 1},
		{Capacity: Executor, Outcome: Observed, Summary: "Source", ReferenceKind: NoReference, ReferenceValue: "x", ExpectedVersion: 1},
		{Capacity: Executor, Outcome: Observed, Summary: "Source", ReferenceKind: GitCommit, ReferenceValue: "../src", ExpectedVersion: 1},
	}
	for i, v := range bad {
		if err := v.Validate(); err == nil {
			t.Fatalf("invalid %d accepted: %+v", i, v)
		}
	}
}
