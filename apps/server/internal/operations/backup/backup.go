package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

type queryClient interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func tableFingerprints(ctx context.Context, db queryClient) ([]Table, error) {
	rows, err := db.Query(ctx, `SELECT table_schema,table_name
 FROM information_schema.tables
 WHERE table_type='BASE TABLE' AND (table_schema='qual'
 OR (table_schema='public' AND table_name='goose_db_version'))
 ORDER BY table_schema,table_name`)
	if err != nil {
		return nil, err
	}
	type name struct{ schema, table string }
	names := []name{}
	for rows.Next() {
		var item name
		if err := rows.Scan(&item.schema, &item.table); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(names) < 8 {
		return nil, ErrInvalid
	}
	result := make([]Table, 0, len(names))
	for _, item := range names {
		// Identifiers come only from PostgreSQL catalog, then quoted as identifiers.
		if !dbNameRE.MatchString(item.table) {
			return nil, ErrInvalid
		}
		query := fmt.Sprintf(`SELECT to_jsonb(t)::text FROM %s AS t ORDER BY to_jsonb(t)::text`,
			(pgx.Identifier{item.schema, item.table}).Sanitize())
		records, err := db.Query(ctx, query)
		if err != nil {
			return nil, err
		}
		sum := sha256.New()
		var count int64
		for records.Next() {
			var value string
			if err := records.Scan(&value); err != nil {
				records.Close()
				return nil, err
			}
			count++
			if count > MaxTableRows {
				records.Close()
				return nil, ErrLimit
			}
			_, _ = io.WriteString(sum, value)
			_, _ = io.WriteString(sum, "\n")
		}
		err = records.Err()
		records.Close()
		if err != nil {
			return nil, err
		}
		result = append(result, Table{
			Schema: item.schema, Name: item.table, Rows: count,
			Fingerprint: hex.EncodeToString(sum.Sum(nil)),
		})
	}
	return result, nil
}

type objectSource struct {
	Key         string
	Size        int64
	Sum         string
	Kind        string
	ContentType string
}

func enumerateObjects(ctx context.Context, tx pgx.Tx) ([]objectSource, error) {
	rows, err := tx.Query(ctx, `
 SELECT object_key,size_bytes,encode(sha256,'hex'),'artifact',media_type
 FROM qual.artifacts WHERE state IN ('ready','uploaded')
 UNION ALL
 SELECT object_key,size_bytes,encode(archive_sha256,'hex'),'export','application/zip'
 FROM qual.archive_exports ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := make([]objectSource, 0)
	var sum int64
	for rows.Next() {
		var item objectSource
		if err := rows.Scan(&item.Key, &item.Size, &item.Sum, &item.Kind, &item.ContentType); err != nil {
			return nil, err
		}
		if len(found) >= MaxObjects || item.Size > MaxSingleObjectBytes || item.Size < 1 ||
			sum > MaxTotalObjectBytes-item.Size || !validObjectKey(item.Key) {
			return nil, ErrLimit
		}
		sum += item.Size
		found = append(found, item)
	}
	return found, rows.Err()
}

type boundedWriter struct {
	Destination io.Writer
	Remaining   int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		return 0, ErrLimit
	}
	n, err := w.Destination.Write(p)
	w.Remaining -= int64(n)
	return n, err
}
func runDocker(ctx context.Context, compose string, input io.Reader, output io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", compose, "exec", "-T", "postgres"}, args...)...)
	command.Stdin = input
	command.Stdout = output
	command.Stderr = io.Discard // Never leak DB metadata, credentials or raw SQL to logs.
	if err := command.Run(); err != nil {
		return ErrUnavailable
	}
	return nil
}
func writeDump(ctx context.Context, compose, dbname, snapshot, path string) (string, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", 0, err
	}
	writer := &boundedWriter{Destination: file, Remaining: MaxDumpBytes}
	err = runDocker(ctx, compose, nil, writer,
		"pg_dump", "-U", "qual", "-d", dbname, "-Fc", "--no-owner", "--no-acl", "--snapshot", snapshot)
	closeErr := file.Close()
	if err != nil {
		return "", 0, err
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	sum, size, err := digestFile(path, MaxDumpBytes)
	return sum, size, err
}

// Backup requires a maintenance window: no concurrent API/Worker/S3 mutation.
// It stores a signed manifest, PostgreSQL logical snapshot and S3 objects to
// a *new* private directory; on error its incomplete output is removed.
func (s *Service) Backup(ctx context.Context, destination string) (result Manifest, err error) {
	key, err := signingKey()
	if err != nil {
		return result, err
	}
	if s == nil || s.Pool == nil || s.Objects == nil {
		return result, ErrUnavailable
	}
	dbName, err := restrictedDatabaseName(s.DatabaseURL)
	if err != nil {
		return result, err
	}
	dest, err := filepath.Abs(destination)
	if err != nil {
		return result, err
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		return result, errors.New("backup destination must be a new private directory")
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err := os.Mkdir(filepath.Join(dest, "objects"), 0700); err != nil {
		return result, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshotID string
	var snapshotAt time.Time
	if err := tx.QueryRow(ctx, `SELECT pg_export_snapshot(),transaction_timestamp()`).Scan(&snapshotID, &snapshotAt); err != nil {
		return result, err
	}
	// Enumerations and pg_dump use precisely the same PostgreSQL snapshot ID.
	tables, err := tableFingerprints(ctx, tx)
	if err != nil {
		return result, err
	}
	objects, err := enumerateObjects(ctx, tx)
	if err != nil {
		return result, err
	}
	dumpFile := filepath.Join(dest, "database.dump")
	sum, size, err := writeDump(ctx, s.ComposeFile, dbName, snapshotID, dumpFile)
	if err != nil {
		return result, err
	}
	result = Manifest{
		Version: SchemaVersion, SourceDatabase: dbName, SourceBucket: s.Objects.Bucket,
		CreatedAt: time.Now().UTC(), SnapshotAt: snapshotAt, DatabaseDump: "database.dump",
		DatabaseSHA256: sum, DatabaseSize: size, Tables: tables,
		Objects: make([]Object, 0, len(objects)),
	}
	// Even if S3 changes outside PostgreSQL, mismatched bytes cause failure.
	for i, obj := range objects {
		body, err := s.Objects.Get(ctx, obj.Key, MaxSingleObjectBytes)
		if err != nil {
			return result, ErrUnavailable
		}
		if int64(len(body)) != obj.Size || digest(body) != obj.Sum {
			return result, ErrIntegrity
		}
		file := fmt.Sprintf("objects/%06d.bin", i)
		if err := privateFile(filepath.Join(dest, file), body); err != nil {
			return result, err
		}
		result.Objects = append(result.Objects, Object{Key: obj.Key, File: file, Kind: obj.Kind,
			SHA256: obj.Sum, Size: obj.Size, ContentType: obj.ContentType})
	}
	if err := validateManifest(result); err != nil {
		return result, err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return result, err
	}
	if err := privateFile(filepath.Join(dest, "manifest.json"), append(raw, '\n')); err != nil {
		return result, err
	}
	if err := privateFile(filepath.Join(dest, "manifest.hmac"), []byte(sign(key, append(raw, '\n'))+"\n")); err != nil {
		return result, err
	}
	// Release the exported snapshot only after dump+S3 verification completed.
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	_, err = Verify(dest)
	return result, err
}

// Strict comparison avoids silently accepting a partial restored database.
func compareTables(a, b []Table) error {
	if len(a) != len(b) {
		return ErrIntegrity
	}
	sort.Slice(a, func(i, j int) bool {
		if a[i].Schema != a[j].Schema {
			return a[i].Schema < a[j].Schema
		}
		return a[i].Name < a[j].Name
	})
	sort.Slice(b, func(i, j int) bool {
		if b[i].Schema != b[j].Schema {
			return b[i].Schema < b[j].Schema
		}
		return b[i].Name < b[j].Name
	})
	for i := range a {
		if a[i] != b[i] {
			return fmt.Errorf("%w: restored table differs", ErrIntegrity)
		}
	}
	return nil
}
