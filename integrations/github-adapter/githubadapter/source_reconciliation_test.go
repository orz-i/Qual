package githubadapter

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type signedProofFixture struct {
	restored *Spool
	source   SourceLedgerStatement
	qual     QualLedgerStatement
	fence    ReconciliationFence
	keys     SourceReconciliationKeys
	private  [3]ed25519.PrivateKey
	now      time.Time
	first    Delivery
}

func signSyntheticLedger(t *testing.T, data any, domain string, key ed25519.PrivateKey) SignedLedgerBytes {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	msg := append([]byte(domain+"\x00"), raw...)
	return SignedLedgerBytes{CanonicalJSON: raw, Signature: ed25519.Sign(key, msg)}
}

func (f *signedProofFixture) evidence(t *testing.T) SourceReconciliationEvidence {
	t.Helper()
	src := signSyntheticLedger(t, f.source, SourceLedgerSchema, f.private[0])
	qual := signSyntheticLedger(t, f.qual, QualLedgerSchema, f.private[1])
	fence := f.fence
	fence.SourceSHA256 = hashLedgerBytes(src.CanonicalJSON)
	fence.QualSHA256 = hashLedgerBytes(qual.CanonicalJSON)
	return SourceReconciliationEvidence{
		Source: src, Qual: qual, Operator: signSyntheticLedger(t, fence, SourceFenceSchema, f.private[2]),
	}
}

func sourceRecord(d Delivery, seq uint64) SourceLedgerFact {
	return SourceLedgerFact{
		Sequence: seq, DeliveryID: d.DeliveryID, PayloadSHA256: d.PayloadSHA256,
		EventType: d.EventType, ExternalThreadID: d.ExternalThreadID,
	}
}
func qualRecord(d Delivery, eventID string) QualLedgerFact {
	return QualLedgerFact{
		DeliveryID: d.DeliveryID, PayloadSHA256: d.PayloadSHA256, QualEventID: eventID,
		ProjectID: testProject, WorkItemID: testWork, ThreadID: testThread,
	}
}

func newSignedProofFixture(t *testing.T) *signedProofFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	source, err := OpenSpool(filepath.Join(t.TempDir(), "signed-source"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDelivery(fixtureBody("opened"), fixtureDeliveryID, "pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	if inserted, err := source.Enqueue(d); err != nil || !inserted {
		t.Fatal("synthetic signed source", err)
	}
	target := Target{ProjectID: testProject, WorkItemID: testWork, ThreadID: testThread}
	d, err = source.PinTarget(d, target)
	if err != nil {
		t.Fatal(err)
	}
	d.Attempts = 1
	d.DeliveredEventID = testEventID
	if err := source.Update(d, true); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ArchiveDelivered(1); err != nil {
		t.Fatal(err)
	}
	syntheticKey := []byte(strings.Repeat("synthetic-operator-key-separate-from-PI-", 2))
	archive := filepath.Join(t.TempDir(), "sealed-old.zip")
	created, err := source.CreateSignedBackup(archive, syntheticKey)
	if err != nil {
		t.Fatal(err)
	}
	restored, verified, err := RestoreSignedBackup(archive, filepath.Join(t.TempDir(), "restored"), syntheticKey)
	if err != nil || verified.ArchiveSHA256 != created.ArchiveSHA256 {
		t.Fatal("isolated synthetic restore", err)
	}
	var private [3]ed25519.PrivateKey
	var keys SourceReconciliationKeys
	for i := 0; i < 3; i++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		private[i] = priv
		switch i {
		case 0:
			keys.Source = pub
		case 1:
			keys.Qual = pub
		case 2:
			keys.Operator = pub
		}
	}
	origin := now.Add(-2 * time.Minute)
	return &signedProofFixture{
		restored: restored, private: private, keys: keys, now: now, first: d,
		source: SourceLedgerStatement{
			Schema: SourceLedgerSchema, RepositoryID: fixtureRepo.ID, RepositoryName: fixtureRepo.FullName,
			CapturedAt: origin, LastSequence: 1, Facts: []SourceLedgerFact{sourceRecord(d, 1)},
		},
		qual: QualLedgerStatement{
			Schema: QualLedgerSchema, RepositoryID: fixtureRepo.ID, RepositoryName: fixtureRepo.FullName,
			CapturedAt: origin.Add(30 * time.Second), Facts: []QualLedgerFact{qualRecord(d, testEventID)},
		},
		fence: ReconciliationFence{
			Schema: SourceFenceSchema, RepositoryID: fixtureRepo.ID, RepositoryName: fixtureRepo.FullName,
			SnapshotSHA256: verified.ArchiveSHA256, LatestSequence: 1, IssuedAt: now.Add(-time.Minute),
		},
	}
}

