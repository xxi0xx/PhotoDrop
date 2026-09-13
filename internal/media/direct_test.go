package media

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/events"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/s3test"
)

// Rebuild the runtime registry to model a process restart; production never
// changes its active backend or credential map while serving requests.
func configureDirect(t *testing.T, s *Service, remote *storage.S3, active bool) {
	t.Helper()
	cfg := config.Config{StorageProvider: "local", S3Backends: map[string]config.S3{}}
	if remote != nil {
		origin, err := remote.UploadOrigin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		cfg.S3Backends["test-store"] = config.S3{Bucket: "photos", Endpoint: origin, Region: s3test.Region, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PathStyle: true, Prefix: "test/", PresignTTL: time.Minute}
	}
	if active {
		cfg.StorageProvider = "s3"
		cfg.StorageBackendKey = "test-store"
	}
	backends, err := storage.Reconcile(t.Context(), s.db, cfg, s.logger)
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureBackends(backends)
}

func directFixture(t *testing.T) (*Service, string, events.Event, Session, *s3test.Server, *storage.S3) {
	t.Helper()
	s, dir, e, session := fixture(t)
	fake := s3test.New("")
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	remote := storage.NewS3(config.S3{Bucket: "photos", Endpoint: server.URL, Region: s3test.Region, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PathStyle: true, Prefix: "test/", PresignTTL: time.Minute})
	configureDirect(t, s, remote, true)
	return s, dir, e, session, fake, remote
}
func prepare(t *testing.T, s *Service, e events.Event, session Session, kind string, data []byte) Prepared {
	t.Helper()
	p, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"../../<script>file.jpg", int64(len(data)), kind, randomID()}, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func directPUT(p storage.UploadPlan, data []byte) (int, error) {
	r, _ := http.NewRequest(p.Method, p.URL, bytes.NewReader(data))
	for k, v := range p.Headers {
		r.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}
func put(t *testing.T, p Prepared, data []byte) {
	t.Helper()
	status, err := directPUT(p.Upload, data)
	if err != nil || status != 200 {
		t.Fatalf("PUT: %d %v", status, err)
	}
}
func finish(t *testing.T, s *Service, e events.Event, session Session, p Prepared) Asset {
	t.Helper()
	a, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDirectFormatsAndIdempotentCompletion(t *testing.T) {
	s, _, e, session, fake, _ := directFixture(t)
	var total int64
	for kind, data := range testutil.Images() {
		p := prepare(t, s, e, session, kind, data)
		if p.Asset.Status != "pending" {
			t.Fatal("premature readiness")
		}
		if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, storage.ErrMissing) {
			t.Fatal("missing object became ready", err)
		}
		put(t, p, data)
		before := len(fake.Requests())
		a := finish(t, s, e, session, p)
		if a.Status != "ready" || a.Size != int64(len(data)) || a.MIMEType != kind {
			t.Fatal("incorrect verified metadata")
		}
		total += a.Size
		again := finish(t, s, e, session, p)
		if again != a || len(fake.Requests()) != before+2 {
			t.Fatal("repeat completion performed I/O or changed asset")
		}
		if _, err := s.Authorize(t.Context(), e.PublicID, session.ID, a.ID); !errors.Is(err, ErrReady) {
			t.Fatal("reauthorized ready asset")
		}
		status, err := directPUT(p.Upload, []byte("overwrite"))
		if err != nil || status != 412 {
			t.Fatal("ready object overwritten")
		}
	}
	stats, err := s.Stats(t.Context())
	if err != nil || stats[e.ID].PhotoCount != 6 || stats[e.ID].StorageBytes != total {
		t.Fatal("incorrect ready totals", err)
	}
	for _, r := range fake.Requests() {
		if r.Method == "GET" && r.Range != "bytes=0-511" {
			t.Fatal("whole-object verification requested")
		}
	}
}

func TestDirectPrepareValidationIsolationAndRefresh(t *testing.T) {
	s, _, e, session, _, _ := directFixture(t)
	data := testutil.Images()["image/png"]
	input := Preparation{"same.png", int64(len(data)), "image/png", randomID()}
	for _, bad := range []Preparation{{"", 10, "image/png", randomID()}, {strings.Repeat("x", 256), 10, "image/png", randomID()}, {"x", 0, "image/png", randomID()}, {"x", 1025, "image/png", randomID()}, {"x", 10, "video/mp4", randomID()}, {"x", 10, "image/png", "bad"}} {
		if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, bad, 1024); err == nil {
			t.Fatal("accepted bad metadata")
		}
	}
	if countAssets(t, s, "pending") != 0 {
		t.Fatal("invalid preparation inserted records")
	}
	other := createEvent(t, s)
	otherSession, _ := s.CreateSession(t.Context(), other.PublicID)
	for _, pair := range [][2]string{{other.PublicID, session.ID}, {e.PublicID, otherSession.ID}, {e.PublicID, randomID()}, {"1", session.ID}, {"unknown", session.ID}} {
		if _, err := s.Prepare(t.Context(), pair[0], pair[1], input, 1024); err == nil {
			t.Fatal("accepted unrelated event/session")
		}
	}
	p, err := s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024)
	if err != nil || repeated.Asset.ID != p.Asset.ID || countAssets(t, s, "pending") != 1 {
		t.Fatal("lost prepare response duplicated asset", err)
	}
	input.Filename = "changed.png"
	if _, err = s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024); !errors.Is(err, ErrRequest) {
		t.Fatal("changed immutable metadata")
	}
	for _, pair := range [][2]string{{other.PublicID, session.ID}, {e.PublicID, otherSession.ID}, {e.PublicID, randomID()}, {"1", session.ID}} {
		if _, err = s.Authorize(t.Context(), pair[0], pair[1], p.Asset.ID); err == nil {
			t.Fatal("cross-session authorization")
		}
		if _, err = s.Complete(t.Context(), pair[0], pair[1], p.Asset.ID); err == nil {
			t.Fatal("cross-session completion")
		}
	}
	expired := p.Upload
	u, _ := url.Parse(expired.URL)
	q := u.Query()
	q.Set("X-Amz-Date", "20000101T000000Z")
	u.RawQuery = q.Encode()
	expired.URL = u.String()
	status, err := directPUT(expired, data)
	if err != nil || status != 403 {
		t.Fatal("expired URL accepted")
	}
	refreshed, err := s.Authorize(t.Context(), e.PublicID, session.ID, p.Asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldURL, _ := url.Parse(p.Upload.URL)
	newURL, _ := url.Parse(refreshed.Upload.URL)
	if refreshed.Asset.ID != p.Asset.ID || oldURL.Path != newURL.Path || countAssets(t, s, "pending") != 1 {
		t.Fatal("refresh changed identity")
	}
	put(t, refreshed, data)
	if _, err := s.db.Exec("UPDATE events SET enabled=0 WHERE id=?", e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, ErrClosed) {
		t.Fatal("closed authorization", err)
	}
	if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024); !errors.Is(err, ErrClosed) {
		t.Fatal("closed preparation", err)
	}
	finish(t, s, e, session, p) // Already-authorized valid upload survives event closure.
	if _, err := s.db.Exec("UPDATE events SET enabled=1,expires_at='2000-01-01T00:00:00Z' WHERE id=?", e.ID); err != nil {
		t.Fatal(err)
	}
	input.RequestID = randomID()
	if _, err = s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024); !errors.Is(err, ErrClosed) {
		t.Fatal("expired preparation", err)
	}
}

