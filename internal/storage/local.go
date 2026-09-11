// Package storage contains the local binary storage implementation. Keys are
// opaque to the media service; client filenames never enter this package.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
)

var ErrTooLarge = errors.New("file exceeds upload limit")
var ErrInvalidKey = errors.New("invalid storage key")

type StoredObject struct{ Size int64 }

// Store implements only the operations used by the local upload lifecycle.
// Put publishes a fully closed object or returns an error. Delete is idempotent
// and removes both an object's final file and its deterministic temporary file.
type Store interface {
	Put(context.Context, string, io.Reader, int64) (StoredObject, error)
	Delete(context.Context, string) error
}

type Local struct{ directory string }

// Flat keys avoid traversing intermediate directories, including symlinks.
var keyPattern = regexp.MustCompile(`^e[1-9][0-9]{0,18}_[0-9a-f]{32}$`)

func ValidKey(key string) bool { return keyPattern.MatchString(key) }

func NewLocal(directory string) (*Local, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create upload storage: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect upload storage: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("upload storage must be a real directory, not a symlink")
	}
	return &Local{directory: directory}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Local) Put(ctx context.Context, key string, source io.Reader, limit int64) (StoredObject, error) {
	if !ValidKey(key) {
		return StoredObject{}, ErrInvalidKey
	}
	if limit <= 0 {
		return StoredObject{}, ErrTooLarge
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return StoredObject{}, fmt.Errorf("open upload storage: %w", err)
	}
	defer root.Close()
	temporary := key + ".part"
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return StoredObject{}, fmt.Errorf("create temporary upload: %w", err)
	}
	defer func() { file.Close(); root.Remove(temporary) }()
	// The fixed buffer and reader wrapper deliberately prevent ReaderFrom /
	// WriterTo optimizations from turning this into a whole-file allocation.
	size, err := io.CopyBuffer(struct{ io.Writer }{file}, io.LimitReader(contextReader{ctx, source}, limit+1), make([]byte, 32*1024))
	if err != nil {
		return StoredObject{}, fmt.Errorf("stream upload: %w", err)
	}
	if size > limit {
		return StoredObject{}, ErrTooLarge
	}
	if size == 0 {
		return StoredObject{}, errors.New("empty stored object")
	}
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	if err := file.Sync(); err != nil {
		return StoredObject{}, fmt.Errorf("flush upload: %w", err)
	}
	if err := file.Close(); err != nil {
		return StoredObject{}, fmt.Errorf("close upload: %w", err)
	}
	// The O_EXCL temporary name serializes attempts for the same key. Recheck
	// the final name before rename so a completed object is never overwritten.
	if _, err := root.Lstat(key); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return StoredObject{}, fmt.Errorf("reserve final upload: %w", err)
	}
	if err := root.Rename(temporary, key); err != nil {
		return StoredObject{}, fmt.Errorf("publish upload: %w", err)
	}
	info, err := root.Lstat(key)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return StoredObject{}, errors.New("stored upload size could not be verified")
	}
	// Production runs on Linux. Sync the directory entry as well as file data.
	if runtime.GOOS != "windows" {
		dir, err := root.Open(".")
		if err != nil {
			return StoredObject{}, err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return StoredObject{}, fmt.Errorf("flush upload directory: %w", err)
		}
	}
	return StoredObject{Size: size}, nil
}

func (s *Local) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return fmt.Errorf("open upload storage: %w", err)
	}
	defer root.Close()
	// Remove does not follow a final symlink and never recursively deletes.
	for _, name := range []string{key + ".part", key} {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stored upload: %w", err)
		}
	}
	return nil
}
