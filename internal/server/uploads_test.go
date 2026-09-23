package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/media"
	"photodrop/internal/testutil"
	"photodrop/web"
)

func TestAnonymousUploadHTTP(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: dir, AdminPassword: "upload-api-test-password", MaxFileSize: 1024}
	srv, err := New(t.Context(), cfg, db, assets, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	eventsStore := events.New(db)
	e, err := eventsStore.Create(t.Context(), events.Input{Name: "Upload API", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	other, err := eventsStore.Create(t.Context(), events.Input{Name: "Other event", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	const origin = "http://localhost:8080"
	call := func(path string, body io.Reader, filename, contentType string, length int64, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", origin+path, body)
		r.ContentLength = length
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", contentType)
		if filename != "" {
			r.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		}
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: got %d, want %d: %s", path, w.Code, status, w.Body.String())
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("upload response headers")
		}
		if status >= 400 && (strings.Contains(w.Body.String(), dir) || strings.Contains(w.Body.String(), "SQL") || strings.Contains(w.Body.String(), "storage_key")) {
			t.Fatal("internal upload details exposed")
		}
		return w
	}
	sessionPath := "/api/public/events/" + e.PublicID + "/upload-sessions"
	for _, name := range []string{strings.Repeat("界", 101), "a\x00b", "a\nb"} {
		body, _ := json.Marshal(map[string]string{"contributor_name": name})
		call(sessionPath, bytes.NewReader(body), "", "application/json", int64(len(body)), 422)
	}
	const contributor = "<img src=x onerror=alert(1)> José"
	body, _ := json.Marshal(map[string]string{"contributor_name": contributor})
	w := call(sessionPath, bytes.NewReader(body), "", "application/json", int64(len(body)), 201)
	var session struct {
		UploadSession media.Session `json:"upload_session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	path := sessionPath + "/" + session.UploadSession.ID + "/assets"
	var attribution string
	if err := db.QueryRow("SELECT contributor_name FROM upload_sessions WHERE id=?", session.UploadSession.ID).Scan(&attribution); err != nil || attribution != contributor {
		t.Fatal("session attribution", err)
	}
	data := testutil.Images()["image/png"]
	for _, filename := range []string{"../../etc/passwd", `C:\temp\file.jpg`, "<script>alert(1)</script>.jpg"} {
		w = call(path, bytes.NewReader(data), filename, "image/png", -1, 201)
		var response struct {
			Asset media.Asset `json:"asset"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Asset.Filename != filename || response.Asset.Status != "ready" || response.Asset.Size != int64(len(data)) {
			t.Fatal("guest metadata mismatch")
		}
		for _, private := range []string{"event_id", "storage_key", "upload_session_id", "created_at", "completed_at", "contributor_name"} {
			if strings.Contains(w.Body.String(), `"`+private+`"`) {
				t.Fatalf("public response exposed %s", private)
			}
		}
	}
	call(path, strings.NewReader("not an image"), "disguised.jpg", "image/jpeg", -1, 415)
	call(path, bytes.NewReader(data), "a.svg", "image/svg+xml", -1, 415)
	call(path, bytes.NewReader(nil), "empty.jpg", "image/jpeg", 0, 422)
	call(path, bytes.NewReader(data), "", "image/png", -1, 422)
	call(path, bytes.NewReader(data), strings.Repeat("x", 256), "image/png", -1, 422)
	call(path, bytes.NewReader(data), "a.png", "image/png", 1025, 413)
	large := &countingReader{reader: io.MultiReader(bytes.NewReader(data), io.LimitReader(zeroReader{}, 10000))}
	call(path, large, "chunked.png", "image/png", -1, 413)
	if large.read > 1025 {
		t.Fatalf("body limit did not bound reading: %d", large.read)
	}
	call("/api/public/events/"+other.PublicID+"/upload-sessions/"+session.UploadSession.ID+"/assets", bytes.NewReader(data), "a.png", "image/png", -1, 404)
	call(sessionPath+"/invalid/assets", bytes.NewReader(data), "a.png", "image/png", -1, 404)
	call("/api/public/events/"+strconv.FormatInt(e.ID, 10)+"/upload-sessions", strings.NewReader("{}"), "", "application/json", 2, 404)
	call("/api/public/events/AAAAAAAAAAAAAAAAAAAAAAAA/upload-sessions", strings.NewReader("{}"), "", "application/json", 2, 404)
	if _, err := db.Exec("UPDATE events SET enabled = 0 WHERE id = ?", e.ID); err != nil {
		t.Fatal(err)
	}
	call(path, bytes.NewReader(data), "a.png", "image/png", -1, 409)
	call(sessionPath, strings.NewReader("{}"), "", "application/json", 2, 409)
	if _, err := db.Exec("UPDATE events SET enabled = 1, expires_at = '2000-01-01T00:00:00Z' WHERE id = ?", e.ID); err != nil {
		t.Fatal(err)
	}
	call(path, bytes.NewReader(data), "a.png", "image/png", -1, 409)
	call(sessionPath, strings.NewReader("{}"), "", "application/json", 2, 409)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM assets WHERE status = 'ready'").Scan(&count); err != nil || count != 3 {
		t.Fatalf("successful/failed asset totals: %d %v", count, err)
	}
	// Storage is private: knowing an object name creates no download endpoint.
	var key string
	if err := db.QueryRow("SELECT storage_key FROM assets LIMIT 1").Scan(&key); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/uploads/" + key, "/data/uploads/" + key} {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest("GET", origin+url, nil))
		if rec.Code != 404 {
			t.Fatal("media file is publicly served")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "uploads", key)); err != nil {
		t.Fatal("ready object missing")
	}
	// An unreadable body never becomes ready and receives a safe error.
	if _, err := db.Exec("UPDATE events SET expires_at = NULL WHERE id = ?", e.ID); err != nil {
		t.Fatal(err)
	}
	call(path, failingReader{}, "a.png", "image/png", -1, 500)
	if err := db.QueryRow("SELECT COUNT(*) FROM assets WHERE status = 'pending'").Scan(&count); err != nil || count != 0 {
		t.Fatal("interrupted HTTP upload left pending data")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("simulated connection failure") }

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	readDeadline, writeDeadline time.Time
}

func (w *deadlineRecorder) SetReadDeadline(t time.Time) error  { w.readDeadline = t; return nil }
func (w *deadlineRecorder) SetWriteDeadline(t time.Time) error { w.writeDeadline = t; return nil }

func TestUploadSpecificDeadlinesAndOrigin(t *testing.T) {
	a := &application{maxFileSize: 1000}
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	r := httptest.NewRequest("POST", "http://localhost/upload", nil)
	r.Header.Set("Origin", "http://localhost")
	a.uploadAsset(w, r) // Rejects missing filename after setting deadlines.
	if w.Code != 422 || time.Until(w.readDeadline) < 9*time.Minute || time.Until(w.writeDeadline) < 9*time.Minute {
		t.Fatal("upload retained short HTTP deadlines")
	}
	r.Header.Set("Origin", "https://other.example")
	rec := httptest.NewRecorder()
	a.uploadAsset(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-origin upload accepted")
	}
}
