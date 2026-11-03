package domain

import (
	"errors"
	"github.com/google/uuid"
	"sort"
	"time"
)

type CredentialMeta struct {
	ID        uuid.UUID
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type RetirementPlan struct {
	Current CredentialMeta
	Old     []CredentialMeta
}

const maxRetirementBatch = 10

func isScopeSubset(previous, current []string) bool {
	if len(previous) == 0 || len(current) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(current))
	for _, v := range current {
		if v == "" || allowed[v] {
			return false
		}
		allowed[v] = true
	}
	seen := make(map[string]bool, len(previous))
	for _, v := range previous {
		if v == "" || seen[v] || !allowed[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// Refuse ambiguous ordering, duplicate rows, and any older credential with
// capabilities missing from the replacement. An explicit -apply is still
// required to commit the planned revocations.
func PlanRetirement(current CredentialMeta, active []CredentialMeta) (RetirementPlan, error) {
	if current.ID == uuid.Nil || current.CreatedAt.IsZero() || len(current.Scopes) == 0 {
		return RetirementPlan{}, errors.New("current Credential metadata is invalid")
	}
	plan := RetirementPlan{Current: current}
	found := false
	seen := make(map[uuid.UUID]bool, len(active))
	for _, item := range active {
		if item.ID == uuid.Nil || seen[item.ID] || item.CreatedAt.IsZero() {
			return RetirementPlan{}, errors.New("duplicate or invalid active Credential")
		}
		seen[item.ID] = true
		if item.ID == current.ID {
			if !item.CreatedAt.Equal(current.CreatedAt) ||
				!isScopeSubset(item.Scopes, current.Scopes) || !isScopeSubset(current.Scopes, item.Scopes) {
				return RetirementPlan{}, errors.New("current Credential changed during inspection")
			}
			found = true
			continue
		}
		if !item.CreatedAt.Before(current.CreatedAt) {
			return RetirementPlan{}, errors.New("another active Credential is not older than the selected one")
		}
		if !isScopeSubset(item.Scopes, current.Scopes) {
			return RetirementPlan{}, errors.New("older Credential has different or broader scopes; inspect manually")
		}
		plan.Old = append(plan.Old, item)
	}
	if !found || len(plan.Old) > maxRetirementBatch {
		return RetirementPlan{}, errors.New("current Credential missing or retirement batch is too large")
	}
	sort.Slice(plan.Old, func(i, j int) bool {
		if plan.Old[i].CreatedAt.Equal(plan.Old[j].CreatedAt) {
			return plan.Old[i].ID.String() < plan.Old[j].ID.String()
		}
		return plan.Old[i].CreatedAt.Before(plan.Old[j].CreatedAt)
	})
	return plan, nil
}
