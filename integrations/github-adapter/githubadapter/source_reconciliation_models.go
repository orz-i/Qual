package githubadapter

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

// These statements must originate from independent append-only source and
// Qual ledger services, NEVER from the restored Adapter itself. All issuers'
// public keys must be pinned by a separate trusted Operator.
const (
	SourceLedgerSchema = "qual-github-independent-source-ledger-v1"
	QualLedgerSchema   = "qual-github-independent-qual-ledger-v1"
	SourceFenceSchema  = "qual-github-independent-recovery-fence-v1"

	maxReconciliationRecords = 10000
	maxReconciliationBytes   = 4 << 20
)

var ErrReconciliation = errors.New("independent signed source reconciliation failed")

type SourceLedgerFact struct {
	Sequence         uint64 `json:"sequence"`
	DeliveryID       string `json:"delivery_id"`
	PayloadSHA256    string `json:"payload_sha256"`
	EventType        string `json:"event_type"`
	ExternalThreadID string `json:"external_thread_id"`
}
type SourceLedgerStatement struct {
	Schema         string             `json:"schema"`
	RepositoryID   int64              `json:"repository_id"`
	RepositoryName string             `json:"repository_name"`
	LastSequence   uint64             `json:"last_sequence"`
	CapturedAt     time.Time          `json:"captured_at"`
	Facts          []SourceLedgerFact `json:"facts"`
}

type QualLedgerFact struct {
	DeliveryID    string `json:"delivery_id"`
	PayloadSHA256 string `json:"payload_sha256"`
	QualEventID   string `json:"qual_event_id"`
	ProjectID     string `json:"project_id"`
	WorkItemID    string `json:"work_item_id"`
	ThreadID      string `json:"thread_id"`
}
type QualLedgerStatement struct {
	Schema         string           `json:"schema"`
	RepositoryID   int64            `json:"repository_id"`
	RepositoryName string           `json:"repository_name"`
	CapturedAt     time.Time        `json:"captured_at"`
	Facts          []QualLedgerFact `json:"facts"`
}

