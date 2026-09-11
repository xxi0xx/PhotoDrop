package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/web"
)

func TestAdminAndGuestWorkflow(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	const password = "server-workflow-test-password"
	const origin = "https://photos.example.com"
	srv, err := New(t.Context(), config.Config{ListenAddr: ":8080", DataDir: t.TempDir(), BaseURL: origin, AdminPassword: password}, db, assets, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, cookie *http.Cookie, csrf, source string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://untrusted-host:8080"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if source != "" {
			r.Header.Set("Origin", source)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("expected %d, got %d: %s", status, w.Code, w.Body)
		}
	}
	for _, path := range []string{"/admin", "/admin/events/new", "/admin/events/1"} {
		w := call("GET", path, "", nil, "", "")
		check(w, 303)
		if w.Header().Get("Location") != "/admin/login" {
			t.Fatal("admin page did not redirect to login")
		}
	}
	check(call("GET", "/admin/login", "", nil, "", ""), 200)
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/admin/session"}, {"GET", "/api/admin/events"}, {"POST", "/api/admin/events"}, {"GET", "/api/admin/events/1"}, {"PUT", "/api/admin/events/1"}, {"DELETE", "/api/admin/events/1"}, {"POST", "/api/admin/logout"}} {
		check(call(endpoint.method, endpoint.path, `{}`, nil, "", origin), 401)
	}
	check(call("POST", "/api/admin/login", `{"password":"wrong password"}`, nil, "", origin), 401)
	loginBody, _ := json.Marshal(map[string]string{"password": password})
	for _, source := range []string{"", "null", "https://evil.example", "https://photos.example.com.evil.test", "http://photos.example.com"} {
		check(call("POST", "/api/admin/login", string(loginBody), nil, "", source), 403)
	}
	w := call("POST", "/api/admin/login", string(loginBody), nil, "", origin)
	check(w, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != int(auth.SessionLifetime.Seconds()) {
		t.Fatalf("unsafe cookie settings: %s", cookie)
	}
	var session auth.Session
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.CSRFToken == "" {
		t.Fatal("missing CSRF token")
	}
	check(call("GET", "/admin", "", cookie, "", ""), 200)
	check(call("GET", "/api/admin/session", "", cookie, "", ""), 200)
	valid := `{"name":"  Wedding <script>alert(1)</script>  ","description":"Welcome ' OR 1=1; --","event_date":"2026-09-04","enabled":true,"expires_at":null}`
	for _, badCSRF := range []string{"", "wrong"} {
		check(call("POST", "/api/admin/events", valid, cookie, badCSRF, origin), 403)
	}
	check(call("POST", "/api/admin/events", valid, cookie, session.CSRFToken, "https://evil.example"), 403)
	for _, body := range []string{`{"name":"x","enabled":true,"public_id":"forced"}`, `{"name":"x","enabled":true,"id":7}`, valid + `{}`, `{`} {
		check(call("POST", "/api/admin/events", body, cookie, session.CSRFToken, origin), 400)
	}
	for _, body := range []string{`{"name":" ","enabled":true}`, `{"name":"x"}`, `{"name":"x","enabled":true,"event_date":"2026-02-30"}`, `{"name":"x","enabled":true,"expires_at":"not-a-time"}`} {
		w := call("POST", "/api/admin/events", body, cookie, session.CSRFToken, origin)
		check(w, 422)
		if !strings.Contains(w.Body.String(), "fields") {
			t.Fatal("validation omitted field errors")
		}
	}
	check(call("POST", "/api/admin/events", `{"name":"`+strings.Repeat("x", 33000)+`","enabled":true}`, cookie, session.CSRFToken, origin), 413)
	w = call("POST", "/api/admin/events", valid, cookie, session.CSRFToken, origin)
	check(w, 201)
	var created struct {
		Event adminEvent `json:"event"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	e := created.Event
	path := "/api/admin/events/" + strconv.FormatInt(e.ID, 10)
	if e.PublicURL != origin+"/e/"+e.PublicID || e.Name != "Wedding <script>alert(1)</script>" {
		t.Fatalf("incorrect public link or text: %+v", e)
	}
	w = call("GET", "/api/admin/events", "", cookie, "", "")
	check(w, 200)
	if !strings.Contains(w.Body.String(), e.PublicID) {
		t.Fatal("event missing from list")
	}
	check(call("GET", path, "", cookie, "", ""), 200)
	check(call("GET", "/e/"+e.PublicID, "", nil, "", ""), 200)
	w = call("GET", "/api/public/events/"+e.PublicID, "", nil, "", "")
	check(w, 200)
	var public struct {
		Event map[string]any `json:"event"`
	}
	json.Unmarshal(w.Body.Bytes(), &public)
	if public.Event["status"] != "open" || public.Event["name"] != e.Name {
		t.Fatal("open guest event not returned")
	}
	for _, field := range []string{"id", "public_id", "enabled", "expires_at", "created_at", "updated_at", "csrf_token", "public_url"} {
		if _, ok := public.Event[field]; ok {
			t.Fatalf("public response exposes %s", field)
		}
	}
	for _, update := range []string{`{"name":"Closed wedding","description":"private while closed","enabled":false}`, `{"name":"Closed wedding","enabled":true,"expires_at":"2000-01-01T00:00:00Z"}`} {
		w = call("PUT", path, update, cookie, session.CSRFToken, origin)
		check(w, 200)
		if !strings.Contains(w.Body.String(), e.PublicID) {
			t.Fatal("public ID changed on edit")
		}
		check(call("GET", "/e/"+e.PublicID, "", nil, "", ""), 200)
		w = call("GET", "/api/public/events/"+e.PublicID, "", nil, "", "")
		check(w, 200)
		if !strings.Contains(w.Body.String(), `"status":"closed"`) || strings.Contains(w.Body.String(), "private while closed") {
			t.Fatal("closed event leaked details or showed open")
		}
	}
	check(call("PUT", path, valid, cookie, session.CSRFToken, origin), 200)
	check(call("GET", "/e/"+strconv.FormatInt(e.ID, 10), "", nil, "", ""), 404)
	check(call("GET", "/e/AAAAAAAAAAAAAAAAAAAAAAAA", "", nil, "", ""), 404)
	check(call("GET", "/api/public/events/AAAAAAAAAAAAAAAAAAAAAAAA", "", nil, "", ""), 404)
	check(call("GET", "/api/admin/events/999999", "", cookie, "", ""), 404)
	check(call("PUT", "/api/admin/events/999999", valid, cookie, session.CSRFToken, origin), 404)
	check(call("DELETE", path, "", cookie, session.CSRFToken, origin), 204)
	check(call("DELETE", path, "", cookie, session.CSRFToken, origin), 404)
	check(call("GET", "/e/"+e.PublicID, "", nil, "", ""), 404)
	w = call("POST", "/api/admin/logout", "", cookie, session.CSRFToken, origin)
	check(w, 204)
	if w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	check(call("GET", "/api/admin/events", "", cookie, "", ""), 401)
	check(call("GET", "/admin", "", cookie, "", ""), 303)
	w = call("POST", "/api/admin/login", string(loginBody), nil, "", origin)
	check(w, 200)
	cookie = w.Result().Cookies()[0]
	if _, err := db.Exec("UPDATE admin_sessions SET expires_at = ?", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	check(call("GET", "/api/admin/events", "", cookie, "", ""), 401)
	for _, secret := range []string{password, cookie.Value, session.CSRFToken} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret appeared in application logs")
		}
	}
	db.Close()
	w = call("GET", "/api/public/events/"+e.PublicID, "", nil, "", "")
	check(w, 500)
	if strings.Contains(w.Body.String(), "database") || strings.Contains(w.Body.String(), "sql") {
		t.Fatal("database error leaked to client")
	}
}

func TestOriginFallbackAndHTTPCookie(t *testing.T) {
	a := &application{}
	for _, tt := range []struct {
		origin, referer, site string
		accepted              bool
	}{
		{"http://localhost:8080", "", "same-origin", true},
		{"", "http://localhost:8080/admin", "", true},
		{"", "", "", false},
		{"null", "http://localhost:8080/admin", "", false},
		{"http://localhost:8080", "", "cross-site", false},
		{"", "http://localhost:8080@evil.test/admin", "", false},
	} {
		r := httptest.NewRequest("POST", "http://localhost:8080/api/admin/login", nil)
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("Referer", tt.referer)
		r.Header.Set("Sec-Fetch-Site", tt.site)
		if a.sameOrigin(httptest.NewRecorder(), r) != tt.accepted {
			t.Fatalf("unexpected origin decision: %+v", tt)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost:8080", nil)
	a.setCookie(w, r, "token", time.Now().Add(time.Hour), 3600)
	if w.Result().Cookies()[0].Secure {
		t.Fatal("local HTTP cookie unexpectedly requires HTTPS")
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "https://localhost", nil)
	a.setCookie(w, r, "token", time.Now().Add(time.Hour), 3600)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("direct TLS cookie is not Secure")
	}
}
