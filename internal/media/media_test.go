package media

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
)

func fixture(t *testing.T) (*Service, string, events.Event, Session) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	objects, err := storage.NewLocal(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, objects, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := createEvent(t, s)
	session, err := s.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, e, session
}
func createEvent(t *testing.T, s *Service) events.Event {
	t.Helper()
	enabled := true
	e, err := s.events.Create(t.Context(), events.Input{Name: "Upload test", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func upload(t *testing.T, s *Service, e events.Event, session Session, filename string) Asset {
	t.Helper()
	data := testutil.Images()["image/png"]
	a, err := s.Upload(t.Context(), e.PublicID, session.ID, filename, "image/png", bytes.NewReader(data), int64(len(data)), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func countAssets(t *testing.T, s *Service, status string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM assets WHERE status = ?", status).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIncompleteCleanupPreservesCommittedReadyAsset(t *testing.T) {
	s, dir, e, session := fixture(t)
	a := upload(t, s, e, session, "completed.png")
	key := objectKey(e.ID, a.ID)
	if err := s.removeAttempt(t.Context(), e.ID, a.ID, key); err == nil {
		t.Fatal("cleanup accepted a completed asset")
	}
	data, err := os.ReadFile(filepath.Join(dir, "uploads", key))
	if err != nil || !bytes.Equal(data, testutil.Images()["image/png"]) || countAssets(t, s, "ready") != 1 {
		t.Fatalf("cleanup damaged a completed asset: %v", err)
	}
}

func TestFormatsFilenamesStatsAndRestart(t *testing.T) {
	s, dir, e, session := fixture(t)
	var total int64
	for kind, data := range testutil.Images() {
		a, err := s.Upload(t.Context(), e.PublicID, session.ID, "same-name.jpg", "application/octet-stream", bytes.NewReader(data), -1, 1024*1024)
		if err != nil || a.MIMEType != kind || a.Status != "ready" || a.Size != int64(len(data)) {
			t.Fatalf("%s: %+v, %v", kind, a, err)
		}
		total += a.Size
	}
	for _, filename := range []string{"../../etc/passwd", `..\..\windows\system32\test.jpg`, "/absolute/path.jpg", `C:\temp\file.jpg`, "<script>alert(1)</script>.jpg", "IMG_0001.JPG", "IMG_0001.JPG"} {
		a := upload(t, s, e, session, filename)
		total += a.Size
		var name, key string
		if err := s.db.QueryRow("SELECT original_filename, storage_key FROM assets WHERE id = ?", a.ID).Scan(&name, &key); err != nil {
			t.Fatal(err)
		}
		if name != filename || key != objectKey(e.ID, a.ID) {
			t.Fatal("filename was lost or influenced storage")
		}
		if _, err := os.Stat(filepath.Join(dir, "uploads", key)); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.Stats(t.Context())
	if err != nil || stats[e.ID].PhotoCount != 13 || stats[e.ID].StorageBytes != total {
		t.Fatalf("stats: %+v, %v", stats, err)
	}
	s.db.Close()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restarted := New(db, s.storage, s.logger)
	if err := restarted.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := restarted.Stats(t.Context())
	if err != nil || after[e.ID] != stats[e.ID] {
		t.Fatal("restart lost ready assets")
	}
	files, err := os.ReadDir(filepath.Join(dir, "uploads"))
	if err != nil || len(files) != 13 {
		t.Fatalf("restart lost media: %d, %v", len(files), err)
	}
}

func TestValidationAndSessionIsolation(t *testing.T) {
	s, _, e, session := fixture(t)
	image := testutil.Images()["image/jpeg"]
	for _, tc := range []struct {
		name, filename, kind string
		data                 []byte
		limit                int64
		expected             error
	}{
		{"empty", "a.jpg", "image/jpeg", nil, 1000, ErrEmpty},
		{"disguised text", "a.jpg", "image/jpeg", []byte("not an image"), 1000, ErrType},
		{"svg", "a.svg", "image/svg+xml", []byte("<svg/>"), 1000, ErrType},
		{"video", "a.mov", "video/quicktime", image, 1000, ErrType},
		{"missing filename", "", "image/jpeg", image, 1000, ErrFilename},
		{"long filename", strings.Repeat("é", 256), "image/jpeg", image, 1000, ErrFilename},
		{"invalid UTF8", string([]byte{255}), "image/jpeg", image, 1000, ErrFilename},
		{"unknown length oversize", "a.jpg", "image/jpeg", image, 100, storage.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Upload(t.Context(), e.PublicID, session.ID, tc.filename, tc.kind, bytes.NewReader(tc.data), -1, tc.limit)
			if !errors.Is(err, tc.expected) {
				t.Fatalf("got %v, want %v", err, tc.expected)
			}
		})
	}
	other := createEvent(t, s)
	for _, tc := range []struct {
		publicID, sessionID string
		expected            error
	}{
		{other.PublicID, session.ID, ErrSession}, {e.PublicID, "bogus", ErrSession}, {e.PublicID, randomID(), ErrSession}, {"1", session.ID, sql.ErrNoRows}, {"AAAAAAAAAAAAAAAAAAAAAAAA", session.ID, sql.ErrNoRows},
	} {
		_, err := s.Upload(t.Context(), tc.publicID, tc.sessionID, "a.jpg", "image/jpeg", bytes.NewReader(image), -1, 1000)
		if !errors.Is(err, tc.expected) {
			t.Fatalf("isolation: %v", err)
		}
	}
	if countAssets(t, s, "ready") != 0 || countAssets(t, s, "pending") != 0 {
		t.Fatal("invalid uploads left asset records")
	}
}

type callbackReader struct {
	callback func()
	reader   io.Reader
}

func (r *callbackReader) Read(p []byte) (int, error) {
	if r.callback != nil {
		cb := r.callback
		r.callback = nil
		cb()
	}
	return r.reader.Read(p)
}

func TestAvailabilityDuringUploadAndDeletion(t *testing.T) {
	s, dir, e, session := fixture(t)
	data := testutil.Images()["image/jpeg"]
	reader := &callbackReader{reader: bytes.NewReader(data), callback: func() {
		if countAssets(t, s, "pending") != 1 {
			t.Fatal("missing pending row before receiving media")
		}
		stats, err := s.Stats(t.Context())
		if err != nil || stats[e.ID].PhotoCount != 0 {
			t.Fatal("pending asset counted")
		}
		if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrBusy) {
			t.Fatalf("active deletion: %v", err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := s.db.ExecContext(ctx, "UPDATE events SET enabled = 0 WHERE id = ?", e.ID); err != nil {
			t.Fatal("upload holds a database transaction while reading", err)
		}
	}}
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "a.jpg", "image/jpeg", reader, -1, 1000); !errors.Is(err, ErrClosed) {
		t.Fatalf("event closed during upload: %v", err)
	}
	if countAssets(t, s, "ready") != 0 || countAssets(t, s, "pending") != 0 {
		t.Fatal("closed event upload was not cleaned")
	}
	if files, _ := os.ReadDir(filepath.Join(dir, "uploads")); len(files) != 0 {
		t.Fatal("closed event retained upload")
	}
	for _, statement := range []string{"UPDATE events SET enabled = 0", "UPDATE events SET enabled = 1, expires_at = '2000-01-01T00:00:00Z'"} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateSession(t.Context(), e.PublicID); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed session: %v", err)
		}
		if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "a.jpg", "image/jpeg", bytes.NewReader(data), -1, 1000); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed upload: %v", err)
		}
	}
	if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(t.Context(), e.PublicID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted event accepted a session")
	}
}

type failingStore struct {
	storage.Store
	failPut, failDelete bool
}

func (s *failingStore) Put(ctx context.Context, key string, r io.Reader, limit int64) (storage.StoredObject, error) {
	object, err := s.Store.Put(ctx, key, r, limit)
	if err == nil && s.failPut {
		return object, errors.New("simulated finalization failure")
	}
	return object, err
}
func (s *failingStore) Delete(ctx context.Context, key string) error {
	if s.failDelete {
		return errors.New("simulated disk cleanup failure")
	}
	return s.Store.Delete(ctx, key)
}

func TestFailureRecoveryAndPartialDeletion(t *testing.T) {
	s, dir, e, session := fixture(t)
	other := createEvent(t, s)
	otherSession, err := s.CreateSession(t.Context(), other.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	keep := upload(t, s, other, otherSession, "keep.png")
	failure := &failingStore{Store: s.storage, failPut: true, failDelete: true}
	s.storage = failure
	data := testutil.Images()["image/png"]
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "failed.png", "image/png", bytes.NewReader(data), -1, 1000); err == nil {
		t.Fatal("failure reported success")
	}
	if countAssets(t, s, "pending") != 1 {
		t.Fatal("cleanup failure lost recovery metadata")
	}
	if err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if countAssets(t, s, "pending") != 1 {
		t.Fatal("startup cleanup removed a recent attempt")
	}
	if _, err := s.db.Exec("UPDATE assets SET created_at = '2000-01-01T00:00:00Z' WHERE status = 'pending'"); err != nil {
		t.Fatal(err)
	}
	failure.failPut = false
	failure.failDelete = false
	if err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if countAssets(t, s, "pending") != 0 {
		t.Fatal("stale attempt remains")
	}
	asset := upload(t, s, e, session, "delete.png")
	failure.failDelete = true
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
		t.Fatalf("partial deletion: %v", err)
	}
	current, err := s.events.Get(t.Context(), e.ID)
	if err != nil || !current.Deleting || current.Enabled {
		t.Fatal("partial deletion did not persist closed state")
	}
	stats, _ := s.Stats(t.Context())
	if stats[e.ID].PhotoCount != 0 || stats[other.ID].PhotoCount != 1 {
		t.Fatal("deleting asset counted or unrelated event affected")
	}
	failure.failDelete = false
	if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", objectKey(e.ID, asset.ID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted event media remains")
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", objectKey(other.ID, keep.ID))); err != nil {
		t.Fatal("unrelated media was removed")
	}
	var sessions int
	s.db.QueryRow("SELECT COUNT(*) FROM upload_sessions WHERE event_id = ?", e.ID).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("deleted event session metadata remains")
	}
}

