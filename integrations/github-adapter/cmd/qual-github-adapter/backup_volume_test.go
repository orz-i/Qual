package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

func TestBackupSeparateVolumePreflightFailsClosedWithoutWriting(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	t.Setenv("QUAL_GITHUB_REPO_ID", "1443998164")
	t.Setenv("QUAL_GITHUB_REPOSITORY", "orz-i/Qual")
	t.Setenv("QUAL_GITHUB_SPOOL_DIR", source)
	t.Setenv("QUAL_GITHUB_BACKUP_HMAC_KEY", strings.Repeat("synthetic-unit-backup-key-", 3))
	if _, err := githubadapter.InitNewSpool(source); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, "backups")
	if err := os.Mkdir(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(backupDir, "signed-backup.zip")
	separate, err := backupVolumeSeparation(source, out)
	if err != nil || separate {
		t.Fatalf("two paths on same actual filesystem cannot be called independent: separate=%v err=%v", separate, err)
	}
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{"adapter", "backup", "--out", out, "--approve", "--require-separate-volume"}
	if err := run(); !errors.Is(err, errSeparateBackupVolumeRequired) {
		t.Fatalf("backup required a separate filesystem yet was accepted: %v", err)
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatalf("failed preflight created archive: %v", err)
	}
	if _, err := os.Lstat(out + ".sig"); !os.IsNotExist(err) {
		t.Fatalf("failed preflight created detached signature: %v", err)
	}
	// A same-volume backup remains permitted as an explicitly labelled local
	// protection copy; it must never claim off-host or encrypted assurance.
	os.Args = []string{"adapter", "backup", "--out", out, "--approve"}
	if err := run(); err != nil {
		t.Fatalf("existing local signed backup broken: %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestBackupVolumeDeviceIdentityRequiresOwnerAndNoSymlinks(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "spool")
	target := filepath.Join(dir, "backup")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	independent, err := backupVolumeSeparation(source, filepath.Join(target, "a.zip"))
	if err != nil || independent {
		t.Fatalf("same filesystem reported separate: %v %v", independent, err)
	}
	if !distinctDevices(11, 12) || distinctDevices(11, 11) {
		t.Fatal("device identity comparison regressed")
	}
	link := filepath.Join(dir, "linked-target")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := backupVolumeSeparation(source, filepath.Join(link, "a.zip")); err == nil {
		t.Fatal("symlinked destination parent accepted")
	}
	if err := os.Chmod(target, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := backupVolumeSeparation(source, filepath.Join(target, "a.zip")); err == nil {
		t.Fatal("group-writable destination accepted as protected backup location")
	}
	if _, err := backupVolumeSeparation(source, "relative.zip"); err == nil {
		t.Fatal("relative backup destination accepted")
	}
}
