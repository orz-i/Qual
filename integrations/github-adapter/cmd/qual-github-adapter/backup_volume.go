package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/orz-i/Qual/integrations/github-adapter/githubadapter"
)

// backupVolumeSeparation is a conservative OS device-identity check, NOT
// an attestation of a remote host, encrypted storage, or durable replication.
// The underlying HMAC snapshot command still enforces its separate safe-parent
// and atomic no-replace destination constraints.
func backupVolumeSeparation(spoolRoot, outZip string) (bool, error) {
	if !filepath.IsAbs(spoolRoot) || !filepath.IsAbs(outZip) ||
		filepath.Clean(spoolRoot) != spoolRoot || filepath.Clean(outZip) != outZip {
		return false, githubadapter.ErrInvalid
	}
	source, err := ownerDirectoryDevice(spoolRoot)
	if err != nil {
		return false, err
	}
	destination, err := ownerDirectoryDevice(filepath.Dir(outZip))
	if err != nil {
		return false, err
	}
	return distinctDevices(source, destination), nil
}

func ownerDirectoryDevice(path string) (uint64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0022 != 0 {
		return 0, githubadapter.ErrSpool
	}
	meta, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(meta.Uid) != os.Getuid() {
		return 0, githubadapter.ErrSpool
	}
	return uint64(meta.Dev), nil
}

// Empty-source test helper is deliberately pure; filesystem test cases
// exercise both actual device IDs and unsafe path rejection.
func distinctDevices(a, b uint64) bool { return a != b }

var errSeparateBackupVolumeRequired = errors.New("signed backup requires a distinct filesystem volume; no snapshot created")