func TestDirectFailedVerificationAndLostResponses(t *testing.T) {
	s, _, e, session, fake, _ := directFixture(t)
	data := testutil.Images()["image/png"]
	for name, bad := range map[string][]byte{"empty": {}, "wrong size": append(bytes.Clone(data), 1), "fake image": bytes.Repeat([]byte("x"), len(data))} {
		t.Run(name, func(t *testing.T) {
			p := prepare(t, s, e, session, "image/png", data)
			put(t, p, bad)
			if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); err == nil {
				t.Fatal("invalid object finalized")
			}
			u, _ := url.Parse(p.Upload.URL)
			if _, exists := fake.Objects()[u.Path]; exists {
				t.Fatal("invalid object not deleted")
			}
			if countAssets(t, s, "ready") != 0 {
				t.Fatal("invalid object counted")
			}
		})
	}
	p := prepare(t, s, e, session, "image/png", data)
	fake.DropPUTResponses(1)
	if _, err := directPUT(p.Upload, data); err == nil {
		t.Fatal("lost response was not injected")
	}
	a := finish(t, s, e, session, p)
	if again := finish(t, s, e, session, p); again != a || countAssets(t, s, "ready") != 1 {
		t.Fatal("lost response duplicated asset")
	}
	p = prepare(t, s, e, session, "image/png", data)
	put(t, p, data)
	for _, method := range []string{"HEAD", "GET"} {
		fake.Fault(method, 503)
		if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, storage.ErrUnavailable) {
			t.Fatal("provider error", err)
		}
		fake.Fault(method, 0)
		if countAssets(t, s, "ready") != 1 {
			t.Fatal("provider failure became ready")
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_s3_ready BEFORE UPDATE OF status ON assets WHEN NEW.status='ready' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); err == nil {
		t.Fatal("DB failure reported success")
	}
	s.db.Exec("DROP TRIGGER fail_s3_ready")
	finish(t, s, e, session, p) // Keep verified bytes after DB failure for finalize-first recovery.
}

