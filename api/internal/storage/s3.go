package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
	"github.com/selectdev/purros/api/internal/config"
)

// S3 stores files in an S3-compatible bucket (AWS, R2, B2, Wasabi, MinIO…).
type S3 struct {
	client *minio.Client
	bucket string
	prefix string
	sse    encrypt.ServerSide
}

// NewS3 connects to a bucket. Without an access key it uses the standard
// AWS environment variables or the instance/task role.
func NewS3(c config.S3) (*S3, error) {
	if c.Bucket == "" {
		return nil, errors.New("an S3 bucket is required")
	}
	host, secure := "s3.amazonaws.com", true
	if c.Region != "" && c.Region != "auto" && c.Endpoint == "" {
		host = "s3." + c.Region + ".amazonaws.com"
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("S3 endpoint must be a URL like https://s3.example.com, got %q", c.Endpoint)
		}
		host, secure = u.Host, u.Scheme != "http"
	}
	var creds *credentials.Credentials
	if c.AccessKeyID != "" {
		creds = credentials.NewStaticV4(c.AccessKeyID, c.SecretAccessKey, "")
	} else {
		creds = credentials.NewChainCredentials([]credentials.Provider{&credentials.EnvAWS{}, &credentials.IAM{}})
	}
	lookup := minio.BucketLookupAuto
	if c.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(host, &minio.Options{Creds: creds, Secure: secure, Region: c.Region, BucketLookup: lookup})
	if err != nil {
		return nil, err
	}
	s := &S3{client: client, bucket: c.Bucket, prefix: c.Prefix}
	switch c.SSE {
	case "":
	case "AES256":
		s.sse = encrypt.NewSSE()
	case "aws:kms":
		if s.sse, err = encrypt.NewSSEKMS(c.KMSKeyID, nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("S3 server-side encryption must be AES256 or aws:kms, got %q", c.SSE)
	}
	return s, nil
}

func (s *S3) key(k string) (string, error) {
	if err := CheckKey(k); err != nil {
		return "", err
	}
	return s.prefix + k, nil
}

func notFound(err error) error {
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return ErrNotFound
	}
	return err
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// Unsigned payloads avoid aws-chunked framing, which not every
	// S3-compatible service decodes. Callers verify content with their own
	// SHA-256 checksums (attachments and backups both do).
	_, err = s.client.PutObject(ctx, s.bucket, k, r, size, minio.PutObjectOptions{ContentType: contentType,
		ServerSideEncryption: s.sse, DisableContentSha256: true})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	k, err := s.key(key)
	if err != nil {
		return nil, Object{}, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, k, minio.GetObjectOptions{})
	if err != nil {
		return nil, Object{}, notFound(err)
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, Object{}, notFound(err)
	}
	return obj, Object{Key: key, Size: st.Size, ModTime: st.LastModified, ContentType: st.ContentType}, nil
}

func (s *S3) Stat(ctx context.Context, key string) (Object, error) {
	k, err := s.key(key)
	if err != nil {
		return Object{}, err
	}
	st, err := s.client.StatObject(ctx, s.bucket, k, minio.StatObjectOptions{})
	if err != nil {
		return Object{}, notFound(err)
	}
	return Object{Key: key, Size: st.Size, ModTime: st.LastModified, ContentType: st.ContentType}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	k, err := s.key(key)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, k, minio.RemoveObjectOptions{})
}

func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	for o := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix + prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, Object{Key: strings.TrimPrefix(o.Key, s.prefix), Size: o.Size, ModTime: o.LastModified})
	}
	return out, nil
}

func (s *S3) SignedURL(ctx context.Context, key string, ttl time.Duration, filename, contentType string, inline bool) (string, error) {
	k, err := s.key(key)
	if err != nil {
		return "", err
	}
	disp := "attachment"
	if inline {
		disp = "inline"
	}
	params := url.Values{"response-content-disposition": {mime.FormatMediaType(disp, map[string]string{"filename": filename})}}
	if contentType != "" {
		params.Set("response-content-type", contentType)
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, k, ttl, params)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *S3) Describe() string { return "s3://" + s.bucket + "/" + s.prefix }

// EnsureBucket creates the bucket if it doesn't exist.
func (s *S3) EnsureBucket(ctx context.Context, region string) (bool, error) {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || ok {
		return false, err
	}
	return true, s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: region})
}
