package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/media"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/s3test"
	"photodrop/web"
)

type countedBody struct {
	io.ReadCloser
	bytes int
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes += n
	return n, err
}

func TestDirectUploadHTTPDataPathAndIsolation(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects := s3test.New("http://localhost:8080")
	remote := httptest.NewServer(objects)
	defer remote.Close()
	cfg := config.Config{DataDir: dir, AdminPassword: "s3-test-admin-password", MaxFileSize: 256 * 1024, StorageProvider: "s3", S3: config.S3{Bucket: "photos", Region: s3test.Region, Endpoint: remote.URL, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PathStyle: true, Prefix: "test/", PresignTTL: time.Minute}}
	cfg.StorageBackendKey = "test-store"
	cfg.S3Backends = map[string]config.S3{"test-store": cfg.S3}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	assets, _ := web.Assets()
	srv, err := New(t.Context(), cfg, db, assets, logger)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	store := events.New(db)
	e, err := store.Create(t.Context(), events.Input{Name: "Direct test", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/public/events/" + e.PublicID + "/upload-sessions"
	call := func(method, path, origin string, body []byte, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost:8080"+path, bytes.NewReader(body))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		spy := &countedBody{ReadCloser: r.Body}
		r.Body = spy
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if spy.bytes > 4097 {
			t.Fatal("control plane consumed a media-sized body")
		}
		if strings.Contains(w.Body.String(), s3test.SecretKey) || strings.Contains(w.Body.String(), "storage_target") {
			t.Fatal("private metadata leaked")
		}
		return w
	}
	origin := "http://localhost:8080"
	w := call("POST", base, origin, []byte("{}"), 201)
	var session struct {
		Session  media.Session `json:"upload_session"`
		Strategy string        `json:"upload_strategy"`
	}
	json.Unmarshal(w.Body.Bytes(), &session)
	if session.Strategy != "direct" {
		t.Fatal("missing capability")
	}
	path := base + "/" + session.Session.ID + "/assets"
	// A real JPEG with padding makes the media body much larger than any control request.
	data := append(testutil.Images()["image/jpeg"], make([]byte, 64*1024)...)
	input := media.Preparation{Filename: "../../same.jpg", Size: int64(len(data)), ContentType: "image/jpeg", RequestID: strings.Repeat("a", 32)}
	body, _ := json.Marshal(input)
	call("POST", path+"/prepare", "https://evil.test", body, 403)
	call("POST", path+"/prepare", origin, []byte(`{"filename":"x","bucket":"chosen-by-guest"}`), 400)
	call("POST", path+"/prepare", origin, bytes.Repeat([]byte("x"), 5000), 400)
	w = call("POST", path+"/prepare", origin, body, 201)
	var prepared media.Prepared
	if err := json.Unmarshal(w.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self' "+remote.URL) {
		t.Fatal("direct PUT origin blocked by CSP")
	}
	var pending, ready int
	db.QueryRow("SELECT COUNT(*) FROM assets WHERE status='pending'").Scan(&pending)
	if pending != 1 {
		t.Fatal("pending row not created")
	}
	r, _ := http.NewRequest(prepared.Upload.Method, prepared.Upload.URL, bytes.NewReader(data))
	for k, v := range prepared.Upload.Headers {
		r.Header.Set(k, v)
	}
	r.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatal("direct PUT or CORS failed")
	}
	db.QueryRow("SELECT COUNT(*) FROM assets WHERE status='ready'").Scan(&ready)
	if ready != 0 {
		t.Fatal("browser success made asset ready")
	}
	complete := path + "/" + prepared.Asset.ID + "/complete"
	call("POST", complete, origin, []byte("{}"), 200)
	call("POST", complete, origin, []byte("{}"), 200)
	call("POST", path+"/"+prepared.Asset.ID+"/authorize", origin, []byte("{}"), 409)
	call("GET", complete, origin, nil, 405)
	call("POST", strings.Replace(complete, session.Session.ID, strings.Repeat("b", 32), 1), origin, []byte("{}"), 404)
	db.QueryRow("SELECT COUNT(*) FROM assets WHERE status='ready'").Scan(&ready)
	if ready != 1 {
		t.Fatal("completion duplicated asset")
	}
	foundPut, foundRange := false, false
	for _, request := range objects.Requests() {
		if request.Method == "PUT" && request.Bytes == len(data) {
			foundPut = true
		}
		if request.Method == "GET" && request.Range == "bytes=0-511" {
			foundRange = true
		}
	}
	if !foundPut || !foundRange {
		t.Fatal("image did not follow direct PUT / ranged verification path")
	}
	// Raw local upload bodies are rejected in direct mode before being consumed.
	raw := httptest.NewRequest("POST", "http://localhost:8080"+path, bytes.NewReader(data))
	raw.Header.Set("Origin", origin)
	raw.Header.Set("Content-Type", "image/jpeg")
	raw.Header.Set("Content-Disposition", "attachment; filename=a.jpg")
	spy := &countedBody{ReadCloser: raw.Body}
	raw.Body = spy
	out := httptest.NewRecorder()
	srv.Handler.ServeHTTP(out, raw)
	if out.Code != 409 || spy.bytes != 0 {
		t.Fatal("S3 mode accepted media through PhotoDrop")
	}
	for _, secret := range []string{s3test.SecretKey, s3test.AccessKey, "X-Amz-", "Signature=", remote.URL} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("normal logs leaked object-store authorization")
		}
	}
	// Historical metadata without credentials must not take browsing/health down.
	cfg.StorageProvider = "local"
	cfg.S3Backends = nil
	restarted, err := New(t.Context(), cfg, db, assets, logger)
	if err != nil {
		t.Fatal("missing historical credentials blocked startup", err)
	}
	health := httptest.NewRecorder()
	restarted.Handler.ServeHTTP(health, httptest.NewRequest("GET", "http://localhost:8080/healthz", nil))
	if health.Code != 200 {
		t.Fatal("missing historical credentials broke health")
	}
	changed := cfg.S3
	changed.Bucket = "wrong-bucket"
	cfg.S3Backends = map[string]config.S3{"test-store": changed}
	if _, err := New(t.Context(), cfg, db, assets, logger); err == nil || !strings.Contains(err.Error(), "choose a new") {
		t.Fatal("startup allowed destination substitution", err)
	}
}