func TestMixedProviderDeletionAndRetiredReplay(t *testing.T) {
	s, dir, e, session, fake, remote := directFixture(t)
	data := testutil.Images()["image/png"]
	configureDirect(t, s, remote, false)
	local := upload(t, s, e, session, "same.png")
	configureDirect(t, s, remote, true)
	p := prepare(t, s, e, session, "image/png", data)
	put(t, p, data)
	finish(t, s, e, session, p)
	other := createEvent(t, s)
	otherSession, _ := s.CreateSession(t.Context(), other.PublicID)
	keep := prepare(t, s, other, otherSession, "image/png", data)
	put(t, keep, data)
	finish(t, s, other, otherSession, keep)
	fake.Seed("/photos/unrelated", data, "image/png")
	configureDirect(t, s, remote, false) // Active local mode still deletes historical S3.
	fake.Fault("DELETE", 503)
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
		t.Fatal("partial deletion was not retained", err)
	}
	event, err := s.events.Get(t.Context(), e.ID)
	if err != nil || !event.Deleting || event.Enabled {
		t.Fatal("partial delete reopened event")
	}
	fake.Fault("DELETE", 0)
	if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", objectKey(e.ID, local.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("historical local media remains")
	}
	if len(fake.Objects()) != 2 {
		t.Fatal("unrelated S3 objects affected")
	}
	// An issued URL may recreate a deleted object. A durable retirement record
	// keeps it discoverable even after the event and asset rows are gone.
	put(t, p, data)
	if _, err := s.db.Exec("UPDATE s3_cleanup SET checked_at='2000-01-01T00:00:00Z'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.Objects()) != 2 {
		t.Fatal("late replay was not reconciled")
	}
	if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); err == nil {
		t.Fatal("deleted event accepted complete")
	}
}

func TestDirectStaleCleanupAndBackendIdentity(t *testing.T) {
	s, _, e, session, fake, remote := directFixture(t)
	data := testutil.Images()["image/png"]
	fresh := prepare(t, s, e, session, "image/png", data)
	put(t, fresh, data)
	stale := prepare(t, s, e, session, "image/png", data)
	put(t, stale, data)
	missing := prepare(t, s, e, session, "image/png", data)
	ready := prepare(t, s, e, session, "image/png", data)
	put(t, ready, data)
	finish(t, s, e, session, ready)
	s.db.Exec("UPDATE assets SET created_at='2000-01-01T00:00:00Z'")
	s.db.Exec("UPDATE assets SET authorized_until='2000-01-01T00:00:00Z' WHERE id IN (?,?)", stale.Asset.ID, missing.Asset.ID)
	if err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if countAssets(t, s, "pending") != 1 || countAssets(t, s, "ready") != 1 || len(fake.Objects()) != 2 {
		t.Fatal("stale cleanup affected fresh/ready assets")
	}
	configureDirect(t, s, nil, false)
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
		t.Fatal("missing historical backend reported deletion", err)
	}
	configureDirect(t, s, remote, false)
	if _, err := s.db.Exec("UPDATE assets SET storage_key='wrong-prefix' WHERE id=?", ready.Asset.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
		t.Fatal("changed backing accepted")
	}
	u, _ := url.Parse(ready.Upload.URL)
	if _, exists := fake.Objects()[u.Path]; !exists {
		t.Fatal("wrong backend deleted data")
	}
}

func TestConcurrentDirectFinalization(t *testing.T) {
	s, _, e, session, fake, _ := directFixture(t)
	data := testutil.Images()["image/png"]
	p := prepare(t, s, e, session, "image/png", data)
	put(t, p, data)
	var group sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		group.Go(func() {
			_, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID)
			if err == nil {
				_, err = s.Stats(t.Context())
			}
			errs <- err
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	heads, gets := 0, 0
	for _, r := range fake.Requests() {
		if r.Method == "HEAD" {
			heads++
		}
		if r.Method == "GET" {
			gets++
		}
	}
	if countAssets(t, s, "ready") != 1 || heads != 1 || gets != 1 {
		t.Fatal("concurrent finalizers duplicated verification or metadata")
	}
}
