//go:build linux

package githubadapter

import "golang.org/x/sys/unix"

// Linux renameat2(RENAME_NOREPLACE) atomically refuses all existing targets.
// If the underlying filesystem does not support the flag, this fails closed.
func renameVerifiedDirectoryNoReplace(staged, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, staged,
		unix.AT_FDCWD, destination, uint(unix.RENAME_NOREPLACE))
}
