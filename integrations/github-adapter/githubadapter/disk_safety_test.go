package githubadapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiskLowSpaceRefusesNewButKeepsIdenticalReplayIdempotent(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 40)
	h, err := NewHandler(fixtureRepo, []byte(secret), s)
	if err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return fixtureNow }
	raw := fixtureBody("opened")
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 32 << 20}, nil
	}
	if got := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret); got.Code != 503 {
		t.Fatalf("disk below hard reserve accepted new signed webhook: HTTP%d", got.Code)
	}
	if got, err := s.Pending(); err != nil || len(got) != 0 {
		t.Fatalf("rejected ingress nonetheless wrote pending receipt: %v %v", got, err)
	}
	c, err := s.Capacity()
	if err != nil || !c.DiskIngressBlocked || !c.IngressBlocked || !c.DiskNearLimit || c.DiskAvailableBytes != 32<<20 {
		t.Fatalf("disk blocked telemetry inaccurate: %+v %v", c, err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 256 << 20}, nil
	}
	if got := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret); got.Code != 202 {
		t.Fatalf("adequate disk rejected a valid initial delivery: HTTP%d", got.Code)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: 0}, nil
	}
	if got := sendWebhook(h, raw, fixtureDeliveryID, "pull_request", secret); got.Code != 202 {
		t.Fatalf("same durable signed delivery failed idempotent low-disk acknowledgment: HTTP%d", got.Code)
	}
	if got, err := s.Pending(); err != nil || len(got) != 1 {
		t.Fatalf("low-disk duplicate destroyed/fabricated an event: %+v %v", got, err)
	}
}

func TestDiskProbeFailsClosedAndDoesNotCreateReceipts(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	s.probeDisk = func(string) (filesystemStats, error) {
		return filesystemStats{}, fmt.Errorf("synthetic statfs refused")
	}
	if created, err := s.Enqueue(d); created || err == nil {
		t.Fatalf("statfs error accepted webhook: %t %v", created, err)
	}
	if _, err := s.Capacity(); err == nil {
		t.Fatal("inspect falsely claimed healthy filesystem after statfs error")
	}
	if got, err := s.Pending(); err != nil || len(got) != 0 {
		t.Fatalf("failed disk preflight wrote data: %+v %v", got, err)
	}
	s.probeDisk = func(string) (filesystemStats, error) { return filesystemStats{TotalBytes: 0, AvailableBytes: 1}, nil }
	if created, err := s.Enqueue(d); created || !errors.Is(err, ErrSpool) {
		t.Fatalf("untrustworthy zero total bytes accepted: %t %v", created, err)
	}
}

func TestDiskTelemetryCoversEightyPercentAndHardReserve(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		free             uint64
		warning, blocked bool
	}{
		{free: 800 << 20, warning: false, blocked: false},
		{free: 300 << 20, warning: false, blocked: false},
		{free: 200 << 20, warning: true, blocked: false},
		{free: 64 << 20, warning: true, blocked: false},
		{free: (64 << 20) - 1, warning: true, blocked: true},
	} {
		s.probeDisk = func(string) (filesystemStats, error) {
			return filesystemStats{TotalBytes: 1 << 30, AvailableBytes: tc.free}, nil
		}
		result, err := s.Capacity()
		if err != nil {
			t.Fatal(err)
		}
		if result.DiskNearLimit != tc.warning || result.DiskIngressBlocked != tc.blocked ||
			result.IngressBlocked != tc.blocked || result.DiskAvailableBytes != tc.free ||
			result.DiskReserveBytes != 64<<20 {
			t.Errorf("free=%d: %+v expected warning=%t blocked=%t", tc.free, result, tc.warning, tc.blocked)
		}
	}
}

func TestActualDiskCapacityReadIsAvailableToOperator(t *testing.T) {
	s, err := OpenSpool(filepath.Join(t.TempDir(), "spool"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Capacity()
	if err != nil || result.DiskTotalBytes == 0 || result.DiskAvailableBytes == 0 {
		t.Fatalf("actual temporary spool filesystem not observable: %+v %v", result, err)
	}
	if _, err := os.Stat(s.Root); err != nil {
		t.Fatal(err)
	}
}
