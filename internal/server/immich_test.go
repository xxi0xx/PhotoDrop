package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/web"
)

func TestImmichAdminBoundaryAndHealth(t *testing.T) {
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
	cfg := config.Config{DataDir: dir, BaseURL: "http://photos.test", AdminPassword: "admin-integration-test-password", ImmichTarget: "missing", ImmichTargets: map[string]config.Immich{"missing": {URL: "http://127.0.0.1:1"}}}
	srv, err := New(t.Context(), cfg, db, assets, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("optional integration blocked startup", err)
	}
	enabled := true
	if _, err := events.New(db).Create(t.Context(), events.Input{Name: "Test", Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://photos.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		return w
	}
	const path = "/api/admin/events/1/immich"
	for _, route := range []struct{ method, path string }{{"GET", path}, {"POST", path + "/test"}, {"POST", path + "/jobs"}, {"POST", path + "/cancel"}} {
		if w := call(route.method, route.path, "{}", nil, "", "http://photos.test"); w.Code != 401 {
			t.Fatal("anonymous integration access", w.Code)
		}
	}
	w := call("POST", "/api/admin/login", `{"password":"admin-integration-test-password"}`, nil, "", "http://photos.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	var session auth.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"/test", "/jobs", "/cancel"} {
		if w := call("POST", path+suffix, "{}", cookie, "", "http://photos.test"); w.Code != 403 {
			t.Fatal("missing CSRF accepted")
		}
		if w := call("POST", path+suffix, "{}", cookie, session.CSRFToken, "https://foreign.test"); w.Code != 403 {
			t.Fatal("foreign origin accepted")
		}
		if w := call("POST", path+suffix, `{"url":"http://internal-target"}`, cookie, session.CSRFToken, "http://photos.test"); w.Code != 400 {
			t.Fatal("URL accepted from browser")
		}
	}
	w = call("GET", path, "", cookie, "", "http://photos.test")
	if w.Code != 200 || strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), "API_KEY") {
		t.Fatal("status exposed internals", w.Body.String())
	}
	if w := call("POST", path+"/test", "{}", cookie, session.CSRFToken, "http://photos.test"); w.Code != 503 {
		t.Fatal("missing credentials silently passed")
	}
	if w := call("GET", "/healthz", "", nil, "", ""); w.Code != 200 {
		t.Fatal("integration changed health")
	}
	if w := call("POST", path+"/jobs", `{"mode":"new","album_name":"`+strings.Repeat("x", 5000)+`"}`, cookie, session.CSRFToken, "http://photos.test"); w.Code != 413 {
		t.Fatal("unbounded import request")
	}
}
