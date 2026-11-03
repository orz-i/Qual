package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func TestArtifactInputValidationAndIntegrity(t *testing.T) {
	data := []byte("recorded, never executed")
	sum := sha256.Sum256(data)
	in := BeginInput{TargetKind: WorkItem, TargetID: uuid.New(), Filename: "report.pdf",
		MediaType: "application/pdf", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Verify(data, in.SizeBytes, in.SHA256); err != nil {
		t.Fatal(err)
	}
	if err := Verify([]byte("tampered"), in.SizeBytes, in.SHA256); !errors.Is(err, ErrIntegrity) {
		t.Fatal("mismatch allowed", err)
	}
	for _, filename := range []string{"../secrets.txt", "foo/bar.xml", "..", "a\nname.txt", " "} {
		in.Filename = filename
		if err := in.Validate(); !errors.Is(err, ErrValidation) {
			t.Errorf("unsafe name accepted: %q", filename)
		}
	}
}
func TestExternalReferenceRequiresHTTPS(t *testing.T) {
	in := ReferenceInput{TargetKind: TestRun, TargetID: uuid.New(), Provider: "github", Kind: "workflow",
		ExternalID: "123", URL: "https://github.com/example/ci/actions/runs/123"}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	in.URL = "http://169.254.169.254/"
	if err := in.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatal("plaintext URL accepted")
	}
}
