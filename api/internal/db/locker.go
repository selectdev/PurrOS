package db

import (
	"io/fs"

	"github.com/pressly/goose/v3/lock"
)

func newLocker() (lock.SessionLocker, error) {
	return lock.NewPostgresSessionLocker()
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
