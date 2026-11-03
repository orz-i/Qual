package application

import (
	"bytes"
	"testing"
)

func TestIssuedTokens(t *testing.T) {
	first, digest, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	second, digest2, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || bytes.Equal(digest[:], digest2[:]) {
		t.Fatal("tokens must be random and unique")
	}
	got, err := HashToken(first)
	if err != nil || !bytes.Equal(digest[:], got[:]) {
		t.Fatal("token hash must match stored hash")
	}
	for _, input := range []string{"", "secret", "qual_pat_001", first + "x"} {
		if _, err := HashToken(input); err == nil {
			t.Fatalf("invalid token %q was accepted", input)
		}
	}
}
