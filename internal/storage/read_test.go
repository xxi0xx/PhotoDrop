package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photodrop/internal/config"
)

func TestLocalReadOwnershipAndCancellation(t *testing.T) {
	dir := t.TempDir()
	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := local.Open(t.Context(), testKey); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	if _, _, err := local.Open(t.Context(), "../outside"); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
	if _, err := local.Put(t.Context(), testKey, strings.NewReader("ready bytes"), 100); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, info, err := local.Open(ctx, testKey)
	if err != nil || info.Size != 11 {
		t.Fatal(info, err)
	}
	data, err := io.ReadAll(stream)
	if err != nil || string(data) != "ready bytes" {
		t.Fatal(err)
	}
	cancel()
	if _, err := stream.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, testKey)); err != nil {
		t.Fatal("stream still holds file open", err)
	}
	if err := os.Mkdir(filepath.Join(dir, testKey), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := local.Open(t.Context(), testKey); !errors.Is(err, ErrUnavailable) {
		t.Fatal("opened non-regular file", err)
	}
}

func TestS3StreamAndFailures(t *testing.T) {
	s, fake := sdkFixture(t)
	key := s.Key(1, strings.Repeat("a", 32))
	data := bytes.Repeat([]byte{42}, 1<<20)
	fake.Seed("/photos/"+key, data, "image/png")
	stream, info, err := s.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || info.Size != int64(len(data)) || info.ContentType != "image/png" || !bytes.Equal(got, data) {
		t.Fatal("stream mismatch", info, err)
	}
	if err := s.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Open(t.Context(), key); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	fake.Fault("GET", 403)
	if _, _, err := s.Open(t.Context(), key); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestS3OpenDoesNotBufferBodyAndCloseStopsTransfer(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000000")
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("prefix"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	s := NewS3(config.S3{Endpoint: server.URL, Bucket: "photos", Region: "auto", PathStyle: true, AccessKeyID: "test", SecretAccessKey: "secret"})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	stream, info, err := s.Open(ctx, s.Key(1, strings.Repeat("a", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 100000000 {
		t.Fatal(info)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(stream, buf); err != nil || string(buf) != "prefix" {
		t.Fatal(err)
	}
	stream.Close()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("closing stream did not stop the transfer")
	}
}

func TestLocalReadRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("outside data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, testKey)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	local, err := NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stream, _, err := local.Open(t.Context(), testKey); err == nil {
		stream.Close()
		t.Fatal("followed symlink source")
	}
}
