package githubadapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The recovered spool must not replace a destination reserved by another
// legitimate Operator in the check/use window. An empty target is sufficient
// for the underlying syscall to replace it unless RENAME_EXCL/NOREPLACE is used.
func TestRestoreAtomicNoReplaceRefusesLateEmptyDestination(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "staged")
	target := filepath.Join(root, "restored")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, "archived"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	// Simulate another Operator reserving the target after preliminary checks.
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := renameVerifiedDirectoryNoReplace(stage, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("lost reservation or unexpected no-replace error: %v", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("preexisting reserved directory overwritten: %v entries=%v", err, entries)
	}
	if _, err := os.Lstat(filepath.Join(stage, "archived")); err != nil {
		t.Fatal("verified stage unexpectedly removed", err)
	}
}

func TestRestoreAtomicNoReplacePublishesExactlyOnce(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "verified")
	target := filepath.Join(root, "new-isolated-spool")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, "archived"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := renameVerifiedDirectoryNoReplace(stage, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, "archived")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged directory still present after verified publication: %v", err)
	}
	newStage := filepath.Join(root, "other-staged")
	if err := os.Mkdir(newStage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := renameVerifiedDirectoryNoReplace(newStage, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second restore overwrote already verified target: %v", err)
	}
}

func TestRestoreAtomicNoReplaceRefusesSymlinkAndFileTargets(t *testing.T) {
	for _, mode := range []string{"symlink", "file"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			stage := filepath.Join(root, "staged")
			target := filepath.Join(root, "target")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "symlink":
				if err := os.Symlink(stage, target); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := renameVerifiedDirectoryNoReplace(stage, target); !errors.Is(err, os.ErrExist) {
				t.Fatalf("%s destination unexpectedly overwritten: %v", mode, err)
			}
			if _, err := os.Lstat(stage); err != nil {
				t.Fatalf("source removed after refusing %s: %v", mode, err)
			}
		})
	}
}
