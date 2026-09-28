// Package storage keeps files (attachments, backups) on local disk or in an
// S3-compatible bucket.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/selectdev/purros/api/internal/config"
)

// ErrNotFound is returned for a missing object.
var ErrNotFound = errors.New("file not found in storage")

// ErrNoSignedURLs is returned by drivers that can't make signed URLs.
var ErrNoSignedURLs = errors.New("this storage doesn't support signed URLs")

// Object describes a stored file.
type Object struct {
	Key         string
	Size        int64
	ModTime     time.Time
	ContentType string
}

// Driver stores files by key. Keys use "/" separators.
type Driver interface {
	// Put stores r under key. size may be -1 when unknown.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, Object, error)
	Stat(ctx context.Context, key string) (Object, error)
	Delete(ctx context.Context, key string) error
	// List returns the objects whose key starts with prefix, sorted by key.
	List(ctx context.Context, prefix string) ([]Object, error)
	// SignedURL returns a short-lived download URL (S3 only).
	SignedURL(ctx context.Context, key string, ttl time.Duration, filename, contentType string, inline bool) (string, error)
	// Describe names the storage for messages, e.g. "s3://bucket/prefix".
	Describe() string
}

// New returns the driver for uploaded files.
func New(cfg config.Storage) (Driver, error) {
	switch cfg.Driver {
	case "", "local":
		return NewLocal(cfg.LocalPath), nil
	case "s3":
		return NewS3(cfg.S3)
	}
	return nil, fmt.Errorf("unknown STORAGE_DRIVER %q", cfg.Driver)
}

// CheckKey rejects keys that could escape a directory or confuse S3.
func CheckKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "\x00") {
		return fmt.Errorf("invalid storage key %q", key)
	}
	for part := range strings.SplitSeq(key, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid storage key %q", key)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Local disk
// ---------------------------------------------------------------------------

// Local stores files under a directory.
type Local struct{ Root string }

func NewLocal(root string) *Local { return &Local{Root: root} }

func (l *Local) path(key string) (string, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	return filepath.Join(l.Root, filepath.FromSlash(key)), nil
}

func (l *Local) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, Object, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Object{}, ErrNotFound
	}
	if err != nil {
		return nil, Object{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Object{}, err
	}
	return f, Object{Key: key, Size: st.Size(), ModTime: st.ModTime()}, nil
}

func (l *Local) Stat(_ context.Context, key string) (Object, error) {
	p, err := l.path(key)
	if err != nil {
		return Object{}, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, err
	}
	return Object{Key: key, Size: st.Size(), ModTime: st.ModTime()}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) List(_ context.Context, prefix string) ([]Object, error) {
	var out []Object
	err := filepath.WalkDir(l.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".upload-") {
			return nil
		}
		rel, err := filepath.Rel(l.Root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, Object{Key: key, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, err
}

func (l *Local) SignedURL(context.Context, string, time.Duration, string, string, bool) (string, error) {
	return "", ErrNoSignedURLs
}

func (l *Local) Describe() string { return "local:" + l.Root }
