package media

import (
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/s3test"
)

func TestReadMixedHistoricalBackendsAndDeletionLock(t *testing.T) {
	s, _, e, session := fixture(t)
	data := testutil.Images()["image/png"]
	upload(t, s, e, session, "local.png")
	configs := map[string]config.S3{}
	for _, key := range []string{"s3-a", "s3-b"} {
		fake := s3test.New("")
		server := httptest.NewServer(fake)
		defer server.Close()
		configs[key] = config.S3{Endpoint: server.URL, Bucket: key, Region: "auto", PathStyle: true, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PresignTTL: time.Minute}
		backends, err := storage.Reconcile(t.Context(), s.db, config.Config{StorageProvider: "s3", StorageBackendKey: key, S3Backends: configs}, s.logger)
		if err != nil {
			t.Fatal(err)
		}
		s.ConfigureBackends(backends)
		p := prepare(t, s, e, session, "image/png", data)
		put(t, p, data)
		finish(t, s, e, session, p)
	}
	prepare(t, s, e, session, "image/png", data) // pending must not appear in snapshot
	snapshot, err := s.Snapshot(t.Context(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Assets) != 3 {
		t.Fatal("included nonready or missed historical assets")
	}
	for _, a := range snapshot.Assets {
		stream, info, err := snapshot.Open(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(stream)
		stream.Close()
		if err != nil || !bytes.Equal(got, data) || info.ContentType != "image/png" {
			t.Fatal("wrong backend read", err)
		}
	}
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrBusy) {
		t.Fatal("deletion raced pinned reads", err)
	}
	snapshot.Close()
	snapshot.Close()
	delete(configs, "s3-a")
	backends, err := storage.Reconcile(t.Context(), s.db, config.Config{StorageProvider: "local", S3Backends: configs}, s.logger)
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureBackends(backends)
	snapshot, err = s.Snapshot(t.Context(), e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	missing := 0
	for _, a := range snapshot.Assets {
		stream, _, err := snapshot.Open(t.Context(), a)
		if errors.Is(err, storage.ErrBackend) {
			missing++
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		stream.Close()
	}
	if missing != 1 {
		t.Fatal("historical credentials were silently replaced", missing)
	}
}
