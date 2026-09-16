package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 is a Store backed by any S3-compatible object store (MinIO, AWS S3, ...).
type S3 struct {
	client *minio.Client
	bucket string
}

// OpenS3 connects to an S3-compatible endpoint and ensures bucket exists.
//
// endpoint is host:port WITHOUT a URL scheme (e.g. "localhost:9000" or
// "s3.amazonaws.com"); useSSL selects https. A leading "http://" or
// "https://" is tolerated and stripped, with the scheme overriding useSSL.
func OpenS3(ctx context.Context, endpoint, accessKey, secretKey, bucket, region string, useSSL bool) (*S3, error) {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasPrefix(endpoint, "https://") {
		endpoint, useSSL = strings.TrimPrefix(endpoint, "https://"), true
	} else if strings.HasPrefix(endpoint, "http://") {
		endpoint, useSSL = strings.TrimPrefix(endpoint, "http://"), false
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	if endpoint == "" {
		return nil, errors.New("blob endpoint is required")
	}
	if bucket == "" {
		return nil, errors.New("blob bucket is required")
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}
	s := &S3{client: client, bucket: bucket}
	if err := s.ensureBucket(ctx, region); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *S3) ensureBucket(ctx context.Context, region string) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	err = s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region})
	if err == nil {
		return nil
	}
	// Concurrent creators race: treat "already exists / owned by you" as success.
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
		return nil
	}
	return fmt.Errorf("create bucket %q: %w", s.bucket, err)
}

// Bucket returns the configured bucket name.
func (s *S3) Bucket() string { return s.bucket }

// Put implements Store. An existing key is left untouched.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	exists, err := s.Exists(ctx, key)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object %q: %w", key, err)
	}
	return nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get object %q: %w", key, err)
	}
	// GetObject is lazy; Stat forces the request so a missing key surfaces here.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("stat object %q: %w", key, err)
	}
	return obj, nil
}

// Exists implements Store.
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat object %q: %w", key, err)
}

// Delete implements Store. S3 deletion is idempotent for missing keys.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("delete object %q: %w", key, err)
	}
	return nil
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NotFound", "NoSuchBucket":
		return true
	}
	return resp.StatusCode == 404
}

var _ Store = (*S3)(nil)
