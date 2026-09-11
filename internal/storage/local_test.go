package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testKey = "e1_0123456789abcdef0123456789abcdef"

type incrementalReader struct {
	remaining, reads int
	directory        string
}

func (r *incrementalReader) Read(p []byte) (int, error) {
	if len(p) > 32*1024 {
		return 0, errors.New("upload attempted an unbounded read")
	}
	if _, err := os.Stat(filepath.Join(r.directory, testKey+".part")); err != nil {
		return 0, errors.New("source consumed before temporary storage exists")
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.reads++
	r.remaining -= n
	return n, nil
}

func TestStreamedWriteAndDelete(t *testing.T) {
	dir := t.TempDir()
	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	reader := &incrementalReader{remaining: 2 * 1024 * 1024, directory: dir}
	object, err := local.Put(t.Context(), testKey, reader, 3*1024*1024)
	if err != nil || object.Size != 2*1024*1024 || reader.reads < 64 {
		t.Fatalf("streamed write: %+v, reads=%d, %v", object, reader.reads, err)
	}
	info, err := os.Stat(filepath.Join(dir, testKey))
	if err != nil || info.Size() != object.Size {
		t.Fatalf("final file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("upload permissions are not restrictive")
	}
	if _, err := os.Stat(filepath.Join(dir, testKey+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary file remains")
	}
	if _, err := local.Put(t.Context(), testKey, strings.NewReader("replacement"), 100); err == nil {
		t.Fatal("completed file overwritten")
	}
	if info, _ := os.Stat(filepath.Join(dir, testKey)); info.Size() != object.Size {
		t.Fatal("collision changed original object")
	}
	if err := local.Delete(t.Context(), testKey); err != nil {
		t.Fatal(err)
	}
	if err := local.Delete(t.Context(), testKey); err != nil {
		t.Fatal("delete is not idempotent")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestFailedWritesAndKeySafety(t *testing.T) {
	dir := t.TempDir()
	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "../escape", "/absolute", `C:\temp\file`, `..\escape`, "e2_../escape", testKey + "/child", testKey + ".part"} {
		if _, err := local.Put(t.Context(), key, strings.NewReader("x"), 10); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("write accepted %q", key)
		}
		if err := local.Delete(t.Context(), key); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("delete accepted %q", key)
		}
	}
	for name, source := range map[string]io.Reader{"interrupted": io.MultiReader(strings.NewReader("partial"), errorReader{}), "oversize": bytes.NewReader(make([]byte, 101)), "empty": strings.NewReader("")} {
		t.Run(name, func(t *testing.T) {
			if _, err := local.Put(t.Context(), testKey, source, 100); err == nil {
				t.Fatal("failed input was published")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed write left files: %v, %v", entries, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := local.Put(ctx, testKey, strings.NewReader("x"), 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestDeleteDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, testKey)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Delete(t.Context(), testKey); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatal("delete followed a symlink outside storage")
	}
}
