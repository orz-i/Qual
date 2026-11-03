package domain

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPlanRetirementRequiresSingleCurrentAndNarrowerOlderCredentials(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	current := CredentialMeta{ID: uuid.New(), CreatedAt: now, Scopes: []string{"work:read", "work:review", "audit:read"}}
	old1 := CredentialMeta{ID: uuid.New(), CreatedAt: now.Add(-time.Hour), Scopes: []string{"work:read"}}
	old2 := CredentialMeta{ID: uuid.New(), CreatedAt: now.Add(-2 * time.Hour), Scopes: []string{"work:read", "work:review"}}
	plan, err := PlanRetirement(current, []CredentialMeta{old1, current, old2})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Old) != 2 || plan.Old[0].ID != old2.ID || plan.Old[1].ID != old1.ID {
		t.Fatal("retirement plan did not preserve current or sort older credentials")
	}
	if len(plan.Current.Scopes) != 3 {
		t.Fatal("current Credential unexpectedly altered")
	}

	for name, entries := range map[string][]CredentialMeta{
		"missing current": {old1, old2},
		"duplicate":       {current, old1, old1},
		"same timestamp":  {current, {ID: uuid.New(), CreatedAt: now, Scopes: old1.Scopes}},
		"later token":     {current, {ID: uuid.New(), CreatedAt: now.Add(time.Hour), Scopes: old1.Scopes}},
		"broader token":   {current, {ID: uuid.New(), CreatedAt: now.Add(-time.Hour), Scopes: []string{"access:manage"}}},
		"invalid scope":   {current, {ID: uuid.New(), CreatedAt: now.Add(-time.Hour), Scopes: []string{"work:read", "work:read"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PlanRetirement(current, entries); err == nil {
				t.Fatal("ambiguous or broader credentials must fail closed")
			}
		})
	}
}

func TestRetirementPlanIsBounded(t *testing.T) {
	now := time.Now().UTC()
	current := CredentialMeta{ID: uuid.New(), CreatedAt: now, Scopes: []string{"work:read"}}
	items := []CredentialMeta{current}
	for i := 0; i < maxRetirementBatch+1; i++ {
		items = append(items, CredentialMeta{ID: uuid.New(), Scopes: []string{"work:read"},
			CreatedAt: now.Add(-time.Duration(i+1) * time.Hour)})
	}
	if _, err := PlanRetirement(current, items); err == nil {
		t.Fatal("bulk retirement without bounded batch must fail closed")
	}
}

func TestScopeSubsetAlwaysRequiresValidNarrowerScopes(t *testing.T) {
	if !isScopeSubset([]string{"work:read"}, []string{"work:read", "work:review"}) {
		t.Fatal("narrower scope should be eligible")
	}
	for _, tc := range []struct{ old, current []string }{
		{[]string{"work:review"}, []string{"work:read"}},
		{[]string{"work:read", "work:read"}, []string{"work:read"}},
		{[]string{}, []string{"work:read"}},
		{[]string{"work:read"}, []string{}},
		{[]string{"work:read"}, []string{"work:read", "work:read"}},
	} {
		if isScopeSubset(tc.old, tc.current) {
			t.Fatalf("unsafe scope set accepted: %v", tc.old)
		}
	}
}
