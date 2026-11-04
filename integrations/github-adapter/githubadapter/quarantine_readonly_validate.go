package githubadapter

import (
	"os"
	"path/filepath"
	"strings"
)

// validateQuarantinedReadOnly verifies a signed-restored spool's durable
// identities without applying *any* crash recovery writes to source facts.
// The original active spool may reconcile a fsync/rename crash window; an
// untrusted old snapshot must NOT silently delete Pending or move receipts
// into Archived merely because a read-only "inspect" opened it.
//
// CreateSignedBackup always locks and reconciles source BEFORE snapshotting,
// so a restored snapshot should already be canonical. Any ambiguous overlap
// or partially archived migration is rejected for an Operator to investigate.
func (s *Spool) validateQuarantinedReadOnly() error {
	for _, sub := range snapshotDirectories {
		entries, err := os.ReadDir(filepath.Join(s.Root, sub))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !validSnapshotEntry(sub+"/"+entry.Name()) {
				return ErrSpool
			}
			key := strings.TrimSuffix(entry.Name(), ".json")
			if sub == "archive-index" {
				d, found, err := s.readArchiveIndex(key)
				if err != nil || !found {
					return ErrSpool
				}
				if _, inArchive, err := s.read("archived", d.DeliveryID); err != nil || !inArchive {
					return ErrSpool
				}
				continue
			}
			d, found, err := s.read(sub, key)
			if err != nil || !found || !archiveDigestRE.MatchString(d.PayloadSHA256) {
				return ErrSpool
			}
			switch sub {
			case "pending":
				_, hot, err := s.read("delivered", key)
				if err != nil || hot {
					return ErrSpool
				}
				_, cold, err := s.read("archived", key)
				if err != nil || cold {
					return ErrSpool
				}
			case "delivered":
				if !deliveryIDRE.MatchString(d.DeliveredEventID) {
					return ErrSpool
				}
				_, cold, err := s.read("archived", key)
				if err != nil || cold {
					return ErrSpool
				}
				// A complete, canonical signed snapshot has already
				// moved any durable indexed delivery to Archived.
				if _, indexed, err := s.readArchiveIndex(d.PayloadSHA256); err != nil || indexed {
					return ErrSpool
				}
			case "archived":
				index, exists, err := s.readArchiveIndex(d.PayloadSHA256)
				if err != nil || !exists || index.DeliveryID != key {
					return ErrSpool
				}
			}
		}
	}
	return nil
}
