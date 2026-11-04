package githubadapter

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"time"
)

// An ACTIVE spool must have a source-origin declaration issued only when
// OpenSpool creates a previously nonexistent directory. Signed snapshot ZIPs
// intentionally contain only pending/delivered/archived/index data, never
// these origin files. RestoreSignedBackup creates quarantine markers instead.
//
// This is defense in depth against ACCIDENTALLY deleting both quarantine
// marker files and interpreting a restored old snapshot as a new live spool.
// It is not cryptographic freshness, a ledger, or an independent OS authority:
// a malicious actor with the same UID can forge local witness files.
const (
	activeOriginFile        = "active-source-origin.json"
	activeOriginWitnessFile = "active-source-origin-witness.json"
	activeOriginSchema      = "qual-github-adapter-fresh-spool-origin-v1"
)

type activeOrigin struct {
	Schema         string    `json:"schema"`
	NonceSHA256    string    `json:"nonce_sha256"`
	RootPathSHA256 string    `json:"root_path_sha256"`
	CreatedAt      time.Time `json:"created_at"`
}

func originPathSHA(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:])
}

func createFreshOrigin(root string) error {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return ErrSpool
	}
	nonce := sha256.Sum256(random)
	for i := range random {
		random[i] = 0
	}
	origin := activeOrigin{
		Schema: activeOriginSchema, NonceSHA256: hex.EncodeToString(nonce[:]),
		RootPathSHA256: originPathSHA(root), CreatedAt: time.Now().UTC(),
	}
	data, err := json.Marshal(origin)
	if err != nil {
		return ErrSpool
	}
	for _, name := range []string{activeOriginFile, activeOriginWitnessFile} {
		if err := writeAndSyncExclusive(filepath.Join(root, name), data); err != nil {
			return err
		}
	}
	return syncDirectory(root)
}

func requireFreshOrigin(root string) error {
	a, err := trustedFile(filepath.Join(root, activeOriginFile))
	b, otherErr := trustedFile(filepath.Join(root, activeOriginWitnessFile))
	if err != nil || otherErr != nil || !bytes.Equal(a, b) {
		return ErrSpool
	}
	var origin activeOrigin
	if json.Unmarshal(a, &origin) != nil ||
		origin.Schema != activeOriginSchema ||
		!archiveDigestRE.MatchString(origin.NonceSHA256) ||
		origin.RootPathSHA256 != originPathSHA(root) ||
		origin.CreatedAt.IsZero() || origin.CreatedAt.After(time.Now().UTC().Add(10*time.Minute)) {
		return ErrSpool
	}
	return nil
}

func (s *Spool) verifiedActiveOrigin() error {
	if s == nil || s.Root == "" {
		return ErrSpool
	}
	info, err := s.RecoveryStatus()
	if err != nil {
		return err
	}
	if info.Quarantined {
		return ErrRestoredQuarantine
	}
	if err := requireFreshOrigin(s.Root); err != nil {
		return err
	}
	return nil
}

// Absence of this fresh-origin marker never triggers implicit migration:
// a stale restored snapshot with stripped quarantine metadata must fail closed.
