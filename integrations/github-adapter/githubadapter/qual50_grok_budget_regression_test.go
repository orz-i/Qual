package githubadapter

import (
	"errors"
	"math"
	"testing"
)

// TestQual50BackupDestinationSpaceBoundaries locks fail-closed results that
// TestSnapshotDestinationBudgetFailsClosed does not already assert: free space
// reported above the filesystem size, the exact 64MiB reserve floor, a one-byte
// shortfall of payload plus per-record overhead plus that reserve, and a payload
// whose uint64 sum would wrap if the record cap were skipped.
func TestQual50BackupDestinationSpaceBoundaries(t *testing.T) {
	const payload = uint64(8 << 20)
	const records = 4
	const combined = diskReserveBytes + payload + records*1024

	for _, tc := range []struct {
		name    string
		stats   filesystemStats
		payload uint64
		records int
		want    error
	}{
		{
			name: "free-bytes-exceed-filesystem-size",
			stats: filesystemStats{
				TotalBytes:     diskReserveBytes,
				AvailableBytes: diskReserveBytes + 1,
			},
			payload: 0,
			records: 0,
			want:    ErrSpool,
		},
		{
			name: "one-byte-under-64mib-reserve",
			stats: filesystemStats{
				TotalBytes:     1 << 30,
				AvailableBytes: diskReserveBytes - 1,
			},
			payload: 0,
			records: 0,
			want:    ErrFull,
		},
		{
			name: "one-byte-under-payload-overhead-and-reserve",
			stats: filesystemStats{
				TotalBytes:     1 << 30,
				AvailableBytes: combined - 1,
			},
			payload: payload,
			records: records,
			want:    ErrFull,
		},
		{
			name: "maxuint64-payload-does-not-wrap-into-acceptance",
			stats: filesystemStats{
				TotalBytes:     1 << 30,
				AvailableBytes: 1 << 30,
			},
			payload: math.MaxUint64,
			records: 0,
			want:    ErrInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBackupDestinationSpace(tc.stats, tc.payload, tc.records)
			if !errors.Is(err, tc.want) {
				t.Fatalf("checkBackupDestinationSpace() = %v, want %v", err, tc.want)
			}
		})
	}
}
