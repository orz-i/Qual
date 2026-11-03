package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	identity "qual/internal/identity/application"
	"strings"
	"syscall"
)

// The current PAT is read only to match its digest against a real DB row.
// Never print, persist or return the bearer value. Shared macOS UID is NOT
// an operating-system security boundary.
func privateAgentTokenDigest(name string) ([32]byte, error) {
	var empty [32]byte
	if name == "" || !filepath.IsAbs(name) {
		return empty, errors.New("absolute -token-file path required")
	}
	directory, err := os.Lstat(filepath.Dir(filepath.Clean(name)))
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return empty, errors.New("credential parent must be a real private directory")
	}
	if directory.Mode().Perm() != 0o700 || !ownedByCurrentUser(directory) {
		return empty, errors.New("credential parent requires owner-only mode 0700")
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return empty, errors.New("could not open private non-symlink credential file")
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Mode().Perm() != 0o600 {
		return empty, errors.New("credential file requires current owner, regular file and mode 0600")
	}
	if info.Size() < 1 || info.Size() > 128 {
		return empty, errors.New("invalid private credential file length")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil || len(raw) > 128 {
		return empty, errors.New("could not safely read credential file")
	}
	digest, err := identity.HashToken(strings.TrimSpace(string(raw)))
	if err != nil {
		return empty, errors.New("invalid Qual PAT format in private credential file")
	}
	return digest, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