func TestReconciliationExactIndependentSignaturesStillNeverUnlock(t *testing.T) {
	f := newSignedProofFixture(t)
	marker := filepath.Join(f.restored.Root, recoveryMarkerFile)
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
	if err != nil {
		t.Fatal(err)
	}
	if !report.RecordsMatch || report.SignedSourceFacts != 1 || report.SignedQualFacts != 1 ||
		report.RestoredFacts != 1 || report.LatestSequence != 1 ||
		!report.RecoveredSpoolQuarantined || !report.RequiresIndependentReview ||
		report.ActivationAuthorized || report.LatestSourceExternallyProven {
		t.Fatalf("valid evidence cannot imply production activation: %+v", report)
	}
	after, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("offline verification mutated restored quarantine")
	}
	if _, err := NewHandler(fixtureRepo, []byte(strings.Repeat("s", 40)), f.restored); !errors.Is(err, ErrRestoredQuarantine) {
		t.Fatalf("verified report accidentally unlocked receive path: %v", err)
	}
	p := Publisher{Spool: f.restored, Repository: fixtureRepo, Token: "synthetic-only"}
	if stats, err := p.PublishOnce(context.Background()); !errors.Is(err, ErrRestoredQuarantine) || stats.Attempted != 0 {
		t.Fatalf("verified report accidentally unlocked Qual writer: %+v %v", stats, err)
	}
}

func TestSignedLatestSourceReportsMissingPostSnapshotFact(t *testing.T) {
	f := newSignedProofFixture(t)
	// T1 is an independently witnessed accepted fact missing from T0.
	later, err := ParseDelivery(fixtureBody("edited"), "22222222-2222-4222-8222-222222222222",
		"pull_request", fixtureRepo, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	f.source.Facts = append(f.source.Facts, sourceRecord(later, 2))
	f.source.LastSequence = 2
	f.qual.Facts = append(f.qual.Facts, qualRecord(later, "77777777-7777-4777-8777-777777777777"))
	f.fence.LatestSequence = 2
	report, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
	if err != nil {
		t.Fatal(err)
	}
	if report.RecordsMatch || report.MissingFromRestored != 1 ||
		report.LatestSequence != 2 || report.ActivationAuthorized {
		t.Fatalf("old signed backup falsely reconciled post-T0 event: %+v", report)
	}
}

func TestSignedFenceRejectsValidButOldSourceCheckpoint(t *testing.T) {
	f := newSignedProofFixture(t)
	f.fence.LatestSequence = 2
	if _, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("valid but stale source checkpoint accepted as latest: %v", err)
	}
}

func TestQualLedgerCannotHideDeliveredEventOrChangeBoundTarget(t *testing.T) {
	t.Run("missing_qual_receipt", func(t *testing.T) {
		f := newSignedProofFixture(t)
		f.qual.Facts = nil
		report, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
		if err != nil || report.QualInconsistencies != 1 || report.RecordsMatch || report.ActivationAuthorized {
			t.Fatalf("missing Qual audit approved: %+v %v", report, err)
		}
	})
	t.Run("wrong_work_item", func(t *testing.T) {
		f := newSignedProofFixture(t)
		f.qual.Facts[0].WorkItemID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		report, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
		if err != nil || report.QualInconsistencies != 1 || report.RecordsMatch {
			t.Fatalf("independently signed wrong WorkItem approved: %+v %v", report, err)
		}
	})
	t.Run("unknown_qual_fact", func(t *testing.T) {
		f := newSignedProofFixture(t)
		extra := f.qual.Facts[0]
		extra.DeliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
		extra.QualEventID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
		extra.PayloadSHA256 = strings.Repeat("e", 64)
		f.qual.Facts = append(f.qual.Facts, extra)
		report, err := f.restored.VerifyRestoredSourceEvidence(fixtureRepo, f.keys, f.evidence(t), f.now)
		if err != nil || report.QualInconsistencies != 1 || report.RecordsMatch {
			t.Fatalf("Qual Event with no source record accepted: %+v %v", report, err)
		}
	})
}