func TestCompletionDatabaseFailureAndMalformedKey(t *testing.T) {
	s, dir, e, session := fixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_ready BEFORE UPDATE OF status ON assets WHEN NEW.status = 'ready' BEGIN SELECT RAISE(ABORT, 'simulated failure'); END;`); err != nil {
		t.Fatal(err)
	}
	data := testutil.Images()["image/png"]
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "a.png", "image/png", bytes.NewReader(data), -1, 1000); err == nil {
		t.Fatal("database completion failure reported success")
	}
	if countAssets(t, s, "ready") != 0 || countAssets(t, s, "pending") != 0 {
		t.Fatal("completion failure not cleaned")
	}
	if files, _ := os.ReadDir(filepath.Join(dir, "uploads")); len(files) != 0 {
		t.Fatal("completion failure left final file")
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_ready"); err != nil {
		t.Fatal(err)
	}
	asset := upload(t, s, e, session, "a.png")
	other := createEvent(t, s)
	otherSession, _ := s.CreateSession(t.Context(), other.PublicID)
	keep := upload(t, s, other, otherSession, "keep.png")
	// A syntactically valid key in another event namespace must still be refused.
	if _, err := s.db.Exec("UPDATE assets SET storage_key = ? WHERE id = ?", objectKey(other.ID, asset.ID), asset.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEvent(t.Context(), e.ID); !errors.Is(err, ErrDeleting) {
		t.Fatal("malformed metadata permitted deletion")
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", objectKey(other.ID, keep.ID))); err != nil {
		t.Fatal("malformed key removed other event file")
	}
}

func TestConcurrentUploadsAndStats(t *testing.T) {
	s, _, e, session := fixture(t)
	data := testutil.Images()["image/png"]
	var group sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		group.Go(func() {
			_, err := s.Upload(t.Context(), e.PublicID, session.ID, "same.png", "image/png", bytes.NewReader(data), -1, 1000)
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
	stats, err := s.Stats(t.Context())
	if err != nil || stats[e.ID].PhotoCount != 12 || stats[e.ID].StorageBytes != int64(12*len(data)) {
		t.Fatalf("concurrent totals: %+v %v", stats, err)
	}
}
