package githubadapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A positive liveness check does not mean the spool is able to accept a new
// signed webhook. These probes must not remove historical Delivery facts.
func TestIngressReadinessFollowsCapacityAndSourceOrigin(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 300 << 20}, nil
	}
	if err := s.IngressReadiness(); err != nil {
		t.Fatalf("fresh and funded spool not ready: %v", err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 63 << 20}, nil
	}
	if err := s.IngressReadiness(); !errors.Is(err, ErrFull) {
		t.Fatalf("below disk reserve readiness=%v, want ErrFull", err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 64 << 20}, nil
	}
	if err := s.IngressReadiness(); err != nil {
		t.Fatalf("exact disk reserve gate drift: %v", err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{}, errors.New("synthetic statfs failure")
	}
	if err := s.IngressReadiness(); err == nil {
		t.Fatal("failed disk telemetry advertised ready")
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 300 << 20}, nil
	}
	if err := os.Remove(filepath.Join(s.Root, activeOriginWitnessFile)); err != nil {
		t.Fatal(err)
	}
	if err := s.IngressReadiness(); !errors.Is(err, ErrSpool) {
		t.Fatalf("missing active-origin witness advertised ready: %v", err)
	}
}
