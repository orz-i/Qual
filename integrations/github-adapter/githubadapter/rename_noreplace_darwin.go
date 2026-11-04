//go:build darwin

package githubadapter

import "golang.org/x/sys/unix"

// Darwin's RENAME_EXCL enforces nonreplacement inside the kernel. Unlike
// os.Rename, there is no separate Lstat->rename check/use window.
func renameVerifiedDirectoryNoReplace(staged, destination string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, staged,
		unix.AT_FDCWD, destination, unix.RENAME_EXCL)
}
