package githubadapter

import (
	"archive/zip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const snapshotSchema = "qual-github-adapter-spool-snapshot-v1"
const maxBackupRecords = 100000
const maxBackupRecordBytes int64 = 512 * 1024 * 1024
const maxBackupZipBytes int64 = 600 * 1024 * 1024
const maxBackupManifestBytes int64 = 16 * 1024 * 1024

var snapshotDirectories = []string{"pending", "delivered", "archived", "archive-index"}

type snapshotRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type snapshotManifest struct {
	Schema  string           `json:"schema"`
	Records []snapshotRecord `json:"records"`
}

// SignedBackupResult deliberately omits the HMAC and all record contents.
type SignedBackupResult struct {
	RecordCount   int    `json:"record_count"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

func validBackupKey(key []byte) bool {
	if len(key) < 32 || len(key) > 256 {
		return false
	}
	for _, b := range key {
		if b == '\r' || b == '\n' || b == 0 {
			return false
		}
	}
	return true
}

func validatedBackupOutput(path, spoolRoot string) error {
	if !filepath.IsAbs(path) || !strings.HasSuffix(path, ".zip") ||
		filepath.Clean(path) != path {
		return ErrInvalid
	}
	if err := safeParent(filepath.Dir(path)); err != nil {
		return err
	}
	if spoolRoot != "" {
		relative, err := filepath.Rel(spoolRoot, path)
		if err != nil || relative == "." || relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrInvalid
		}
	}
	for _, name := range []string{path, path + ".sig"} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return ErrConflict
		}
	}
	return nil
}

func validSnapshotEntry(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[1] == "" || !strings.HasSuffix(parts[1], ".json") {
		return false
	}
	stem := strings.TrimSuffix(parts[1], ".json")
	switch parts[0] {
	case "pending", "delivered", "archived":
		return deliveryIDRE.MatchString(stem)
	case "archive-index":
		return archiveDigestRE.MatchString(stem)
	default:
		return false
	}
}

func snapshotZipEntry(z *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0600)
	w, err := z.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func writeAndSyncExclusive(path string, contents []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(contents); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CreateSignedBackup is an EXTERNAL, explicit trusted-Operator operation.
// It blocks concurrent ingress/worker writes while streaming a bounded
// owner-only ZIP of all sanitized spool facts; raw GitHub payload, JWTs and
// Qual credentials are never in these records. Detached HMAC guards the
// complete archive, using an independently injected Operator-only key.
// This is integrity, NOT encryption.
func (s *Spool) CreateSignedBackup(outZip string, key []byte) (SignedBackupResult, error) {
	var result SignedBackupResult
	if !validBackupKey(key) || validatedBackupOutput(outZip, s.Root) != nil {
		return result, ErrInvalid
	}
	parent := filepath.Dir(outZip)
	err := s.withLock("worker", func() error {
		return s.withLock("queue", func() error {
			if err := s.reconcileLocked(); err != nil {
				return err
			}
			if err := s.reconcileArchiveLocked(); err != nil {
				return err
			}
			var names []string
			for _, sub := range snapshotDirectories {
				entries, err := os.ReadDir(filepath.Join(s.Root, sub))
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".pending-") {
						continue // an incomplete atomicWrite, not a committed fact
					}
					name := sub + "/" + entry.Name()
					if entry.IsDir() || !validSnapshotEntry(name) {
						return ErrSpool
					}
					names = append(names, name)
				}
			}
			if len(names) > maxBackupRecords {
				return ErrFull
			}
			sort.Strings(names)
			zipTemp, err := os.CreateTemp(parent, ".qual-snapshot-*.zip")
			if err != nil {
				return err
			}
			zipTmpPath := zipTemp.Name()
			defer os.Remove(zipTmpPath)
			if err := zipTemp.Chmod(0600); err != nil {
				zipTemp.Close()
				return err
			}
			mac := hmac.New(sha256.New, key)
			digest := sha256.New()
			z := zip.NewWriter(io.MultiWriter(zipTemp, mac, digest))
			manifest := snapshotManifest{Schema: snapshotSchema, Records: make([]snapshotRecord, 0, len(names))}
			var total int64
			for _, name := range names {
				data, err := trustedFile(filepath.Join(s.Root, filepath.FromSlash(name)))
				if err != nil || len(data) == 0 || int64(len(data)) > MaxRecordBytes {
					z.Close()
					zipTemp.Close()
					return ErrSpool
				}
				total += int64(len(data))
				if total > maxBackupRecordBytes {
					z.Close()
					zipTemp.Close()
					return ErrFull
				}
				sum := sha256.Sum256(data)
				if err := snapshotZipEntry(z, name, data); err != nil {
					z.Close()
					zipTemp.Close()
					return err
				}
				manifest.Records = append(manifest.Records, snapshotRecord{
					Path: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)),
				})
			}
			rawManifest, err := json.Marshal(manifest)
			if err != nil || int64(len(rawManifest)) > maxBackupManifestBytes {
				z.Close()
				zipTemp.Close()
				return ErrFull
			}
			if err := snapshotZipEntry(z, "_manifest.json", rawManifest); err != nil {
				z.Close()
				zipTemp.Close()
				return err
			}
			if err := z.Close(); err != nil {
				zipTemp.Close()
				return err
			}
			if err := zipTemp.Sync(); err != nil {
				zipTemp.Close()
				return err
			}
			info, err := zipTemp.Stat()
			if err != nil || info.Size() > maxBackupZipBytes {
				zipTemp.Close()
				return ErrFull
			}
			if err := zipTemp.Close(); err != nil {
				return err
			}
			signature, err := os.CreateTemp(parent, ".qual-signature-*")
			if err != nil {
				return err
			}
			sigTempPath := signature.Name()
			defer os.Remove(sigTempPath)
			if err := signature.Chmod(0600); err != nil {
				signature.Close()
				return err
			}
			if _, err := signature.Write([]byte(hex.EncodeToString(mac.Sum(nil)) + "\n")); err != nil {
				signature.Close()
				return err
			}
			if err := signature.Sync(); err != nil {
				signature.Close()
				return err
			}
			if err := signature.Close(); err != nil {
				return err
			}
			if err := validatedBackupOutput(outZip, s.Root); err != nil {
				return err
			}
			// The detached signature is published first. Either partial
			// publication makes restore reject; it never means success.
			if err := os.Rename(sigTempPath, outZip+".sig"); err != nil {
				return err
			}
			if err := os.Rename(zipTmpPath, outZip); err != nil {
				return err
			}
			if err := syncDirectory(parent); err != nil {
				return err
			}
			result = SignedBackupResult{
				RecordCount:   len(names),
				ArchiveSHA256: hex.EncodeToString(digest.Sum(nil)),
			}
			return nil
		})
	})
	return result, err
}

func secureBackupFile(path string, maxBytes int64) (*os.File, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() == 0 ||
		info.Size() > maxBytes {
		return nil, 0, ErrSpool
	}
	if owner, ok := info.Sys().(*syscall.Stat_t); !ok || int(owner.Uid) != os.Getuid() {
		return nil, 0, ErrSpool
	}
	// Prevent a symlink swap between Lstat and Open.
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, ErrSpool
	}
	file := os.NewFile(uintptr(fd), path)
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 ||
		opened.Size() != info.Size() || opened.Size() > maxBytes {
		file.Close()
		return nil, 0, ErrSpool
	}
	st, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		file.Close()
		return nil, 0, ErrSpool
	}
	return file, opened.Size(), nil
}

// verifyBackupZip authenticates EVERY archive byte before any target
// directory is created. A wrong/missing/public signature fails closed.
func verifyBackupZip(archive string, key []byte) (*os.File, int64, string, error) {
	if !validBackupKey(key) || !filepath.IsAbs(archive) {
		return nil, 0, "", ErrInvalid
	}
	if err := safeParent(filepath.Dir(archive)); err != nil {
		return nil, 0, "", err
	}
	sig, err := trustedFile(archive + ".sig")
	if err != nil {
		return nil, 0, "", err
	}
	if len(sig) != sha256.Size*2+1 || sig[len(sig)-1] != '\n' {
		return nil, 0, "", ErrSpool
	}
	expected, err := hex.DecodeString(string(sig[:len(sig)-1]))
	if err != nil || len(expected) != sha256.Size {
		return nil, 0, "", ErrSpool
	}
	file, size, err := secureBackupFile(archive, maxBackupZipBytes)
	if err != nil {
		return nil, 0, "", err
	}
	mac := hmac.New(sha256.New, key)
	checksum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(mac, checksum), file); err != nil {
		file.Close()
		return nil, 0, "", err
	}
	if !hmac.Equal(mac.Sum(nil), expected) {
		file.Close()
		return nil, 0, "", ErrSpool
	}
	return file, size, hex.EncodeToString(checksum.Sum(nil)), nil
}
