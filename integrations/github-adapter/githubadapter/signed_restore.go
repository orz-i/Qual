package githubadapter

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type restorableFile struct {
	file   *zip.File
	record snapshotRecord
}

func readBoundedZipEntry(file *zip.File, max int64) ([]byte, error) {
	if file.Method != zip.Store || file.UncompressedSize64 == 0 ||
		file.UncompressedSize64 > uint64(max) ||
		file.CompressedSize64 != file.UncompressedSize64 ||
		!file.FileInfo().Mode().IsRegular() ||
		file.FileInfo().Mode().Perm() != 0600 {
		return nil, ErrSpool
	}
	f, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || uint64(len(data)) != file.UncompressedSize64 {
		return nil, ErrSpool
	}
	return data, nil
}

// validateSignedZipStructure prevents archive traversal, duplicated paths,
// malformed manifests and zip bombs even when an authorized signing key was
// deliberately used to sign a malicious archive.
func validateSignedZipStructure(reader *zip.Reader) ([]restorableFile, error) {
	if len(reader.File) < 1 || len(reader.File) > maxBackupRecords+1 {
		return nil, ErrSpool
	}
	files := map[string]*zip.File{}
	for _, file := range reader.File {
		if file.Name != "_manifest.json" && !validSnapshotEntry(file.Name) {
			return nil, ErrSpool
		}
		if files[file.Name] != nil {
			return nil, ErrSpool
		}
		files[file.Name] = file
	}
	mf := files["_manifest.json"]
	if mf == nil {
		return nil, ErrSpool
	}
	raw, err := readBoundedZipEntry(mf, maxBackupManifestBytes)
	if err != nil {
		return nil, err
	}
	var manifest snapshotManifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Schema != snapshotSchema ||
		len(manifest.Records) != len(reader.File)-1 {
		return nil, ErrSpool
	}
	var result []restorableFile
	var total int64
	for i, item := range manifest.Records {
		if !validSnapshotEntry(item.Path) || item.Bytes < 1 ||
			item.Bytes > MaxRecordBytes || !archiveDigestRE.MatchString(item.SHA256) {
			return nil, ErrSpool
		}
		if i > 0 && manifest.Records[i-1].Path >= item.Path {
			return nil, ErrSpool
		}
		file := files[item.Path]
		if file == nil || file.UncompressedSize64 != uint64(item.Bytes) {
			return nil, ErrSpool
		}
		// Reject suspect entry metadata before creating the destination.
		if file.Method != zip.Store || file.CompressedSize64 != file.UncompressedSize64 ||
			!file.FileInfo().Mode().IsRegular() || file.FileInfo().Mode().Perm() != 0600 {
			return nil, ErrSpool
		}
		total += item.Bytes
		if total > maxBackupRecordBytes {
			return nil, ErrFull
		}
		result = append(result, restorableFile{file: file, record: item})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].record.Path < result[j].record.Path })
	return result, nil
}

// RestoreSignedBackup only restores into a NEW owner-only location.
// The detached HMAC is verified before ANY destination path is created.
// Caller must use a high-entropy independent Operator key, never the PI PAT
// or the Qual database/backup signing key.
func RestoreSignedBackup(archive, destination string, key []byte) (*Spool, SignedBackupResult, error) {
	var zero SignedBackupResult
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination ||
		destination == filepath.Dir(destination) {
		return nil, zero, ErrInvalid
	}
	parent := filepath.Dir(destination)
	if err := safeParent(parent); err != nil {
		return nil, zero, err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return nil, zero, ErrConflict
	}
	source, size, sum, err := verifyBackupZip(archive, key)
	if err != nil {
		return nil, zero, err
	}
	defer source.Close()
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return nil, zero, ErrSpool
	}
	files, err := validateSignedZipStructure(reader)
	if err != nil {
		return nil, zero, err
	}
	// A trusted snapshot can still be larger than the destination's free
	// space. Protect a recovery reserve before any filesystem mutation.
	disk, err := actualFilesystemStats(parent)
	if err != nil || disk.TotalBytes == 0 ||
		disk.AvailableBytes < uint64(size)+diskReserveBytes {
		return nil, zero, ErrFull
	}
	stage, err := os.MkdirTemp(parent, ".qual-restore-*")
	if err != nil {
		return nil, zero, err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0700); err != nil {
		return nil, zero, err
	}
	for _, sub := range snapshotDirectories {
		if err := safeDir(filepath.Join(stage, sub)); err != nil {
			return nil, zero, err
		}
	}
	for _, entry := range files {
		data, err := readBoundedZipEntry(entry.file, MaxRecordBytes)
		if err != nil {
			return nil, zero, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != entry.record.SHA256 {
			return nil, zero, ErrSpool
		}
		target := filepath.Join(stage, filepath.FromSlash(entry.record.Path))
		if !strings.HasPrefix(target, stage+string(filepath.Separator)) {
			return nil, zero, ErrSpool
		}
		if err := writeAndSyncExclusive(target, data); err != nil {
			return nil, zero, err
		}
	}
	for _, sub := range snapshotDirectories {
		if err := syncDirectory(filepath.Join(stage, sub)); err != nil {
			return nil, zero, err
		}
	}
	if err := syncDirectory(stage); err != nil {
		return nil, zero, err
	}
	// The normal spool startup audits all archive indexes, immutable receipts
	// and Pending/Delivered crash windows before any isolated rename.
	if _, err := OpenSpool(stage); err != nil {
		return nil, zero, err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return nil, zero, ErrConflict
	}
	if err := os.Rename(stage, destination); err != nil {
		return nil, zero, err
	}
	if err := syncDirectory(parent); err != nil {
		return nil, zero, err
	}
	reopened, err := OpenSpool(destination)
	if err != nil {
		return nil, zero, err
	}
	return reopened, SignedBackupResult{RecordCount: len(files), ArchiveSHA256: sum}, nil
}
