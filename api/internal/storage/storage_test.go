package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/selectdev/purros/api/internal/storage"
	"github.com/selectdev/purros/api/internal/storage/storagetest"
)

func exercise(t *testing.T, d storage.Driver) {
	ctx := context.Background()
	data := bytes.Repeat([]byte("purros "), 10_000)
	if err := d.Put(ctx, "a/b/one.bin", bytes.NewReader(data), -1, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(ctx, "a/two.txt", strings.NewReader("hi"), 2, "text/plain"); err != nil {
		t.Fatal(err)
	}
	rc, obj, err := d.Get(ctx, "a/b/one.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, data) || obj.Size != int64(len(data)) {
		t.Fatalf("round trip: %d bytes, size %d", len(got), obj.Size)
	}
	if st, err := d.Stat(ctx, "a/two.txt"); err != nil || st.Size != 2 {
		t.Fatalf("stat: %+v %v", st, err)
	}
	list, err := d.List(ctx, "a/")
	if err != nil || len(list) != 2 || list[0].Key != "a/b/one.bin" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := d.Delete(ctx, "a/two.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Stat(ctx, "a/two.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted object: %v", err)
	}
	if _, _, err := d.Get(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing object: %v", err)
	}
	for _, bad := range []string{"../x", "/abs", "a//b", "a/./b", ""} {
		if err := d.Put(ctx, bad, strings.NewReader("x"), 1, ""); err == nil {
			t.Fatalf("key %q accepted", bad)
		}
	}
}

func TestLocal(t *testing.T) {
	d := storage.NewLocal(t.TempDir())
	exercise(t, d)
	if _, err := d.SignedURL(context.Background(), "a/b/one.bin", time.Minute, "one.bin", "", false); !errors.Is(err, storage.ErrNoSignedURLs) {
		t.Fatal("local storage has no signed URLs")
	}
}

func TestS3(t *testing.T) {
	cfg := storagetest.S3(t, "files")
	cfg.Prefix = "tenant/"
	d, err := storage.NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, d)
	u, err := d.SignedURL(context.Background(), "a/b/one.bin", time.Minute, "report é.bin", "application/octet-stream", true)
	if err != nil || !strings.Contains(u, "tenant/a/b/one.bin") || !strings.Contains(u, "X-Amz-Signature") {
		t.Fatalf("signed url %q %v", u, err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("signed URL answered %d", resp.StatusCode)
	}
	if _, err := storage.NewS3(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SSE = "bogus"
	if _, err := storage.NewS3(cfg); err == nil {
		t.Fatal("bad SSE accepted")
	}
}
