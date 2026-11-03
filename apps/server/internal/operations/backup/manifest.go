// Package backup creates operator-only, local, signed backups of the entire
// Qual PostgreSQL database and referenced private S3 bytes. It does not
// provide a restore-to-existing-database command.
package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	s3store "qual/internal/evidence/infrastructure/s3store"
)

const (
	SchemaVersion              = 1
	MaxDumpBytes         int64 = 256 << 20
	MaxTotalObjectBytes  int64 = 512 << 20
	MaxSingleObjectBytes int64 = 32 << 20
	MaxTableRows         int64 = 100000
	MaxObjects                 = 5000
)

var (
	ErrInvalid     = errors.New("invalid backup input or manifest")
	ErrIntegrity   = errors.New("backup signature or integrity verification failed")
	ErrLimit       = errors.New("pilot backup size or record limit exceeded")
	ErrUnavailable = errors.New("required local backup dependency unavailable")
)
var dbNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var fileNameRE = regexp.MustCompile(`^objects/[0-9]{6}\.bin$`)

type Table struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Rows        int64  `json:"rows"`
	Fingerprint string `json:"sha256"`
}
type Object struct {
	Key         string `json:"key"`
	File        string `json:"file"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size_bytes"`
}
type Manifest struct {
	Version        int       `json:"backup_schema_version"`
	SourceDatabase string    `json:"source_database"`
	SourceBucket   string    `json:"source_bucket"`
	SnapshotAt     time.Time `json:"snapshot_at"`
	CreatedAt      time.Time `json:"created_at"`
	DatabaseDump   string    `json:"database_dump"`
	DatabaseSHA256 string    `json:"database_sha256"`
	DatabaseSize   int64     `json:"database_size_bytes"`
	Tables         []Table   `json:"tables"`
	Objects        []Object  `json:"objects"`
}
type Service struct {
	Pool        *pgxpool.Pool
	Objects     *s3store.Store
	DatabaseURL string
	ComposeFile string
}

