package application

import (
	"testing"

	"qual/internal/work/domain"
)

func TestClassifyReviewLaneUsesLatestDecisionNotOnlyStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      domain.Status
		review      *ReviewQueueDecision
		expected    ReviewLane
		resubmitted bool
	}{
		{"pending without record", domain.InReview, nil, ReviewAwaiting, false},
		{"pending observed", domain.InReview, &ReviewQueueDecision{Outcome: domain.Observed}, ReviewAwaiting, false},
		{"stale pending with needs work", domain.InReview, &ReviewQueueDecision{Outcome: domain.NeedsWork}, ReviewNeedsWork, false},
		{"stale pending with failed", domain.InReview, &ReviewQueueDecision{Outcome: domain.Failed}, ReviewNeedsWork, false},
		{"in progress with needs work", domain.InProgress, &ReviewQueueDecision{Outcome: domain.NeedsWork}, ReviewNeedsWork, false},
		{"independent passed but still pending final status", domain.InReview, &ReviewQueueDecision{Outcome: domain.Passed}, ReviewReadyToClose, false},
		{"creator self review cannot independently approve", domain.InReview, &ReviewQueueDecision{Outcome: domain.Passed, SelfReview: true}, ReviewAwaiting, false},
		{"already working with historical passed is not closable", domain.InProgress, &ReviewQueueDecision{Outcome: domain.Passed}, ReviewAwaiting, false},
		{"new explicit submission supersedes former needs_work for queue placement", domain.InReview, &ReviewQueueDecision{Outcome: domain.NeedsWork}, ReviewAwaiting, true},
		{"new explicit submission supersedes former failed but keeps history", domain.InReview, &ReviewQueueDecision{Outcome: domain.Failed}, ReviewAwaiting, true},
		{"new explicit submission needs fresh independent approval after old passed", domain.InReview, &ReviewQueueDecision{Outcome: domain.Passed}, ReviewAwaiting, true},
		{"no resubmission before failed review should remain remediation", domain.InReview, &ReviewQueueDecision{Outcome: domain.Failed}, ReviewNeedsWork, false},
		{"in_progress old decision always remains remediation", domain.InProgress, &ReviewQueueDecision{Outcome: domain.NeedsWork}, ReviewNeedsWork, true},
	}
	for _, v := range tests {
		t.Run(v.name, func(t *testing.T) {
			got := ClassifyReviewLane(v.status, v.review, v.resubmitted)
			if got != v.expected {
				t.Fatalf("got %s expected %s", got, v.expected)
			}
		})
	}
}
