package githubadapter

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReconciliationRejectsForgeryAndSignerSubstitution(t *testing.T) {
	f := newSignedProofFixture(t)
	base := f.evidence(t)
	cases := []struct {
		name   string
		change func(*SourceReconciliationEvidence, *SourceReconciliationKeys)
	}{
		{"signed_source_body_tampered", func(e *SourceReconciliationEvidence, _ *SourceReconciliationKeys) {
			e.Source.CanonicalJSON = append([]byte(nil), e.Source.CanonicalJSON...)
			e.Source.CanonicalJSON[9] ^= 1
		}},
		{"qual_signature_bitflip", func(e *SourceReconciliationEvidence, _ *SourceReconciliationKeys) {
			e.Qual.Signature = append([]byte(nil), e.Qual.Signature...)
			e.Qual.Signature[14] ^= 1
		}},
		{"operator_signature_missing", func(e *SourceReconciliationEvidence, _ *SourceReconciliationKeys) {
			e.Operator.Signature = nil
		}},
		{"source_key_invalid_length", func(_ *SourceReconciliationEvidence, k *SourceReconciliationKeys) {
			k.Source = k.Source[:10]
		}},
		{"qual_key_replaced_with_source_key", func(_ *SourceReconciliationEvidence, k *SourceReconciliationKeys) {
			k.Qual = append(ed25519.PublicKey(nil), k.Source...)
		}},
		{"operator_key_replaced_with_source_key", func(_ *SourceReconciliationEvidence, k *SourceReconciliationKeys) {
			k.Operator = append(ed25519.PublicKey(nil), k.Source...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			k := f.keys
			tc.change(&e, &k)
			if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, k, e, f.now); !errors.Is(err, ErrReconciliation) {
				t.Fatalf("forged or wrong-key evidence was accepted: %v", err)
			}
		})
	}
}

func TestReconciliationRejectsSignedIncompleteOrCrossTenantEvidence(t *testing.T) {
	cases := []struct {
		name   string
		change func(*signedProofFixture)
	}{
		{"sequence_gap", func(f *signedProofFixture) { f.source.Facts[0].Sequence = 2 }},
		{"latest_sequence_claim_not_backed_by_records", func(f *signedProofFixture) { f.source.LastSequence = 3 }},
		{"source_repo_id_changed", func(f *signedProofFixture) { f.source.RepositoryID++ }},
		{"qual_repo_name_changed", func(f *signedProofFixture) { f.qual.RepositoryName = "attacker/Qual" }},
		{"fence_snapshot_sha_wrong", func(f *signedProofFixture) { f.fence.SnapshotSHA256 = strings.Repeat("a", 64) }},
		{"fence_old_sequence_with_new_source", func(f *signedProofFixture) { f.fence.LatestSequence = 0 }},
		{"fence_issued_before_evidence", func(f *signedProofFixture) { f.fence.IssuedAt = f.source.CapturedAt.Add(-time.Minute) }},
		{"fence_expired_even_with_valid_signature", func(f *signedProofFixture) {
			f.source.CapturedAt = f.now.Add(-35 * time.Minute)
			f.qual.CapturedAt = f.now.Add(-34 * time.Minute)
			f.fence.IssuedAt = f.now.Add(-25 * time.Minute)
		}},
		{"source_thread_from_other_repo", func(f *signedProofFixture) { f.source.Facts[0].ExternalThreadID = "attacker/Qual/pull/1" }},
		{"source_invalid_event_kind", func(f *signedProofFixture) { f.source.Facts[0].EventType = "push.opened" }},
		{"source_malformed_thread_number", func(f *signedProofFixture) { f.source.Facts[0].ExternalThreadID = "orz-i/Qual/pull/0001" }},
		{"duplicate_original_hmac_body_with_different_delivery_uuid", func(f *signedProofFixture) {
			copy := f.source.Facts[0]
			copy.Sequence = 2
			copy.DeliveryID = "88888888-8888-4888-8888-888888888888"
			f.source.Facts = append(f.source.Facts, copy)
			f.source.LastSequence = 2
			f.fence.LatestSequence = 2
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSignedProofFixture(t)
			tc.change(f)
			if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now); !errors.Is(err, ErrReconciliation) {
				t.Fatalf("incomplete/cross-repository evidence accepted: %v", err)
			}
		})
	}
}

func TestReconciliationRejectsCanonicalJsonDuplicateKeysEvenWhenSigned(t *testing.T) {
	f := newSignedProofFixture(t)
	real := f.evidence(t)
	original := real.Source.CanonicalJSON
	// Construct a *validly signed* payload containing a duplicate schema
	// key. json.Unmarshal alone would accept it; our canonical check must not.
	prefix := []byte("{\"schema\":\"" + SourceLedgerSchema + "\",")
	forged := append(prefix, original[1:]...)
	real.Source.CanonicalJSON = forged
	real.Source.Signature = ed25519.Sign(f.private[0], append([]byte(SourceLedgerSchema+"\x00"), forged...))
	fence := f.fence
	fence.SourceSHA256 = hashLedgerBytes(forged)
	fence.QualSHA256 = hashLedgerBytes(real.Qual.CanonicalJSON)
	real.Operator = signSyntheticLedger(t, fence, SourceFenceSchema, f.private[2])
	if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, real, f.now); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("valid signature over noncanonical duplicate-key JSON accepted: %v", err)
	}

	// Even when signed and canonical, extra fields are not part of the
	// reviewed protocol. Reject instead of silently ignoring them.
	fresh := f.evidence(t)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(fresh.Source.CanonicalJSON, &m); err != nil {
		t.Fatal(err)
	}
	m["unexpected_approval"] = json.RawMessage("true")
	extra, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Source.CanonicalJSON = extra
	fresh.Source.Signature = ed25519.Sign(f.private[0], append([]byte(SourceLedgerSchema+"\x00"), extra...))
	fence.SourceSHA256 = hashLedgerBytes(extra)
	fresh.Operator = signSyntheticLedger(t, fence, SourceFenceSchema, f.private[2])
	if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, fresh, f.now); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("signed unknown evidence field accepted: %v", err)
	}
}

func TestReconciliationDoesNotConvertActiveOrTamperedSpoolToSafeRestoration(t *testing.T) {
	f := newSignedProofFixture(t)
	e := f.evidence(t)
	// Actual original ACTIVE Spool is not a restored historical snapshot:
	// never offer an audit-only report that masquerades as a restore check.
	source, err := OpenSpool(filepath.Join(t.TempDir(), "fresh-only"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, e, f.now); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("normal source accepted as restored signature target: %v", err)
	}
	if err := os.Chmod(filepath.Join(f.restored.Root, recoveryMarkerFile), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, e, f.now); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("tampered recovered marker accepted: %v", err)
	}
}

func TestReconciliationExplicitSourceFactConflictWithRestoredSnapshot(t *testing.T) {
	f := newSignedProofFixture(t)
	// The source signer claims an allowed but different Action for an
	// existing delivery. The signature verifies; the restored fact does not.
	f.source.Facts[0].EventType = "pull_request.edited"
	result, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
	if err != nil || result.RestoredInconsistencies != 1 ||
		result.RecordsMatch || result.ActivationAuthorized {
		t.Fatalf("signed conflicting identity passed reconciliation: %+v %v", result, err)
	}
}