func restrictedDatabaseName(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil {
		return "", ErrInvalid
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", ErrInvalid
	}
	database := strings.TrimPrefix(u.Path, "/")
	if !dbNameRE.MatchString(database) {
		return "", ErrInvalid
	}
	return database, nil
}
func findComposeFile() (string, error) {
	if val := os.Getenv("QUAL_COMPOSE_FILE"); val != "" {
		info, err := os.Stat(val)
		if err != nil || info.IsDir() {
			return "", ErrInvalid
		}
		return filepath.Abs(val)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "deploy", "compose.dev.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return "", ErrUnavailable
}
func New(ctx context.Context, dsn string) (*Service, error) {
	if _, err := restrictedDatabaseName(dsn); err != nil {
		return nil, err
	}
	compose, err := findComposeFile()
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, ErrUnavailable
	}
	objects, err := s3store.NewFromEnv(ctx)
	if err != nil || objects == nil {
		pool.Close()
		return nil, ErrUnavailable
	}
	return &Service{Pool: pool, Objects: objects, DatabaseURL: dsn, ComposeFile: compose}, nil
}
func (s *Service) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}
func signingKey() ([]byte, error) {
	raw := os.Getenv("QUAL_BACKUP_HMAC_KEY")
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("QUAL_BACKUP_HMAC_KEY must be 32 random bytes in hex")
	}
	return key, nil
}
func sign(key, message []byte) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(message)
	return hex.EncodeToString(h.Sum(nil))
}
func verifySignature(key, raw []byte, expected string) error {
	value, err := hex.DecodeString(strings.TrimSpace(expected))
	if err != nil || len(value) != sha256.Size {
		return ErrIntegrity
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(raw)
	if subtle.ConstantTimeCompare(h.Sum(nil), value) != 1 {
		return ErrIntegrity
	}
	return nil
}
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func digestFile(path string, limit int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", 0, ErrInvalid
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.Size() > limit || !stat.Mode().IsRegular() {
		return "", 0, ErrLimit
	}
	h := sha256.New()
	bytesCopied, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil || bytesCopied > limit {
		return "", 0, ErrLimit
	}
	return hex.EncodeToString(h.Sum(nil)), bytesCopied, nil
}
func privateFile(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	return f.Sync()
}
func validObjectKey(value string) bool {
	// Only the prefixes created by Qual are backed up/restored.
	parts := strings.Split(value, "/")
	if len(parts) != 4 || parts[0] != "projects" || (parts[2] != "artifacts" && parts[2] != "exports") {
		return false
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return false
	}
	name := parts[3]
	if parts[2] == "exports" {
		name = strings.TrimSuffix(name, ".zip")
		if name == parts[3] {
			return false
		}
	}
	_, err := uuid.Parse(name)
	return err == nil
}
func validateManifest(m Manifest) error {
	if m.Version != SchemaVersion || !dbNameRE.MatchString(m.SourceDatabase) || m.SourceBucket == "" ||
		m.DatabaseDump != "database.dump" || len(m.DatabaseSHA256) != 64 || m.DatabaseSize <= 0 || m.DatabaseSize > MaxDumpBytes ||
		len(m.Objects) > MaxObjects || len(m.Tables) == 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	var total int64
	for _, item := range m.Objects {
		if !validObjectKey(item.Key) || !fileNameRE.MatchString(item.File) || item.Size < 1 || item.Size > MaxSingleObjectBytes ||
			len(item.SHA256) != 64 || (item.Kind != "artifact" && item.Kind != "export") || len(item.ContentType) < 3 || len(item.ContentType) > 120 {
			return ErrInvalid
		}
		if seen[item.File] || seen[item.Key] {
			return ErrInvalid
		}
		seen[item.File] = true
		seen[item.Key] = true
		if total > MaxTotalObjectBytes-item.Size {
			return ErrLimit
		}
		total += item.Size
	}
	for _, table := range m.Tables {
		if table.Schema != "qual" && table.Schema != "public" {
			return ErrInvalid
		}
		if !dbNameRE.MatchString(table.Name) || table.Rows < 0 || table.Rows > MaxTableRows || len(table.Fingerprint) != 64 {
			return ErrInvalid
		}
	}
	return nil
}

// Verify works without PostgreSQL/S3 and refuses a missing/wrong HMAC key.
// It validates every backed-up byte before a restore drill can start.
func readPrivateLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, ErrInvalid
	}
	return os.ReadFile(path)
}
func Verify(dir string) (Manifest, error) {
	var m Manifest
	folder, err := os.Lstat(dir)
	if err != nil || !folder.IsDir() || folder.Mode().Perm()&0077 != 0 {
		return m, ErrInvalid
	}
	key, err := signingKey()
	if err != nil {
		return m, err
	}
	raw, err := readPrivateLimited(filepath.Join(dir, "manifest.json"), 1<<20)
	if err != nil {
		return m, ErrIntegrity
	}
	if len(raw) > 1<<20 {
		return m, ErrLimit
	}
	signature, err := readPrivateLimited(filepath.Join(dir, "manifest.hmac"), 256)
	if err != nil {
		return m, ErrIntegrity
	}
	if err := verifySignature(key, raw, string(signature)); err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, ErrInvalid
	}
	if err := validateManifest(m); err != nil {
		return m, err
	}
	sum, size, err := digestFile(filepath.Join(dir, m.DatabaseDump), MaxDumpBytes)
	if err != nil || sum != m.DatabaseSHA256 || size != m.DatabaseSize {
		return m, ErrIntegrity
	}
	for _, item := range m.Objects {
		sum, size, err := digestFile(filepath.Join(dir, item.File), MaxSingleObjectBytes)
		if err != nil || size != item.Size || sum != item.SHA256 {
			return m, ErrIntegrity
		}
	}
	return m, nil
}
func (m Manifest) Summary() string {
	return fmt.Sprintf("PostgreSQL snapshot %s: %d tables, %d S3 objects, dump %d bytes",
		m.SourceDatabase, len(m.Tables), len(m.Objects), m.DatabaseSize)
}
