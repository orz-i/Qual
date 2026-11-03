package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	s3store "qual/internal/evidence/infrastructure/s3store"
)

type DrillReport struct {
	Verified           bool `json:"verified"`
	TablesCompared     int  `json:"tables_compared"`
	ObjectsRestored    int  `json:"objects_restored"`
	PostgreSQLRestored bool `json:"postgresql_restored"`
	IsolatedS3Restored bool `json:"isolated_s3_restored"`
	ScratchCleaned     bool `json:"scratch_cleaned"`
}

func (s *Service) RestoreDrill(ctx context.Context, dir string) (report DrillReport, err error) {
	if s == nil || s.Pool == nil || s.Objects == nil {
		return report, ErrUnavailable
	}
	manifest, err := Verify(dir)
	if err != nil {
		return report, err
	}
	// All resource names are randomly generated; no user-specified restore target.
	short := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	scratchDB := "qual_drill_" + short
	scratchBucket := "qual-drill-" + short
	if !dbNameRE.MatchString(scratchDB) {
		return report, ErrInvalid
	}
	if err := runDocker(ctx, s.ComposeFile, nil, io.Discard, "createdb", "-U", "qual", scratchDB); err != nil {
		return report, err
	}
	createdBucket := false
	var scratchPool *pgxpool.Pool
	copied := []string{}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if scratchPool != nil {
			scratchPool.Close()
		}
		var cleanupErr error
		if createdBucket {
			tempStore := &s3store.Store{Client: s.Objects.Client, Bucket: scratchBucket}
			for _, key := range copied {
				if deleteErr := tempStore.Delete(cleanupCtx, key); deleteErr != nil {
					cleanupErr = ErrUnavailable
				}
			}
			if _, deleteErr := s.Objects.Client.DeleteBucket(cleanupCtx, &s3.DeleteBucketInput{Bucket: aws.String(scratchBucket)}); deleteErr != nil {
				cleanupErr = ErrUnavailable
			}
		}
		if dbErr := runDocker(cleanupCtx, s.ComposeFile, nil, io.Discard,
			"dropdb", "-U", "qual", "--if-exists", "--force", scratchDB); dbErr != nil {
			cleanupErr = ErrUnavailable
		}
		if cleanupErr != nil && err == nil {
			err = errors.New("scratch recovery cleanup incomplete")
		}
		report.ScratchCleaned = cleanupErr == nil
	}()
	archive, err := os.Open(filepath.Join(dir, "database.dump"))
	if err != nil {
		return report, ErrIntegrity
	}
	err = runDocker(ctx, s.ComposeFile, archive, io.Discard,
		"pg_restore", "-U", "qual", "-d", scratchDB, "--no-owner", "--no-acl", "--exit-on-error")
	_ = archive.Close()
	if err != nil {
		return report, fmt.Errorf("scratch pg_restore failed: %w", err)
	}
	config, err := pgxpool.ParseConfig(s.DatabaseURL)
	if err != nil {
		return report, ErrInvalid
	}
	config.ConnConfig.Database = scratchDB
	scratchPool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return report, ErrUnavailable
	}
	if err := scratchPool.Ping(ctx); err != nil {
		return report, ErrUnavailable
	}
	tables, err := tableFingerprints(ctx, scratchPool)
	if err != nil {
		return report, err
	}
	if err := compareTables(append([]Table(nil), manifest.Tables...), tables); err != nil {
		return report, err
	}
	report.PostgreSQLRestored = true
	report.TablesCompared = len(tables)

	// Reconstruct S3 from the *backup bytes* in a brand-new bucket, not by
	// reading the live source bucket. Keep the original object_key unchanged.
	_, err = s.Objects.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(scratchBucket)})
	if err != nil {
		return report, fmt.Errorf("isolated S3 bucket creation failed: %w", ErrUnavailable)
	}
	createdBucket = true
	tempStore := &s3store.Store{Client: s.Objects.Client, Bucket: scratchBucket}
	for _, item := range manifest.Objects {
		body, err := os.ReadFile(filepath.Join(dir, item.File))
		if err != nil {
			return report, ErrIntegrity
		}
		if int64(len(body)) != item.Size || digest(body) != item.SHA256 {
			return report, ErrIntegrity
		}
		if err := tempStore.Put(ctx, item.Key, item.ContentType, body); err != nil {
			return report, ErrUnavailable
		}
		copied = append(copied, item.Key)
	}
	// Ensure database S3 metadata is consistent with the signed manifest;
	// contents and object hashes must match in the new bucket.
	sqlRows, err := scratchPool.Query(ctx, `
  SELECT object_key,size_bytes,encode(sha256,'hex'),'artifact'
  FROM qual.artifacts WHERE state IN ('ready','uploaded')
  UNION ALL
  SELECT object_key,size_bytes,encode(archive_sha256,'hex'),'export'
  FROM qual.archive_exports ORDER BY 1`)
	if err != nil {
		return report, err
	}
	expected := make(map[string]Object, len(manifest.Objects))
	for _, item := range manifest.Objects {
		expected[item.Key] = item
	}
	seen := map[string]bool{}
	for sqlRows.Next() {
		var key, sha, kind string
		var size int64
		if err := sqlRows.Scan(&key, &size, &sha, &kind); err != nil {
			sqlRows.Close()
			return report, err
		}
		item, ok := expected[key]
		if !ok || seen[key] || item.Kind != kind || item.Size != size || item.SHA256 != sha {
			sqlRows.Close()
			return report, ErrIntegrity
		}
		seen[key] = true
	}
	err = sqlRows.Err()
	sqlRows.Close()
	if err != nil {
		return report, err
	}
	if len(seen) != len(manifest.Objects) {
		return report, ErrIntegrity
	}
	for _, item := range manifest.Objects {
		data, err := tempStore.Get(ctx, item.Key, MaxSingleObjectBytes)
		if err != nil {
			return report, ErrIntegrity
		}
		if int64(len(data)) != item.Size || digest(data) != item.SHA256 {
			return report, ErrIntegrity
		}
		report.ObjectsRestored++
	}
	report.IsolatedS3Restored = true
	report.Verified = true
	return report, nil
}
