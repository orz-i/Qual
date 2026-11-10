package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func signAuditFixture(t *testing.T, in any, domain string, secret ed25519.PrivateKey) auditSigned {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return auditSigned{CanonicalJSON: raw,
		Signature: ed25519.Sign(secret, append([]byte(domain+"\x00"), raw...))}
}

func auditJSON(t *testing.T, path string, in any) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReconciliationAuditIsReadOnlyAndNeverAuthorizesRestore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := githubadapter.Repository{ID: 1443998164, FullName: "orz-i/Qual"}
	dir := t.TempDir()
	s, err := githubadapter.InitNewSpool(filepath.Join(dir, "source"))
	if err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	projectID := "22222222-2222-4222-8222-222222222222"
	workID := "33333333-3333-4333-8333-333333333333"
	threadID := "44444444-4444-4444-8444-444444444444"
	eventID := "55555555-5555-4555-8555-555555555555"
	d := githubadapter.Delivery{
		DeliveryID: id, RepositoryID: repo.ID, RepositoryName: repo.FullName,
		ThreadKind: "pull_request", ExternalThreadID: "orz-i/Qual/pull/1",
		EventType: "pull_request.opened", OccurredAt: now.Add(-time.Minute),
		ReceivedAt: now.Add(-time.Minute), PayloadSHA256: strings.Repeat("a", 64),
		Summary: "GitHub PR #1 opened", State: "pending",
	}
	if newRecord, err := s.Enqueue(d); err != nil || !newRecord {
		t.Fatalf("fresh delivery: %v", err)
	}
	d, err = s.PinTarget(d, githubadapter.Target{
		ProjectID: projectID, WorkItemID: workID, ThreadID: threadID,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.Attempts = 1
	d.DeliveredEventID = eventID
	if err := s.Update(d, true); err != nil {
		t.Fatal(err)
	}
	snapshotKey := []byte(strings.Repeat("synthetic-test-operator-", 2))
	zip := filepath.Join(dir, "old-signed.zip")
	created, err := s.CreateSignedBackup(zip, snapshotKey)
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := githubadapter.RestoreSignedBackup(zip, filepath.Join(dir, "restored"), snapshotKey)
	if err != nil {
		t.Fatal(err)
	}
	var keys auditKeys
	var privates [3]ed25519.PrivateKey
	for i := range privates {
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		privates[i] = private
		switch i {
		case 0:
			keys.Source = pub
		case 1:
			keys.Qual = pub
		case 2:
			keys.Operator = pub
		}
	}
	source := githubadapter.SourceLedgerStatement{
		Schema: githubadapter.SourceLedgerSchema, RepositoryID: repo.ID,
		RepositoryName: repo.FullName, LastSequence: 1, CapturedAt: now.Add(-50 * time.Second),
		Facts: []githubadapter.SourceLedgerFact{{
			Sequence: 1, DeliveryID: id, PayloadSHA256: d.PayloadSHA256,
			EventType: d.EventType, ExternalThreadID: d.ExternalThreadID,
		}},
	}
	qual := githubadapter.QualLedgerStatement{
		Schema: githubadapter.QualLedgerSchema, RepositoryID: repo.ID,
		RepositoryName: repo.FullName, CapturedAt: now.Add(-40 * time.Second),
		Facts: []githubadapter.QualLedgerFact{{
			DeliveryID: id, PayloadSHA256: d.PayloadSHA256, QualEventID: eventID,
			ProjectID: projectID, WorkItemID: workID, ThreadID: threadID,
		}},
	}
	signBundle := func() auditBundle {
		t.Helper()
		sourceSignature := signAuditFixture(t, source, githubadapter.SourceLedgerSchema, privates[0])
		qualSignature := signAuditFixture(t, qual, githubadapter.QualLedgerSchema, privates[1])
		sourceHash := sha256.Sum256(sourceSignature.CanonicalJSON)
		qualHash := sha256.Sum256(qualSignature.CanonicalJSON)
		fence := githubadapter.ReconciliationFence{
			Schema: githubadapter.SourceFenceSchema, RepositoryID: repo.ID,
			RepositoryName: repo.FullName, SnapshotSHA256: created.ArchiveSHA256,
			LatestSequence: source.LastSequence,
			SourceSHA256:   hex.EncodeToString(sourceHash[:]), QualSHA256: hex.EncodeToString(qualHash[:]),
			IssuedAt: now.Add(-time.Second),
		}
		return auditBundle{
			Source: sourceSignature, Qual: qualSignature,
			Operator: signAuditFixture(t, fence, githubadapter.SourceFenceSchema, privates[2]),
		}
	}
	keysPath := filepath.Join(dir, "trusted-keys.json")
	evidencePath := filepath.Join(dir, "signed-evidence.json")
	auditJSON(t, keysPath, keys)
	auditJSON(t, evidencePath, signBundle())

	// The verified signatures and matching facts do not establish independent
	// off-host authority or authorization to activate recovered ingress.
	report, err := auditRestoredSpool(restored, repo, keysPath, evidencePath, now)
	if err != nil || !report.RecordsMatch || report.ActivationAuthorized ||
		report.LatestSourceExternallyProven || !report.RecoveredSpoolQuarantined {
		t.Fatalf("audit should match without ever unlocking: %+v %v", report, err)
	}
	if err := runReconciliationAudit(repo, restored, keysPath, evidencePath); err != nil {
		t.Fatalf("read-only CLI audit refused canonical synthetic evidence: %v", err)
	}
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	t.Setenv("QUAL_GITHUB_REPO_ID", "1443998164")
	t.Setenv("QUAL_GITHUB_REPOSITORY", "orz-i/Qual")
	t.Setenv("QUAL_GITHUB_SPOOL_DIR", restored.Root)
	os.Args = []string{"adapter", "reconcile-audit", "--keys", keysPath, "--evidence", evidencePath}
	if err := run(); err != nil {
		t.Fatalf("actual read-only CLI command failed: %v", err)
	}
	recovery, err := restored.RecoveryStatus()
	if err != nil || !recovery.Quarantined {
		t.Fatal("audit changed the original restore quarantine")
	}

	// T1: independent source ledger has a new accepted fact absent from T0
	// signed snapshot. Sign all 3 statements correctly to ensure the mismatch
	// is caught by comparison rather than by signature failure.
	source.Facts = append(source.Facts, githubadapter.SourceLedgerFact{
		Sequence: 2, DeliveryID: "66666666-6666-4666-8666-666666666666",
		PayloadSHA256: strings.Repeat("b", 64), EventType: "pull_request.edited",
		ExternalThreadID: "orz-i/Qual/pull/1",
	})
	source.LastSequence = 2
	qual.Facts = append(qual.Facts, githubadapter.QualLedgerFact{
		DeliveryID:    "66666666-6666-4666-8666-666666666666",
		PayloadSHA256: strings.Repeat("b", 64),
		QualEventID:   "77777777-7777-4777-8777-777777777777",
		ProjectID:     projectID, WorkItemID: workID, ThreadID: threadID,
	})
	auditJSON(t, evidencePath, signBundle())
	missing, err := auditRestoredSpool(restored, repo, keysPath, evidencePath, now)
	if err != nil || missing.RecordsMatch || missing.MissingFromRestored != 1 ||
		missing.ActivationAuthorized {
		t.Fatalf("old signed T0 snapshot was falsely certified complete: %+v %v", missing, err)
	}
	if err := runReconciliationAudit(repo, restored, keysPath, evidencePath); err == nil {
		t.Fatal("CLI succeeded despite missing post-snapshot signed source fact")
	}
	if err := run(); err == nil {
		t.Fatal("actual CLI command succeeded on a restored T0 missing signed T1 fact")
	}
	if status, err := restored.RecoveryStatus(); err != nil || !status.Quarantined {
		t.Fatal("mismatched source evidence unlocked old snapshot")
	}

	// Even a syntactically correct signature cannot legitimize a modified or
	// substituted pinned key input.
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-5] ^= 1
	if err := os.WriteFile(evidencePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := auditRestoredSpool(restored, repo, keysPath, evidencePath, now); err == nil {
		t.Fatal("tampered signed input passed audit")
	}
	if err := os.Chmod(keysPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := auditRestoredSpool(restored, repo, keysPath, evidencePath, now); err == nil {
		t.Fatal("unsafe key pin file permissions accepted")
	}
}

func TestReconciliationInputRefusesSymlinkOverlongUnknownAndMissingFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "owner-only.json")
	if err := os.WriteFile(file, []byte(`{"source":"wrong","unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	var k auditKeys
	raw, err := readOwnerOnlyAuditInput(file, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeExactAuditJSON(raw, &k); !errors.Is(err, githubadapter.ErrReconciliation) {
		t.Fatalf("unknown pinned-key field was accepted: %v", err)
	}
	symlink := filepath.Join(dir, "forged-key-symlink.json")
	if err := os.Symlink(file, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerOnlyAuditInput(symlink, 2048); err == nil {
		t.Fatal("symlinked source evidence accepted")
	}
	if _, err := readOwnerOnlyAuditInput(file, 10); err == nil {
		t.Fatal("oversize signed evidence accepted")
	}
	if _, err := readOwnerOnlyAuditInput(filepath.Join(dir, "missing.json"), 2048); err == nil {
		t.Fatal("missing public key pin accepted")
	}
	if _, err := readOwnerOnlyAuditInput("relative.json", 2048); err == nil {
		t.Fatal("relative trust path accepted")
	}
}
