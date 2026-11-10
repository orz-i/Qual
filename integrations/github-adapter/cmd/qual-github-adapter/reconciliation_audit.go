package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

const maxReconciliationBundleBytes int64 = 18 << 20
const maxReconciliationKeyBytes int64 = 2048

// The audit input is explicitly supplied by a trusted Operator. This
// program does not connect to GitHub, create/sign evidence, or unlock a
// restored queue; a local file and three different keys are not proof of
// independent authorities or a current GitHub high watermark.
type auditKeys struct {
	Source   ed25519.PublicKey `json:"source"`
	Qual     ed25519.PublicKey `json:"qual"`
	Operator ed25519.PublicKey `json:"operator"`
}
type auditSigned struct {
	CanonicalJSON []byte `json:"canonical_json"`
	Signature     []byte `json:"signature"`
}
type auditBundle struct {
	Source   auditSigned `json:"source"`
	Qual     auditSigned `json:"qual"`
	Operator auditSigned `json:"operator"`
}

// readOwnerOnlyAuditInput binds bytes to one O_NOFOLLOW file descriptor and
// bounds size before allocating memory. Public keys are not secrets, but
// protecting the PINNED keys against path substitution is essential.
func readOwnerOnlyAuditInput(path string, maxBytes int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, githubadapter.ErrReconciliation
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 ||
		parent.Mode().Perm()&0022 != 0 {
		return nil, githubadapter.ErrReconciliation
	}
	if stat, ok := parent.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Getuid() {
		return nil, githubadapter.ErrReconciliation
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, githubadapter.ErrReconciliation
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil ||
		stat.Mode&syscall.S_IFMT != syscall.S_IFREG ||
		stat.Mode&0777 != 0600 || int(stat.Uid) != os.Getuid() ||
		stat.Size < 1 || stat.Size > maxBytes {
		return nil, githubadapter.ErrReconciliation
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, githubadapter.ErrReconciliation
	}
	return data, nil
}

func decodeExactAuditJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return githubadapter.ErrReconciliation
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return githubadapter.ErrReconciliation
	}
	return nil
}

// auditRestoredSpool never emits the signed evidence bodies or keys. Even
// records_match=true remains an AUDIT finding, not an authorization.
func auditRestoredSpool(spool *githubadapter.Spool, repo githubadapter.Repository,
	keyPath, evidencePath string, now time.Time) (githubadapter.AuditOnlyReconciliation, error) {
	var zero githubadapter.AuditOnlyReconciliation
	keyBytes, err := readOwnerOnlyAuditInput(keyPath, maxReconciliationKeyBytes)
	if err != nil {
		return zero, err
	}
	evidenceBytes, err := readOwnerOnlyAuditInput(evidencePath, maxReconciliationBundleBytes)
	if err != nil {
		return zero, err
	}
	var keys auditKeys
	var bundle auditBundle
	if decodeExactAuditJSON(keyBytes, &keys) != nil ||
		decodeExactAuditJSON(evidenceBytes, &bundle) != nil {
		return zero, githubadapter.ErrReconciliation
	}
	return spool.VerifyRestoredSourceEvidence(repo, githubadapter.SourceReconciliationKeys{
		Source: keys.Source, Qual: keys.Qual, Operator: keys.Operator,
	}, githubadapter.SourceReconciliationEvidence{
		Source:   githubadapter.SignedLedgerBytes{CanonicalJSON: bundle.Source.CanonicalJSON, Signature: bundle.Source.Signature},
		Qual:     githubadapter.SignedLedgerBytes{CanonicalJSON: bundle.Qual.CanonicalJSON, Signature: bundle.Qual.Signature},
		Operator: githubadapter.SignedLedgerBytes{CanonicalJSON: bundle.Operator.CanonicalJSON, Signature: bundle.Operator.Signature},
	}, now)
}

func runReconciliationAudit(repo githubadapter.Repository, spool *githubadapter.Spool,
	keyPath, evidencePath string) error {
	report, err := auditRestoredSpool(spool, repo, keyPath, evidencePath, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("read-only reconciliation rejected; check independently pinned keys, source evidence and quarantine")
	}
	output(report)
	if !report.RecordsMatch {
		return fmt.Errorf("read-only reconciliation detected missing or inconsistent source facts; quarantine unchanged")
	}
	return nil
}
