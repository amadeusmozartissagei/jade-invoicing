// Package objstore stores issued documents' files in Supabase Storage
// through its S3 protocol, and hands out presigned download URLs
// (ADR 0017, P5, D8).
//
// The S3 access key reaches Storage only, never the database; within Storage
// it bypasses RLS, so it stays on the server.
package objstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// InvoiceFilesBucket is the private bucket the migration
// 20261123120000_invoice_pdf_rendering.sql creates.
const InvoiceFilesBucket = "invoice-files"

// Config locates the bucket. Endpoint is Storage's S3 URL, ending in
// /storage/v1/s3.
type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

// Store reads and writes one bucket. It is safe for concurrent use.
type Store struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

// New returns a Store. It does no I/O.
func New(cfg Config) (*Store, error) {
	if cfg.Endpoint == "" || cfg.Region == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" || cfg.Bucket == "" {
		return nil, errors.New("objstore: incomplete configuration")
	}
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		// Supabase's S3 endpoint takes the bucket in the path.
		UsePathStyle: true,
		// Send and check checksums only where S3 requires them; the SDK's
		// newer default adds headers other S3 implementations may reject.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Store{bucket: cfg.Bucket, client: client, presign: s3.NewPresignClient(client)}, nil
}

// Put writes body under key, replacing an object with the same key. Callers
// name objects by their content hash, so a replacement has the same bytes
// (ADR 0017, D2).
func (s *Store) Put(ctx context.Context, key string, body []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("objstore: put %s: %w", key, err)
	}
	return nil
}

// PresignGet returns a SigV4 query-signed GET URL for key that expires after
// ttl. It signs locally and does no I/O, so it does not check that the
// object exists.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("objstore: presign %s: %w", key, err)
	}
	return req.URL, nil
}
