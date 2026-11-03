// Package s3store uses the official AWS SDK only behind the Qual server.
// Clients never receive AWS credentials or private object keys.
package s3store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Store struct {
	Client *s3.Client
	Bucket string
}

const MaxObjectBytes = 32 * 1024 * 1024

func NewFromEnv(ctx context.Context) (*Store, error) {
	endpoint := strings.TrimSpace(os.Getenv("QUAL_S3_ENDPOINT"))
	if endpoint == "" {
		return nil, nil
	}
	uri, err := url.Parse(endpoint)
	if err != nil || uri.Host == "" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || uri.Path != "" && uri.Path != "/" {
		return nil, errors.New("invalid S3 endpoint")
	}
	if uri.Scheme != "https" {
		ip := net.ParseIP(uri.Hostname())
		if uri.Scheme != "http" || (uri.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("S3 endpoint requires HTTPS, except loopback development")
		}
	}
	region := os.Getenv("QUAL_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	access, secret := os.Getenv("QUAL_S3_ACCESS_KEY"), os.Getenv("QUAL_S3_SECRET_KEY")
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if access != "" || secret != "" {
		if access == "" || secret == "" {
			return nil, errors.New("S3 credential pair incomplete")
		}
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, errors.New("cannot configure S3")
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimSuffix(endpoint, "/"))
		o.UsePathStyle = true
	})
	bucket := os.Getenv("QUAL_S3_BUCKET")
	if bucket == "" {
		bucket = "qual-artifacts"
	}
	if len(bucket) < 3 || len(bucket) > 63 {
		return nil, errors.New("invalid S3 bucket")
	}
	store := &Store{Client: client, Bucket: bucket}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := client.HeadBucket(checkCtx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return nil, fmt.Errorf("S3 bucket unavailable: %w", err)
	}
	return store, nil
}
func (s *Store) Check(ctx context.Context) error {
	if s == nil {
		return errors.New("object storage not configured")
	}
	_, err := s.Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.Bucket)})
	return err
}
func (s *Store) Put(ctx context.Context, key, contentType string, data []byte) error {
	if s == nil || len(data) > MaxObjectBytes || len(data) == 0 {
		return errors.New("object too large or object store unavailable")
	}
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket), Key: aws.String(key), ContentType: aws.String(contentType),
		Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))),
	})
	return err
}
func (s *Store) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if s == nil {
		return nil, errors.New("object store unavailable")
	}
	obj, err := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer obj.Body.Close()
	if obj.ContentLength != nil && *obj.ContentLength > limit {
		return nil, errors.New("object exceeds limit")
	}
	data, err := io.ReadAll(io.LimitReader(obj.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("object exceeds limit")
	}
	return data, nil
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if s == nil {
		return errors.New("object store unavailable")
	}
	_, err := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	return err
}
