package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSignedManifestTamperAndPrivateFiles(t *testing.T) {
	t.Setenv("QUAL_BACKUP_HMAC_KEY", strings.Repeat("ab", 32))
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := privateFile(filepath.Join(dir, "database.dump"), []byte("dummy-postgres-dump")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("external evidence")
	if err := privateFile(filepath.Join(dir, "objects", "000000.bin"), payload); err != nil {
		t.Fatal(err)
	}
	key := "projects/" + uuid.NewString() + "/artifacts/" + uuid.NewString()
	m := Manifest{
		Version: SchemaVersion, SourceDatabase: "qual_unit_test", SourceBucket: "qual-artifacts",
		SnapshotAt: time.Now(), CreatedAt: time.Now(), DatabaseDump: "database.dump",
		DatabaseSHA256: digest([]byte("dummy-postgres-dump")), DatabaseSize: 19,
		Tables: []Table{{Schema: "qual", Name: "projects", Rows: 1, Fingerprint: digest([]byte("row"))}},
		Objects: []Object{{Key: key, File: "objects/000000.bin", Kind: "artifact",
			ContentType: "text/plain", SHA256: digest(payload), Size: int64(len(payload))}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	signature := sign(mustKey(t), raw)
	if err := privateFile(filepath.Join(dir, "manifest.json"), raw); err != nil {
		t.Fatal(err)
	}
	if err := privateFile(filepath.Join(dir, "manifest.hmac"), []byte(signature)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("signed archive not verified: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("sensitive manifest not private")
	}
	if err := os.WriteFile(filepath.Join(dir, "objects", "000000.bin"), []byte("tampered data!"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("modified evidence accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "objects", "000000.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	// Never follow a symlink even when it resolves to identical valid bytes.
	objectPath := filepath.Join(dir, "objects", "000000.bin")
	regularCopy := filepath.Join(dir, "copy.bin")
	if err := os.WriteFile(regularCopy, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regularCopy, objectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("symlinked object accepted")
	}
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objectPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("world-readable backup directory accepted")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("restored private backup rejected: %v", err)
	}
	t.Setenv("QUAL_BACKUP_HMAC_KEY", strings.Repeat("cd", 32))
	if _, err := Verify(dir); err == nil {
		t.Fatal("wrong HMAC key accepted")
	}
}
func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func TestManifestRejectsTraversalAndUnsafeDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://qual:pwd@remote.example.com/qual",
		"postgres://qual:pwd@192.168.1.8/qual",
		"postgres://qual:pwd@127.0.0.1/postgres;DROP",
		"postgres://qual:pwd@127.0.0.1/",
	} {
		if _, err := restrictedDatabaseName(dsn); err == nil {
			t.Errorf("unsafe source database accepted: %s", dsn)
		}
	}
	for _, key := range []string{"../etc/passwd", "projects/x/artifacts/../foo",
		"projects/" + uuid.NewString() + "/exports/../data",
		"projects/" + uuid.NewString() + "/artifacts/../../data",
	} {
		if validObjectKey(key) {
			t.Errorf("malicious S3 key accepted %s", key)
		}
	}
}