// Independent latest-source fence binds BOTH signed ledgers and a particular
// restored snapshot. It does NOT authorize activation or establish that a
// keyholder used an independent OS Principal in a real deployment.
type ReconciliationFence struct {
	Schema         string    `json:"schema"`
	RepositoryID   int64     `json:"repository_id"`
	RepositoryName string    `json:"repository_name"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	LatestSequence uint64    `json:"latest_sequence"`
	SourceSHA256   string    `json:"source_sha256"`
	QualSHA256     string    `json:"qual_sha256"`
	IssuedAt       time.Time `json:"issued_at"`
}

// SignedLedgerBytes signs precisely the canonical JSON byte string, prefixed
// with an unambiguous domain and NUL. No signing function is included in the
// shipping adapter. Unit tests use synthetic private keys only.
type SignedLedgerBytes struct {
	CanonicalJSON []byte
	Signature     []byte
}
type SourceReconciliationKeys struct {
	Source   ed25519.PublicKey
	Qual     ed25519.PublicKey
	Operator ed25519.PublicKey
}
type SourceReconciliationEvidence struct {
	Source   SignedLedgerBytes
	Qual     SignedLedgerBytes
	Operator SignedLedgerBytes
}

// AuditOnlyReconciliation is not a release authorization. Even all records
// matching cannot attest to remote source freshness, principal separation,
// or protected rollout by a trusted Operator.
type AuditOnlyReconciliation struct {
	SnapshotSHA256               string `json:"snapshot_sha256"`
	LatestSequence               uint64 `json:"latest_sequence"`
	SignedSourceFacts            int    `json:"signed_source_facts"`
	SignedQualFacts              int    `json:"signed_qual_facts"`
	RestoredFacts                int    `json:"restored_facts"`
	MissingFromRestored          int    `json:"missing_from_restored"`
	UnexpectedRestored           int    `json:"unexpected_restored"`
	QualInconsistencies          int    `json:"qual_inconsistencies"`
	RestoredInconsistencies      int    `json:"restored_inconsistencies"`
	RecordsMatch                 bool   `json:"records_match"`
	RecoveredSpoolQuarantined    bool   `json:"recovered_spool_quarantined"`
	ActivationAuthorized         bool   `json:"activation_authorized"`
	RequiresIndependentReview    bool   `json:"requires_independent_review"`
	LatestSourceExternallyProven bool   `json:"latest_source_externally_proven"`
}

func validReconciliationKeys(keys SourceReconciliationKeys) bool {
	if len(keys.Source) != ed25519.PublicKeySize ||
		len(keys.Qual) != ed25519.PublicKeySize ||
		len(keys.Operator) != ed25519.PublicKeySize {
		return false
	}
	return !bytes.Equal(keys.Source, keys.Qual) &&
		!bytes.Equal(keys.Source, keys.Operator) &&
		!bytes.Equal(keys.Qual, keys.Operator)
}

// Verify an EXACT signed canonical struct representation, rejecting unknown,
// duplicated, or differently encoded JSON fields even if a signer sent them.
func verifyCanonicalLedger[T any](signed SignedLedgerBytes, key ed25519.PublicKey, domain string) (T, error) {
	var result T
	if len(signed.CanonicalJSON) == 0 || len(signed.CanonicalJSON) > maxReconciliationBytes ||
		len(signed.Signature) != ed25519.SignatureSize || len(key) != ed25519.PublicKeySize {
		return result, ErrReconciliation
	}
	msg := append([]byte(domain+"\x00"), signed.CanonicalJSON...)
	if !ed25519.Verify(key, msg, signed.Signature) {
		return result, ErrReconciliation
	}
	dec := json.NewDecoder(bytes.NewReader(signed.CanonicalJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return result, ErrReconciliation
	}
	var rest any
	if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
		return result, ErrReconciliation
	}
	encoded, err := json.Marshal(result)
	if err != nil || !bytes.Equal(encoded, signed.CanonicalJSON) {
		return result, ErrReconciliation
	}
	return result, nil
}

func validReconciliationRepo(repo Repository, id int64, name string) bool {
	return repo.ID > 0 && repo.FullName != "" && id == repo.ID && name == repo.FullName
}
func validReconciliationTime(t, now time.Time) bool {
	return !t.IsZero() && t.After(time.Unix(1, 0)) && !t.After(now.Add(5*time.Minute))
}

func validSourceLedger(st SourceLedgerStatement, repo Repository, now time.Time) bool {
	if st.Schema != SourceLedgerSchema || !validReconciliationRepo(repo, st.RepositoryID, st.RepositoryName) ||
		!validReconciliationTime(st.CapturedAt, now) || len(st.Facts) > maxReconciliationRecords ||
		st.LastSequence != uint64(len(st.Facts)) {
		return false
	}
	seenID := map[string]bool{}
	seenHash := map[string]bool{}
	for i, fact := range st.Facts {
		if fact.Sequence != uint64(i+1) || !deliveryIDRE.MatchString(fact.DeliveryID) ||
			!archiveDigestRE.MatchString(fact.PayloadSHA256) ||
			fact.EventType == "" || len(fact.EventType) > 120 ||
			fact.ExternalThreadID == "" || len(fact.ExternalThreadID) > 256 ||
			seenID[fact.DeliveryID] || seenHash[fact.PayloadSHA256] ||
			!validSignedGitHubOrigin(fact, repo) {
			return false
		}
		seenID[fact.DeliveryID] = true
		seenHash[fact.PayloadSHA256] = true
	}
	return true
}

// A signed checkpoint may list only this explicitly pinned GitHub source.
// Even an independent signer cannot authorize arbitrary cross-repo facts.
func validSignedGitHubOrigin(f SourceLedgerFact, repo Repository) bool {
	parts := strings.Split(f.EventType, ".")
	if len(parts) != 2 || !allowedActions[parts[0]][parts[1]] {
		return false
	}
	prefix := repo.FullName + "/pull/"
	if parts[0] == "issues" {
		prefix = repo.FullName + "/issues/"
	}
	if !strings.HasPrefix(f.ExternalThreadID, prefix) {
		return false
	}
	number := strings.TrimPrefix(f.ExternalThreadID, prefix)
	n, err := strconv.ParseUint(number, 10, 32)
	return err == nil && n > 0 && n <= 1000000000 && strconv.FormatUint(n, 10) == number
}

func validQualLedger(st QualLedgerStatement, repo Repository, now time.Time) bool {
	if st.Schema != QualLedgerSchema || !validReconciliationRepo(repo, st.RepositoryID, st.RepositoryName) ||
		!validReconciliationTime(st.CapturedAt, now) || len(st.Facts) > maxReconciliationRecords {
		return false
	}
	var previous string
	seenEvent := map[string]bool{}
	for _, f := range st.Facts {
		if !deliveryIDRE.MatchString(f.DeliveryID) || !archiveDigestRE.MatchString(f.PayloadSHA256) ||
			!deliveryIDRE.MatchString(f.QualEventID) || !deliveryIDRE.MatchString(f.ProjectID) ||
			!deliveryIDRE.MatchString(f.WorkItemID) || !deliveryIDRE.MatchString(f.ThreadID) ||
			f.DeliveryID <= previous || seenEvent[f.QualEventID] {
			return false
		}
		seenEvent[f.QualEventID] = true
		previous = f.DeliveryID
	}
	return true
}
func validReconciliationFence(f ReconciliationFence, repo Repository, snapshot string,
	sourceSHA, qualSHA string, source SourceLedgerStatement, qual QualLedgerStatement, now time.Time) bool {
	return f.Schema == SourceFenceSchema &&
		validReconciliationRepo(repo, f.RepositoryID, f.RepositoryName) &&
		archiveDigestRE.MatchString(snapshot) && f.SnapshotSHA256 == snapshot &&
		f.SourceSHA256 == sourceSHA && f.QualSHA256 == qualSHA &&
		validReconciliationTime(f.IssuedAt, now) &&
		!f.IssuedAt.Before(now.Add(-15*time.Minute)) &&
		f.LatestSequence == source.LastSequence &&
		!f.IssuedAt.Before(source.CapturedAt) && !f.IssuedAt.Before(qual.CapturedAt)
}
func hashLedgerBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
