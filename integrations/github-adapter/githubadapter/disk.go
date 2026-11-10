package githubadapter

import (
	"math"
	"math/bits"
	"syscall"
)

// The intake reserve guards small durable JSON writes, their fsync metadata
// and a manual recovery margin. It is a lower bound, not a replacement for
// quotas, disk expansion, archive backups, or host-level disk alerts.
const diskReserveBytes uint64 = 64 * 1024 * 1024
const diskWarnFreeBytes uint64 = 256 * 1024 * 1024

type filesystemStats struct {
	TotalBytes     uint64
	AvailableBytes uint64 // space available to this OS process, not root
}

func saturatingMul64(a, b uint64) uint64 {
	high, low := bits.Mul64(a, b)
	if high != 0 {
		return math.MaxUint64
	}
	return low
}

func actualFilesystemStats(path string) (filesystemStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return filesystemStats{}, err
	}
	blockSize := uint64(stat.Bsize)
	if blockSize == 0 {
		return filesystemStats{}, ErrSpool
	}
	return filesystemStats{
		TotalBytes:     saturatingMul64(uint64(stat.Blocks), blockSize),
		AvailableBytes: saturatingMul64(uint64(stat.Bavail), blockSize),
	}, nil
}

func (s *Spool) disk() (filesystemStats, error) {
	probe := s.probeDisk
	if probe == nil {
		probe = actualFilesystemStats
	}
	stats, err := probe(s.Root)
	if err != nil {
		return filesystemStats{}, err
	}
	if stats.TotalBytes == 0 || stats.AvailableBytes > stats.TotalBytes {
		return filesystemStats{}, ErrSpool
	}
	return stats, nil
}

// IngressReadiness is a read-only, best-effort readiness probe for *new*
// signed deliveries. Unlike /healthz, it also checks the live active-origin
// witnesses, recovery quarantine, real disk reserve and hot queue limits.
// An identical already-durable delivery may still be acknowledged while
// intake is blocked; readiness is not a substitute for Enqueue's locked gate.
func (s *Spool) IngressReadiness() error {
	if err := s.requireActive(); err != nil {
		return err
	}
	capacity, err := s.Capacity()
	if err != nil {
		return err
	}
	if capacity.IngressBlocked {
		return ErrFull
	}
	return nil
}

func (s *Spool) diskPreflight() error {
	disk, err := s.disk()
	if err != nil {
		return err
	}
	if disk.AvailableBytes < diskReserveBytes {
		return ErrFull
	}
	return nil
}
