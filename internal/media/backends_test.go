package media

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/s3test"
)

func TestHistoricalBackendRoutingAndMissingCredentials(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "all configured", true: "missing historical credentials"}[missing], func(t *testing.T) {
			s, dir, e, session := fixture(t)
			data := testutil.Images()["image/png"]
			aStore, bStore := s3test.New(""), s3test.New("")
			aHTTP, bHTTP := httptest.NewServer(aStore), httptest.NewServer(bStore)
			defer aHTTP.Close()
			defer bHTTP.Close()
			a := config.S3{Bucket: "bucket-a", Endpoint: aHTTP.URL, Region: "auto", Prefix: "a/", PathStyle: true, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PresignTTL: time.Minute}
			b := a
			b.Bucket = "bucket-b"
			b.Endpoint = bHTTP.URL
			b.Prefix = "b/"
			configure := func(active string, includeA bool) {
				t.Helper()
				cfg := config.Config{StorageProvider: "local", StorageBackendKey: active, S3Backends: map[string]config.S3{"s3-b": b}}
				if includeA {
					cfg.S3Backends["s3-a"] = a
				}
				if active != "local-default" {
					cfg.StorageProvider = "s3"
				}
				registry, err := storage.Reconcile(t.Context(), s.db, cfg, s.logger)
				if err != nil {
					t.Fatal(err)
				}
				s.ConfigureBackends(registry)
			}
			l := upload(t, s, e, session, "local.png")
			configure("s3-a", true)
			pa := prepare(t, s, e, session, "image/png", data)
			put(t, pa, data)
			stale := prepare(t, s, e, session, "image/png", data)
			put(t, stale, data)
			s.db.Exec("UPDATE assets SET created_at='2000-01-01T00:00:00Z',authorized_until='2000-01-01T00:00:00Z' WHERE id=?", stale.Asset.ID)
			configure("s3-b", true)
			before := len(bStore.Requests())
			refreshed, err := s.Authorize(t.Context(), e.PublicID, session.ID, pa.Asset.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldURL, _ := url.Parse(pa.Upload.URL)
			newURL, _ := url.Parse(refreshed.Upload.URL)
			if oldURL.Host != newURL.Host || oldURL.Path != newURL.Path {
				t.Fatal("refresh jumped to active backend")
			}
			finish(t, s, e, session, pa)
			if len(bStore.Requests()) != before {
				t.Fatal("historical HEAD/GET hit active backend")
			}
			pb := prepare(t, s, e, session, "image/png", data)
			put(t, pb, data)
			finish(t, s, e, session, pb)
			// Legacy provider/fingerprint columns are not routing authority after binding.
			if _, err := s.db.Exec("UPDATE assets SET storage_provider='local',storage_target='obsolete' WHERE id=?", pa.Asset.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			staleURL, _ := url.Parse(stale.Upload.URL)
			if _, exists := aStore.Objects()[staleURL.Path]; exists {
				t.Fatal("historical pending object not cleaned")
			}
			for id, want := range map[string]string{l.ID: "local-default", pa.Asset.ID: "s3-a", pb.Asset.ID: "s3-b"} {
				var key string
				if err := s.db.QueryRow("SELECT b.key FROM assets a JOIN storage_backends b ON a.storage_backend_id=b.id WHERE a.id=?", id).Scan(&key); err != nil || key != want {
					t.Fatal("changed asset ownership", err)
				}
			}
			aStore.Seed("/bucket-a/unrelated", data, "image/png")
			bStore.Seed("/bucket-b/unrelated", data, "image/png")
			configure("local-default", !missing)
			if missing {
				if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
					t.Fatal("missing historical credential was silently skipped", err)
				}
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM assets WHERE id=?", pa.Asset.ID).Scan(&count); err != nil || count != 1 {
					t.Fatal("lost retry metadata", err)
				}
				if _, exists := aStore.Objects()[oldURL.Path]; !exists {
					t.Fatal("missing-backend object lost")
				}
				configure("local-default", true)
			}
			if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "uploads", objectKey(e.ID, l.ID))); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("local media remains")
			}
			if len(aStore.Objects()) != 1 || len(bStore.Objects()) != 1 {
				t.Fatal("wrong destination deleted, or unrelated objects changed")
			}
			// Retired-key reconciliation also retains backend ownership across switches.
			put(t, pa, data)
			s.db.Exec("UPDATE s3_cleanup SET checked_at='2000-01-01T00:00:00Z'")
			configure("s3-b", false)
			if err := s.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(aStore.Objects()) != 2 {
				t.Fatal("missing retired backend did not retain object")
			}
			configure("s3-b", true)
			if err := s.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(aStore.Objects()) != 1 || len(bStore.Objects()) != 1 {
				t.Fatal("retired cleanup used wrong backend")
			}
		})
	}
}
