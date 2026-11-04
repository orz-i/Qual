package githubadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// A valid detached HMAC proves only snapshot integrity. It cannot attest
// that no new GitHub/Qual facts were accepted after the backup. Every
// restored spool must be quarantined before its directory is published.
// Never allow plain --approve / a shared-UID local flag to clear this marker.
const recoveryMarkerFile = "recovery-quarantine.json"
const recoveryWitnessFile = "recovery-witness.json"
const recoveryMarkerSchema = "qual-github-adapter-restored-quarantine-v1"

var ErrRestoredQuarantine = errors.New("restored spool quarantined until trusted external source reconciliation")

// RecoveryStatus contains only sanitized on-disk metadata and deliberately
// does not assert an independently authenticated Human/Operator approval.
type RecoveryStatus struct {
	Quarantined                  bool   `json:"quarantined"`
	RequiresSourceReconciliation bool   `json:"requires_source_reconciliation"`
	SnapshotSHA256               string `json:"snapshot_sha256,omitempty"`
}

type recoveryMarker struct {
	Schema                       string    `json:"schema"`
	Quarantined                  bool      `json:"quarantined"`
	RequiresSourceReconciliation bool      `json:"requires_source_reconciliation"`
	SnapshotSHA256               string    `json:"snapshot_sha256"`
	CreatedAt                    time.Time `json:"created_at"`
}

func (s *Spool) RecoveryStatus() (RecoveryStatus, error) {
	if s == nil || s.Root == "" {
		return RecoveryStatus{}, ErrSpool
	}
	data, err := trustedFile(filepath.Join(s.Root, recoveryMarkerFile))
	witness, witnessErr := trustedFile(filepath.Join(s.Root, recoveryWitnessFile))
	if errors.Is(err, os.ErrNotExist) && errors.Is(witnessErr, os.ErrNotExist) {
		// A preexisting ordinary spool has never passed RestoreSignedBackup.
		// A shared OS UID can still tamper with both files; these two local
		// replicas are NOT an independent cryptographic authority.
		return RecoveryStatus{}, nil
	}
	if err != nil || witnessErr != nil || !bytes.Equal(data, witness) {
		return RecoveryStatus{}, ErrSpool
	}
	var m recoveryMarker
	if json.Unmarshal(data, &m) != nil ||
		m.Schema != recoveryMarkerSchema || !m.Quarantined ||
		!m.RequiresSourceReconciliation ||
		!archiveDigestRE.MatchString(m.SnapshotSHA256) ||
		m.CreatedAt.IsZero() || m.CreatedAt.After(time.Now().UTC().Add(10*time.Minute)) {
		return RecoveryStatus{}, ErrSpool
	}
	return RecoveryStatus{
		Quarantined: true, RequiresSourceReconciliation: true,
		SnapshotSHA256: m.SnapshotSHA256,
	}, nil
}

func (s *Spool) requireActive() error {
	return s.verifiedActiveOrigin()
}

// installRecoveryQuarantine is called on an isolated stage after verifying
// ALL snapshot records, before the atomic no-replace publication. The
// marker must be durable before the restored spool can become addressable.
func (s *Spool) installRecoveryQuarantine(snapshotSHA string) error {
	if !archiveDigestRE.MatchString(snapshotSHA) {
		return ErrInvalid
	}
	m := recoveryMarker{
		Schema:      recoveryMarkerSchema,
		Quarantined: true, RequiresSourceReconciliation: true,
		SnapshotSHA256: snapshotSHA, CreatedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return ErrSpool
	}
	for _, name := range []string{recoveryMarkerFile, recoveryWitnessFile} {
		path := filepath.Join(s.Root, name)
		if err := writeAndSyncExclusive(path, raw); err != nil {
			return err
		}
	}
	return syncDirectory(s.Root)
}
