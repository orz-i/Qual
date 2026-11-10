package application

import (
	"errors"
	"strings"
	"testing"

	"qual/internal/work/domain"
)

func TestTaskAuthorizationPolicyChangeValidation(t *testing.T) {
	valid := TaskAuthorizationPolicyChange{
		Mode: domain.TaskScopedAuthorization, ExpectedVersion: 0,
		Reason: "Qualified explicit authorized rollout of task-scoped mode to an isolated project",
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	legacy := valid
	legacy.Mode = domain.LegacyAuthorization
	if err := legacy.Validate(); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("legacy broadening without explicit acknowledgement unexpectedly allowed: %v", err)
	}
	legacy.AcknowledgeLegacyWidening = true
	if err := legacy.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []TaskAuthorizationPolicyChange{
		{Mode: "unknown", ExpectedVersion: 0, Reason: valid.Reason},
		{Mode: domain.TaskScopedAuthorization, ExpectedVersion: -1, Reason: valid.Reason},
		{Mode: domain.TaskScopedAuthorization, ExpectedVersion: 0, Reason: "short"},
		{Mode: domain.TaskScopedAuthorization, ExpectedVersion: 0, Reason: " " + valid.Reason},
		{Mode: domain.TaskScopedAuthorization, ExpectedVersion: 0, Reason: valid.Reason + " "},
		{Mode: domain.TaskScopedAuthorization, ExpectedVersion: 0, Reason: strings.Repeat("A", 2001)},
	}
	for _, candidate := range cases {
		if err := candidate.Validate(); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("invalid change accepted: %+v, err=%v", candidate, err)
		}
	}
}
