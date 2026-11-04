package githubadapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// archived/<delivery-uuid>.json retains full completed event identities.
// archive-index/<original-body-sha256>.json retains content-addressed replay
// history. BOTH records remain owner-only, never deleted or rotated away.
// Operation order: fsync(index) -> rename(Delivered,Archived) -> fsync dirs.
var archiveDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ArchiveStats struct {
	Archived      int `json:"archived"`
	RemainingHot  int `json:"remaining_hot"`
	TotalArchived int `json:"total_archived"`
}

func (s *Spool) archiveIndexPath(sha string) (string, error) {
	if !archiveDigestRE.MatchString(sha) {
		return "", ErrSpool
	}
	return filepath.Join(s.Root, "archive-index", sha+".json"), nil
}

// readArchiveIndex binds an indexed hash to the original complete receipt,
// including its immutable Qual Event ID. A crash may leave the receipt in
// Delivered temporarily, provided the index was durably fsynced first.
func (s *Spool) readArchiveIndex(sha string) (Delivery, bool, error) {
	path, err := s.archiveIndexPath(sha)
	if err != nil {
		return Delivery{}, false, err
	}
	data, err := trustedFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Delivery{}, false, nil
	}
	if err != nil {
		return Delivery{}, false, err
	}
	var saved Delivery
	if json.Unmarshal(data, &saved) != nil || saved.PayloadSHA256 != sha ||
		!deliveryIDRE.MatchString(saved.DeliveryID) ||
		!deliveryIDRE.MatchString(saved.DeliveredEventID) ||
		saved.State != "delivered" || saved.RepositoryID <= 0 ||
		!validHistory(saved.History) ||
		(saved.PinnedTarget != nil && !validTarget(*saved.PinnedTarget)) {
		return Delivery{}, false, ErrSpool
	}
	archived, inArchive, err := s.read("archived", saved.DeliveryID)
	if err != nil {
		return Delivery{}, false, err
	}
	delivered, inHot, err := s.read("delivered", saved.DeliveryID)
	if err != nil {
		return Delivery{}, false, err
	}
	if inArchive == inHot { // neither or both violates the migration invariant
		return Delivery{}, false, ErrSpool
	}
	if inArchive && !reflect.DeepEqual(archived, saved) ||
		inHot && !reflect.DeepEqual(delivered, saved) {
		return Delivery{}, false, ErrConflict
	}
	return saved, true, nil
}

// moveArchivedLocked is called under the queue lock. Never overwrite an
// archived receipt, and never move a receipt whose content changed.
func (s *Spool) moveArchivedLocked(d Delivery) error {
	from, err := s.path("delivered", d.DeliveryID)
	if err != nil {
		return err
	}
	to, err := s.path("archived", d.DeliveryID)
	if err != nil {
		return err
	}
	if _, exists, err := s.read("archived", d.DeliveryID); err != nil {
		return err
	} else if exists {
		return ErrConflict
	}
	hot, exists, err := s.read("delivered", d.DeliveryID)
	if err != nil {
		return err
	}
	if !exists || !reflect.DeepEqual(hot, d) {
		return ErrConflict
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Join(s.Root, "archived")); err != nil {
		return err
	}
	return syncDirectory(filepath.Join(s.Root, "delivered"))
}

// reconcileArchiveLocked runs during OpenSpool under worker+queue locks.
// It completes migrations after a crash once a durable SHA index exists.
// An orphan archive with a missing or corrupt index FAILS CLOSED rather than
// re-admitting a signed replay under a new unsigned Delivery ID.
func (s *Spool) reconcileArchiveLocked() error {
	idx, err := os.ReadDir(filepath.Join(s.Root, "archive-index"))
	if err != nil {
		return err
	}
	for _, entry := range idx {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue // incomplete temp file; no committed index yet
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return ErrSpool
		}
		sha := strings.TrimSuffix(entry.Name(), ".json")
		d, exists, err := s.readArchiveIndex(sha)
		if err != nil || !exists {
			return ErrSpool
		}
		if _, archived, err := s.read("archived", d.DeliveryID); err != nil {
			return err
		} else if !archived {
			if err := s.moveArchivedLocked(d); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.Root, "archived"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return ErrSpool
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		d, exists, err := s.read("archived", id)
		if err != nil || !exists || !archiveDigestRE.MatchString(d.PayloadSHA256) ||
			!deliveryIDRE.MatchString(d.DeliveredEventID) {
			return ErrSpool
		}
		other, indexed, err := s.readArchiveIndex(d.PayloadSHA256)
		if err != nil || !indexed || !reflect.DeepEqual(d, other) {
			return ErrSpool
		}
	}
	return nil
}

// ArchiveDelivered is an explicit, bounded, trusted-local-Operator command.
// It NEVER removes event identities or their signed-content SHA tombstones.
// Disk capacity and backups of the expanding archive remain Operator duties.
func (s *Spool) ArchiveDelivered(limit int) (ArchiveStats, error) {
	var stats ArchiveStats
	if limit < 1 || limit > 500 {
		return stats, ErrInvalid
	}
	err := s.withLock("worker", func() error {
		return s.withLock("queue", func() error {
			if err := s.reconcileLocked(); err != nil {
				return err
			}
			if err := s.reconcileArchiveLocked(); err != nil {
				return err
			}
			dir := filepath.Join(s.Root, "delivered")
			names, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			var completed []Delivery
			for _, file := range names {
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
					return ErrSpool
				}
				d, found, err := s.read("delivered", strings.TrimSuffix(file.Name(), ".json"))
				if err != nil || !found || !archiveDigestRE.MatchString(d.PayloadSHA256) ||
					!deliveryIDRE.MatchString(d.DeliveredEventID) {
					return ErrSpool
				}
				completed = append(completed, d)
			}
			sort.Slice(completed, func(i, j int) bool {
				if completed[i].ReceivedAt.Equal(completed[j].ReceivedAt) {
					return completed[i].DeliveryID < completed[j].DeliveryID
				}
				return completed[i].ReceivedAt.Before(completed[j].ReceivedAt)
			})
			for _, d := range completed {
				if stats.Archived >= limit {
					break
				}
				path, err := s.archiveIndexPath(d.PayloadSHA256)
				if err != nil {
					return err
				}
				if _, present, err := s.readArchiveIndex(d.PayloadSHA256); err != nil {
					return err
				} else if present {
					return ErrConflict
				}
				// A durable SHA-index must exist BEFORE the hot record moves.
				// This is intentionally not a delete/expiry operation.
				if err := s.atomicWrite(path, d); err != nil {
					return err
				}
				if err := s.moveArchivedLocked(d); err != nil {
					return err
				}
				stats.Archived++
			}
			hot, err := os.ReadDir(filepath.Join(s.Root, "delivered"))
			if err != nil {
				return err
			}
			cold, err := os.ReadDir(filepath.Join(s.Root, "archived"))
			if err != nil {
				return err
			}
			stats.RemainingHot = len(hot)
			stats.TotalArchived = len(cold)
			return nil
		})
	})
	return stats, err
}
