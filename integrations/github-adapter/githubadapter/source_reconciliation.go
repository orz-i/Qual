package githubadapter

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VerifyRestoredSourceEvidence checks independently signed, canonical
// declarations against an already quarantined signed snapshot. It does not
// mutate the restored facts, contact Qual, issue new credentials, create
// reconciliation attestations, or turn on a restored listener.
//
// Independently trust-pinned source, Qual and Operator public keys are a
// precondition. The original signers, off-host latest watermark and trusted
// issuance channels are NOT implemented by this Adapter; the report therefore
// cannot authorize production activation even if RecordsMatch is true.
func (s *Spool) VerifyRestoredSourceEvidence(
	repo Repository, keys SourceReconciliationKeys, evidence SourceReconciliationEvidence,
	now time.Time,
) (AuditOnlyReconciliation, error) {
	var empty AuditOnlyReconciliation
	if s == nil || !validReconciliationKeys(keys) ||
		now.IsZero() || now.After(time.Now().UTC().Add(5*time.Minute)) {
		return empty, ErrReconciliation
	}
	recovery, err := s.RecoveryStatus()
	if err != nil || !recovery.Quarantined ||
		!archiveDigestRE.MatchString(recovery.SnapshotSHA256) {
		return empty, ErrReconciliation
	}
	source, err := verifyCanonicalLedger[SourceLedgerStatement](
		evidence.Source, keys.Source, SourceLedgerSchema,
	)
	if err != nil || !validSourceLedger(source, repo, now) {
		return empty, ErrReconciliation
	}
	qual, err := verifyCanonicalLedger[QualLedgerStatement](
		evidence.Qual, keys.Qual, QualLedgerSchema,
	)
	if err != nil || !validQualLedger(qual, repo, now) {
		return empty, ErrReconciliation
	}
	fence, err := verifyCanonicalLedger[ReconciliationFence](
		evidence.Operator, keys.Operator, SourceFenceSchema,
	)
	if err != nil || !validReconciliationFence(fence, repo,
		recovery.SnapshotSHA256,
		hashLedgerBytes(evidence.Source.CanonicalJSON),
		hashLedgerBytes(evidence.Qual.CanonicalJSON), source, qual, now,
	) {
		return empty, ErrReconciliation
	}

	result := AuditOnlyReconciliation{
		SnapshotSHA256:               recovery.SnapshotSHA256,
		LatestSequence:               source.LastSequence,
		SignedSourceFacts:            len(source.Facts),
		SignedQualFacts:              len(qual.Facts),
		RecoveredSpoolQuarantined:    true,
		RequiresIndependentReview:    true,
		ActivationAuthorized:         false,
		LatestSourceExternallyProven: false,
	}
	err = s.withLock("worker", func() error {
		return s.withLock("queue", func() error {
			// Revalidate during locked inspection: changing the quarantine
			// or snapshot between signature verification and a read is not
			// permission to use a different historical dataset.
			current, err := s.RecoveryStatus()
			if err != nil || !current.Quarantined ||
				current.SnapshotSHA256 != recovery.SnapshotSHA256 {
				return ErrReconciliation
			}
			if err := s.validateQuarantinedReadOnly(); err != nil {
				return err
			}
			restored := map[string]Delivery{}
			for _, sub := range []string{"pending", "delivered", "archived"} {
				entries, err := os.ReadDir(filepath.Join(s.Root, sub))
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if entry.IsDir() || !validSnapshotEntry(sub+"/"+entry.Name()) {
						return ErrReconciliation
					}
					id := strings.TrimSuffix(entry.Name(), ".json")
					d, exists, err := s.read(sub, id)
					if err != nil || !exists {
						return ErrReconciliation
					}
					if _, repeated := restored[id]; repeated {
						return ErrReconciliation
					}
					restored[id] = d
				}
			}
			result.RestoredFacts = len(restored)
			accepted := make(map[string]SourceLedgerFact, len(source.Facts))
			for _, sf := range source.Facts {
				accepted[sf.DeliveryID] = sf
				d, exists := restored[sf.DeliveryID]
				if !exists {
					result.MissingFromRestored++
					continue
				}
				if d.PayloadSHA256 != sf.PayloadSHA256 ||
					d.EventType != sf.EventType ||
					d.ExternalThreadID != sf.ExternalThreadID ||
					d.RepositoryID != repo.ID ||
					d.RepositoryName != repo.FullName {
					result.RestoredInconsistencies++
				}
			}
			for id := range restored {
				if _, exists := accepted[id]; !exists {
					result.UnexpectedRestored++
				}
			}
			received := make(map[string]QualLedgerFact, len(qual.Facts))
			for _, qf := range qual.Facts {
				received[qf.DeliveryID] = qf
				sf, sourceKnown := accepted[qf.DeliveryID]
				if !sourceKnown || sf.PayloadSHA256 != qf.PayloadSHA256 {
					result.QualInconsistencies++
					continue
				}
				d, onDisk := restored[qf.DeliveryID]
				if !onDisk {
					continue // missing restored fact already counted
				}
				if d.State != "delivered" ||
					d.DeliveredEventID != qf.QualEventID ||
					d.PinnedTarget == nil ||
					d.PinnedTarget.ProjectID != qf.ProjectID ||
					d.PinnedTarget.WorkItemID != qf.WorkItemID ||
					d.PinnedTarget.ThreadID != qf.ThreadID {
					result.QualInconsistencies++
				}
			}
			for _, d := range restored {
				if d.State == "delivered" {
					if _, exists := received[d.DeliveryID]; !exists {
						result.QualInconsistencies++
					}
				}
			}
			result.RecordsMatch = result.MissingFromRestored == 0 &&
				result.UnexpectedRestored == 0 &&
				result.RestoredInconsistencies == 0 &&
				result.QualInconsistencies == 0
			// No pathway, including success, lifts restored quarantine.
			return nil
		})
	})
	if err != nil {
		return AuditOnlyReconciliation{}, err
	}
	return result, nil
}
