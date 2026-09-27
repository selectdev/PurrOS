// Package storagetest runs an in-memory S3 server for tests.
package storagetest

import (
	"net/http/httptest"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/selectdev/purros/api/internal/config"
)

// S3 starts a fake S3 server with the given buckets and returns a config
// pointing at it.
func S3(t *testing.T, buckets ...string) config.S3 {
	t.Helper()
	backend := s3mem.New()
	for _, b := range buckets {
		if err := backend.CreateBucket(b); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	return config.S3{Bucket: buckets[0], Region: "us-east-1", Endpoint: srv.URL, AccessKeyID: "test", SecretAccessKey: "testtesttest", ForcePathStyle: true}
}
