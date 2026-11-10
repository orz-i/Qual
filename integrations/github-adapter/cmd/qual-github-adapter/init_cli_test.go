package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRequiresExplicitNewSourceInitialization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-yet-acknowledged")
	t.Setenv("QUAL_GITHUB_REPO_ID", "1443998164")
	t.Setenv("QUAL_GITHUB_REPOSITORY", "orz-i/Qual")
	t.Setenv("QUAL_GITHUB_SPOOL_DIR", root)
	t.Setenv("QUAL_GITHUB_WEBHOOK_SECRET", strings.Repeat("s", 40))
	old := os.Args
	t.Cleanup(func() { os.Args = old })

	os.Args = []string{"adapter", "inspect"}
	if err := run(); err == nil {
		t.Fatal("inspect accidentally initialized an empty new source")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("inspect created a new empty identity: %v", err)
	}
	os.Args = []string{"adapter", "init"}
	if err := run(); err == nil {
		t.Fatal("implicit init accepted")
	}
	os.Args = []string{"adapter", "init", "--approve"}
	if err := run(); err != nil {
		t.Fatalf("explicit new-source initialization failed: %v", err)
	}
	os.Args = []string{"adapter", "init", "--approve"}
	if err := run(); err == nil {
		t.Fatal("second init accepted existing source; replay risk")
	}
	os.Args = []string{"adapter", "inspect"}
	if err := run(); err != nil {
		t.Fatalf("inspect could not read explicitly initialized source: %v", err)
	}
}
