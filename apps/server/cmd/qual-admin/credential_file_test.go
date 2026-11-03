package main

import (
	"os"
	"path/filepath"
	identity "qual/internal/identity/application"
	"strings"
	"testing"
)

func TestPrivateAgentTokenDigestRejectsInsecurePaths(t *testing.T) {
	token, expected, err := identity.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "agent.token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		got, err := privateAgentTokenDigest(path)
		if err != nil || got != expected {
			t.Fatalf("valid private credential refused: %v", err)
		}
	}
	check()
	os.Chmod(path, 0o644)
	if _, err := privateAgentTokenDigest(path); err == nil || strings.Contains(err.Error(), token) {
		t.Fatal("world-readable file accepted or credential value disclosed")
	}
	os.Chmod(path, 0o600)
	os.Chmod(parent, 0o755)
	if _, err := privateAgentTokenDigest(path); err == nil {
		t.Fatal("public parent directory accepted")
	}
	os.Chmod(parent, 0o700)
	check()
	link := filepath.Join(parent, "link.token")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := privateAgentTokenDigest(link); err == nil {
		t.Fatal("symlink token file accepted")
	}
	alias := filepath.Join(filepath.Dir(parent), "private-link")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := privateAgentTokenDigest(filepath.Join(alias, "agent.token")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	os.WriteFile(path, []byte("invalid"), 0o600)
	if _, err := privateAgentTokenDigest(path); err == nil {
		t.Fatal("invalid PAT format accepted")
	}
	if _, err := privateAgentTokenDigest("relative/agent.token"); err == nil {
		t.Fatal("relative path accepted")
	}
}
