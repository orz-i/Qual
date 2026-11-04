//go:build !darwin && !linux

package githubadapter

// Unsupported platforms must not fall back to an overwriting os.Rename.
func renameVerifiedDirectoryNoReplace(_, _ string) error { return ErrSpool }
