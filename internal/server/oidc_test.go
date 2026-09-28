package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/oidctest"
	"photodrop/web"
)

type oidcHTTPFixture struct {
	t    *testing.T
	db   *sql.DB
	cfg  config.Config
	p    *oidctest.Provider
	srv  *http.Server
	logs bytes.Buffer
}

func newOIDCHTTP(t *testing.T, mode string) *oidcHTTPFixture {
	t.Helper()
	p, err := oidctest.New("")
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(p)
	t.Cleanup(remote.Close)
	p.Issuer = remote.URL + "/application/o/drop/"
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &oidcHTTPFixture{t: t, db: db, p: p, cfg: config.Config{DataDir: dir, BaseURL: "https://drop.test", AdminAuth: mode, Security: config.Security{RateDisabled: true}, OIDC: config.OIDC{Issuer: p.Issuer, ClientID: oidctest.ClientID, ClientSecret: oidctest.Secret, AllowedGroups: []string{"photodrop-admins"}, GroupsClaim: "groups", Scopes: []string{"openid", "profile", "email"}}}}
	if mode != "oidc" {
		f.cfg.AdminPassword = "existing-test-password"
	}
	f.restart()
	return f
}
func (f *oidcHTTPFixture) restart() {
	f.t.Helper()
	assets, err := web.Assets()
	if err != nil {
		f.t.Fatal(err)
	}
	srv, err := New(f.t.Context(), f.cfg, f.db, assets, slog.New(slog.NewTextHandler(&f.logs, nil)))
	if err != nil {
		f.t.Fatal(err)
	}
	f.srv = srv
}
func (f *oidcHTTPFixture) call(method, path, body, csrf, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, "http://attacker-host.invalid"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "http")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.srv.Handler.ServeHTTP(w, r)
	return w
}
func findCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing %s cookie", name)
	return nil
}
func (f *oidcHTTPFixture) authorize() (string, *http.Cookie) {
	f.t.Helper()
	w := f.call("GET", "/api/admin/oidc/login", "", "", "")
	if w.Code != 303 {
		f.t.Fatal(w.Code)
	}
	c := findCookie(f.t, w, oidcCookie)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/api/admin/oidc" || c.MaxAge != 300 || c.Domain != "" {
		f.t.Fatal("unsafe binding cookie")
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	if u.Query().Get("redirect_uri") != "https://drop.test"+auth.OIDCCallbackPath {
		f.t.Fatal("Host/forwarded headers changed callback")
	}
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Get(u.String())
	if err != nil {
		f.t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 303 {
		f.t.Fatal("authorization failed", r.StatusCode)
	}
	callback, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	return callback.RequestURI(), c
}
func (f *oidcHTTPFixture) password() *http.Cookie {
	f.t.Helper()
	w := f.call("POST", "/api/admin/login", `{"password":"existing-test-password"}`, "", f.cfg.BaseURL)
	if w.Code != 200 {
		f.t.Fatal("password login", w.Code, w.Body)
	}
	return findCookie(f.t, w, sessionCookie)
}
func TestOIDCHTTPFlowAndLocalSession(t *testing.T) {
	f := newOIDCHTTP(t, "password+oidc")
	old := f.password()
	callback, binding := f.authorize()
	w := f.call("GET", callback+"&return_to=https://evil.example", "", "", "", binding, old)
	if w.Code != 303 || w.Header().Get("Location") != "/admin" {
		t.Fatal("OIDC login", w.Code, w.Header())
	}
	c := findCookie(t, w, sessionCookie)
	if c.Value == old.Value || len(c.Value) != 43 || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 43200 {
		t.Fatal("unsafe/fixed session")
	}
	if findCookie(t, w, oidcCookie).MaxAge != -1 {
		t.Fatal("binding cookie retained")
	}
	if f.call("GET", "/api/admin/session", "", "", "", old).Code != 401 {
		t.Fatal("previous session retained")
	}
	var session auth.Session
	w = f.call("GET", "/api/admin/session", "", "", "", c)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil {
		t.Fatal("missing local session")
	}
	if f.call("POST", "/api/admin/events", `{"name":"CSRF test","enabled":true}`, "", f.cfg.BaseURL, c).Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if f.call("POST", "/api/admin/events", `{"name":"CSRF test","enabled":true}`, session.CSRFToken, "https://evil.test", c).Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	requests, _, _ := f.p.Counts()
	f.p.SetFaults(oidctest.Faults{Unavailable: true})
	if f.call("POST", "/api/admin/events", `{"name":"OIDC event","enabled":true}`, session.CSRFToken, f.cfg.BaseURL, c).Code != 201 {
		t.Fatal("provider outage broke admin mutation")
	}
	for _, path := range []string{"/admin", "/api/admin/session", "/api/admin/events", "/healthz"} {
		if f.call("GET", path, "", "", "", c).Code != 200 {
			t.Fatal("provider outage broke local session", path)
		}
	}
	if n, _, _ := f.p.Counts(); n != requests {
		t.Fatal("session lookup contacted provider")
	}
	if f.call("POST", "/api/admin/logout", "{}", session.CSRFToken, f.cfg.BaseURL, c).Code != 204 || f.call("GET", "/api/admin/session", "", "", "", c).Code != 401 {
		t.Fatal("local logout failed")
	}
	if n, _, _ := f.p.Counts(); n != requests {
		t.Fatal("logout contacted provider")
	}
	if f.call("GET", callback, "", "", "", binding).Header().Get("Location") != "/admin/login?error=oidc" {
		t.Fatal("callback replay accepted")
	}
	for _, secret := range []string{oidctest.Secret, "test-access-token", "test-refresh-token", callback, binding.Value, c.Value} {
		if strings.Contains(f.logs.String(), secret) {
			t.Fatal("sensitive log")
		}
	}
	var stored string
	rows, err := f.db.Query("SELECT token_hash FROM admin_sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		rows.Scan(&stored)
		if stored == c.Value || len(stored) != 64 {
			t.Fatal("raw token persisted")
		}
	}
}
func TestOIDCOutageAtStartupLeavesGuestUsable(t *testing.T) {
	for _, mode := range []string{"oidc", "password+oidc"} {
		t.Run(mode, func(t *testing.T) {
			f := newOIDCHTTP(t, mode)
			f.p.SetFaults(oidctest.Faults{Unavailable: true})
			f.restart()
			if n, _, _ := f.p.Counts(); n != 0 {
				t.Fatal("startup discovery")
			}
			if mode == "password+oidc" {
				f.password()
			} else {
				if f.call("POST", "/api/admin/login", `{"password":"existing-test-password"}`, "", f.cfg.BaseURL).Code != 403 {
					t.Fatal("password enabled")
				}
			}
			if f.call("GET", "/healthz", "", "", "").Code != 200 {
				t.Fatal("unhealthy")
			}
			on := true
			e, err := events.New(f.db).Create(t.Context(), events.Input{Name: "Public during outage", Enabled: &on})
			if err != nil {
				t.Fatal(err)
			}
			if f.call("GET", "/e/"+e.PublicID, "", "", "").Code != 200 {
				t.Fatal("public page blocked")
			}
			w := f.call("POST", "/api/public/events/"+e.PublicID+"/upload-sessions", "{}", "", f.cfg.BaseURL)
			var grant struct {
				Session struct {
					ID string `json:"id"`
				} `json:"upload_session"`
			}
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &grant) != nil {
				t.Fatal("guest grant blocked", w.Code)
			}
			data := testutil.Images()["image/png"]
			r := httptest.NewRequest("POST", "https://drop.test/api/public/events/"+e.PublicID+"/upload-sessions/"+grant.Session.ID+"/assets", bytes.NewReader(data))
			r.Header.Set("Origin", f.cfg.BaseURL)
			r.Header.Set("Content-Type", "image/png")
			r.Header.Set("Content-Disposition", `attachment; filename=photo.png`)
			w = httptest.NewRecorder()
			f.srv.Handler.ServeHTTP(w, r)
			if w.Code != 201 {
				t.Fatal("guest upload blocked", w.Code, w.Body)
			}
			w = f.call("GET", "/api/admin/oidc/login", "", "", "")
			if w.Header().Get("Location") != "/admin/login?error=oidc" {
				t.Fatal("unsafe outage failure")
			}
			if f.call("GET", "/healthz", "", "", "").Code != 200 {
				t.Fatal("login failure changed health")
			}
			f.p.SetFaults(oidctest.Faults{})
			callback, c := f.authorize()
			if f.call("GET", callback, "", "", "", c).Header().Get("Location") != "/admin" {
				t.Fatal("transient discovery failure cached")
			}
		})
	}
}
func TestAuthModesPreserveExistingSessions(t *testing.T) {
	f := newOIDCHTTP(t, "")
	c := f.password()
	var before, after string
	f.db.QueryRow("SELECT password_hash FROM admin_credential").Scan(&before)
	if cost, err := bcrypt.Cost([]byte(before)); err != nil || cost != 12 {
		t.Fatal("bcrypt cost", cost, err)
	}
	for _, mode := range []string{"", "password+oidc", "oidc"} {
		f.cfg.AdminAuth = mode
		if mode == "oidc" {
			f.cfg.AdminPassword = ""
		}
		f.restart()
		f.db.QueryRow("SELECT password_hash FROM admin_credential").Scan(&after)
		if before != after {
			t.Fatal("unexpected credential rotation")
		}
		if f.call("GET", "/api/admin/session", "", "", "", c).Code != 200 {
			t.Fatal("mode/restart lost session")
		}
		w := f.call("GET", "/api/admin/auth/methods", "", "", "")
		var methods map[string]bool
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &methods) != nil || len(methods) != 2 || methods["password"] != (mode != "oidc") || methods["oidc"] != (mode != "") {
			t.Fatal("methods leaked or wrong", w.Body)
		}
		if mode == "oidc" {
			if f.call("POST", "/api/admin/login", `{"password":"existing-test-password"}`, "", f.cfg.BaseURL).Code != 403 {
				t.Fatal("historical password accepted")
			}
		} else {
			f.password()
		}
	}
	f.cfg.AdminAuth = "password+oidc"
	f.cfg.AdminPassword = "rotated-admin-password"
	f.restart()
	if f.call("GET", "/api/admin/session", "", "", "", c).Code != 401 {
		t.Fatal("password rotation did not revoke old session")
	}
}
func TestOIDCHTTPFailureAndDisabledRoutes(t *testing.T) {
	f := newOIDCHTTP(t, "password")
	if f.call("GET", "/api/admin/oidc/login", "", "", "").Code != 404 {
		t.Fatal("OIDC enabled by residual config")
	}
	f.cfg.AdminAuth = "oidc"
	f.restart()
	for _, method := range []string{"POST", "HEAD", "PUT"} {
		if f.call(method, "/api/admin/oidc/login", "", "", "").Code != 405 {
			t.Fatal("unsafe method", method)
		}
	}
	callback, c := f.authorize()
	for _, path := range []string{callback, callback + "&state=duplicate", callback + "&iss=wrong", auth.OIDCCallbackPath + "?state=bad&code=secret&error_description=secret"} {
		w := f.call("GET", path, "", "", "") // no browser binding
		if w.Header().Get("Location") != "/admin/login?error=oidc" {
			t.Fatal("unbound/invalid callback accepted")
		}
	}
	f.p.SetFaults(oidctest.Faults{Nonce: "private-wrong-nonce"})
	w := f.call("GET", callback, "", "", "", c)
	if w.Header().Get("Location") != "/admin/login?error=oidc" || strings.Contains(w.Body.String(), "private") {
		t.Fatal("unsafe provider failure")
	}
}
