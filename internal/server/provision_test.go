package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/integrations/immich"
	"photodrop/web"
)

func TestEventProvisioningAPI(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer remote.Close()
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
	cfg := config.Config{DataDir: dir, BaseURL: "http://drop.test", AdminPassword: "provision-test-admin-password", ImmichTarget: "home", ImmichTargets: map[string]config.Immich{
		"home":    {URL: remote.URL, APIKey: "test-only-private-key", PublicURL: "https://photos.example.test"},
		"offline": {URL: "http://offline.invalid"},
	}}
	srv, err := New(t.Context(), cfg, db, assets, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	var token string
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, cfg.BaseURL+path, strings.NewReader(body))
		r.Header.Set("Origin", cfg.BaseURL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", token)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		return w
	}
	login := call("POST", "/api/admin/login", `{"password":"provision-test-admin-password"}`)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie = login.Result().Cookies()[0]
	var session auth.Session
	json.Unmarshal(login.Body.Bytes(), &session)
	token = session.CSRFToken
	catalog := call("GET", "/api/admin/immich/targets", "")
	var targets immich.TargetCatalog
	if err := json.Unmarshal(catalog.Body.Bytes(), &targets); err != nil || catalog.Code != 200 || len(targets.Targets) != 2 || targets.ActiveTarget != "home" {
		t.Fatal(catalog.Body.String(), err)
	}
	if strings.Contains(catalog.Body.String(), remote.URL) || strings.Contains(catalog.Body.String(), "private-key") || strings.Contains(catalog.Body.String(), "offline.invalid") {
		t.Fatal("unsafe discovery", catalog.Body.String())
	}
	for _, setup := range []string{"", `,"immich":{"target":"home","album_name":"Early album","auto_import":true}`, `,"immich":{"target":"offline","album_name":"Unavailable"}`} {
		started := time.Now()
		w := call("POST", "/api/admin/events", `{"name":"Event","enabled":true`+setup+`}`)
		if w.Code != 201 || time.Since(started) > time.Second || calls.Load() != 0 {
			t.Fatal("creation depended on Immich", w.Code, w.Body.String())
		}
		var result struct {
			Event events.Event `json:"event"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w := call("GET", "/e/"+result.Event.PublicID, ""); w.Code != 200 {
			t.Fatal("guest page unavailable", w.Code)
		}
		if w := call("POST", "/api/public/events/"+result.Event.PublicID+"/upload-sessions", "{}"); w.Code != 201 {
			t.Fatal("guest grant unavailable", w.Body.String())
		}
	}
	var jobs, bindings int
	db.QueryRow("SELECT count(*) FROM integration_jobs").Scan(&jobs)
	db.QueryRow("SELECT count(*) FROM immich_event_imports").Scan(&bindings)
	if jobs != 2 || bindings != 2 {
		t.Fatal("event setup not queued", jobs, bindings)
	}
	var automatic bool
	if err := db.QueryRow("SELECT auto_import FROM immich_event_imports WHERE event_id=2").Scan(&automatic); err != nil || !automatic {
		t.Fatal("creation lost auto setting", err)
	}
	for _, enabled := range []string{"false", "true"} {
		if w := call("PUT", "/api/admin/events/2/immich/auto-import", `{"target":"home","auto_import":`+enabled+`}`); w.Code != 204 {
			t.Fatal(w.Body.String())
		}
		var changed immich.Status
		w := call("GET", "/api/admin/events/2/immich", "")
		if err := json.Unmarshal(w.Body.Bytes(), &changed); err != nil || changed.AutoImport != (enabled == "true") {
			t.Fatal(w.Body.String(), err)
		}
	}
	if w := call("PUT", "/api/admin/events/1/immich/auto-import", `{"target":"home","auto_import":true}`); w.Code != 404 {
		t.Fatal("enabled absent binding", w.Code)
	}
	if w := call("PUT", "/api/admin/events/2/immich/auto-import", `{"target":"home"}`); w.Code != 422 {
		t.Fatal("absent policy accepted", w.Code)
	}
	// The selected non-default binding must be shown after the creation redirect.
	w := call("GET", "/api/admin/events/3/immich", "")
	var status immich.Status
	json.Unmarshal(w.Body.Bytes(), &status)
	if status.Target == nil || status.Target.Key != "offline" || status.Target.Available || status.AutoImport || status.Job.Status != "queued" {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "/api/admin/events/1/immich/album", `{"target":"home","album_name":"Existing empty event"}`); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if w := call("POST", "/api/admin/events", `{"name":"Bad","enabled":true,"immich":{"target":"missing"}}`); w.Code < 400 {
		t.Fatal("unknown target accepted")
	}
	if w := call("POST", "/api/admin/events", `{"name":"Bad","enabled":true,"immich":{"target":"home","album_name":"bad\nname"}}`); w.Code != 422 {
		t.Fatal(w.Body.String())
	}
	var count int
	db.QueryRow("SELECT count(*) FROM events").Scan(&count)
	if count != 3 {
		t.Fatal("invalid setup leaked event", count)
	}
}
