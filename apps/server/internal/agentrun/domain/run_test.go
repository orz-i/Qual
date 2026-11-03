package domain

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestExternalRunStatusIsNotWorkStatus(t *testing.T) {
	for _, terminal := range []Status{Succeeded, Failed, Cancelled} {
		for _, next := range []Status{Running, Blocked, Queued, Succeeded, Failed, Cancelled} {
			if CanProject(terminal, next) {
				t.Fatalf("%s may not regress to %s", terminal, next)
			}
		}
		if got := Observe(terminal, nil, time.Now(), time.Minute); got != "terminal" {
			t.Fatalf("%s status %s", terminal, got)
		}
	}
	if CanProject(Running, Queued) || !CanProject(Blocked, Running) {
		t.Fatal("unexpected transition")
	}
	now := time.Now()
	old := now.Add(-10 * time.Minute)
	if got := Observe(Running, &old, now, 5*time.Minute); got != "stale" {
		t.Fatal(got)
	}
	if got := Observe(Running, nil, now, time.Minute); got != "unknown" {
		t.Fatal(got)
	}
}
func TestInvalidUntrustedReportFields(t *testing.T) {
	now := time.Now()
	r := Registration{WorkItemID: uuid.New(), Source: "pi", ExternalRunID: "run:1", Host: "local"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.Source = "pi /bad"
	if r.Validate() == nil {
		t.Fatal("invalid source accepted")
	}
	r.Source = "pi"
	for _, host := range []string{"mac\x00mini", "mac\nmini", "mac\rmini", "mac\tmini", "mac\x7fmini", "mac\u0085mini", "  mac", "mac  "} {
		r.Host = host
		if err := r.Validate(); err == nil {
			t.Errorf("control/space in Host not rejected: %q", host)
		}
	}
	r.Host = "Mac mini"
	if err := r.Validate(); err != nil {
		t.Fatalf("ordinary multiword Host invalid: %v", err)
	}
	h := ReportInput{Kind: Heartbeat, SourceReportID: "hb-1", OccurredAt: now, Status: Succeeded}
	if h.Validate(now) == nil {
		t.Fatal("heartbeat forged success")
	}
	h.Status = ""
	if err := h.Validate(now); err != nil {
		t.Fatal(err)
	}
	h.EvidenceArtifactID = new(uuid.UUID)
	if h.Validate(now) == nil {
		t.Fatal("nil evidence UUID accepted")
	}
	h.EvidenceArtifactID = nil
	h.OccurredAt = now.Add(time.Hour)
	if h.Validate(now) == nil {
		t.Fatal("future event accepted")
	}
}
