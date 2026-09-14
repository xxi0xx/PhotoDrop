package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"photodrop/internal/abuse"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/media"
	"photodrop/internal/testutil"
	"photodrop/web"
	"strings"
	"sync"
	"testing"
	"time"
)

type verifierFunc func(context.Context, string) (abuse.Verification, error)

func (f verifierFunc) Verify(ctx context.Context, token string) (abuse.Verification, error) {
	return f(ctx, token)
}

func TestTurnstileSessionBoundaryHeadersAndBodies(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			dir := t.TempDir()
			db, err := database.Open(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cfg := config.Config{DataDir: dir, BaseURL: "https://photos.test", AdminPassword: "security-test-password"}
			if enabled {
				cfg.Security = config.Security{TurnstileSiteKey: "public-site", TurnstileSecretKey: "private-secret"}
			}
			calls := 0
			validUsed := false
			verifier := verifierFunc(func(ctx context.Context, token string) (abuse.Verification, error) {
				calls++
				switch token {
				case "valid-private-token":
					if validUsed {
						return abuse.Verification{Codes: []string{"timeout-or-duplicate"}}, nil
					}
					validUsed = true
					return abuse.Verification{Success: true, Hostname: "photos.test", Action: abuse.ChallengeAction}, nil
				case "wrong-host":
					return abuse.Verification{Success: true, Hostname: "evil.test", Action: abuse.ChallengeAction}, nil
				case "wrong-action":
					return abuse.Verification{Success: true, Hostname: "photos.test", Action: "other"}, nil
				case "duplicate":
					return abuse.Verification{Codes: []string{"timeout-or-duplicate"}}, nil
				case "unavailable":
					return abuse.Verification{}, errors.New("private-secret private-token transport diagnostic")
				default:
					return abuse.Verification{}, nil
				}
			})
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			assets, _ := web.Assets()
			srv, err := newServer(t.Context(), cfg, db, assets, logger, verifier)
			if err != nil {
				t.Fatal(err)
			}
			on := true
			e, err := events.New(db).Create(t.Context(), events.Input{Name: "security", Enabled: &on})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/public/events/" + e.PublicID + "/upload-sessions"
			call := func(method, path, body string, status int) *httptest.ResponseRecorder {
				t.Helper()
				r := httptest.NewRequest(method, "https://photos.test"+path, strings.NewReader(body))
				r.Header.Set("Origin", cfg.BaseURL)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				srv.Handler.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s: got %d want %d: %s", path, w.Code, status, w.Body.String())
				}
				return w
			}
			for _, page := range []string{"/e/" + e.PublicID, "/admin/login", "/api/public/events/" + e.PublicID, "/healthz"} {
				w := call("GET", page, "", 200)
				for key, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY", "Cache-Control": "no-store", "Strict-Transport-Security": "max-age=31536000"} {
					if w.Header().Get(key) != want {
						t.Fatal(key, w.Header())
					}
				}
				csp := w.Header().Get("Content-Security-Policy")
				if !strings.Contains(csp, "object-src 'none'") || w.Header().Get("Permissions-Policy") == "" {
					t.Fatal("missing headers")
				}
				if strings.Contains(csp, "challenges.cloudflare.com") != enabled {
					t.Fatal("challenge CSP condition")
				}
				if strings.Contains(w.Body.String(), "private-secret") {
					t.Fatal("secret in response")
				}
			}
			if enabled {
				for _, token := range []string{"", "invalid", "wrong-host", "wrong-action", "duplicate"} {
					body, _ := json.Marshal(map[string]string{"turnstile_token": token})
					call("POST", path, string(body), 403)
				}
				call("POST", path, `{"turnstile_token":"unavailable"}`, 503)
				var count int
				db.QueryRow("SELECT count(*) FROM upload_sessions").Scan(&count)
				if count != 0 {
					t.Fatal("failed verification granted a session")
				}
			}
			call("POST", path, `{"turnstile_token":"`+strings.Repeat("x", 5000)+`"}`, 413)
			body := "{}"
			if enabled {
				body = `{"turnstile_token":"valid-private-token"}`
			}
			w := call("POST", path, body, 201)
			var result struct {
				Session media.Session `json:"upload_session"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			before := calls
			data := testutil.Images()["image/png"]
			for range 2 {
				r := httptest.NewRequest("POST", "https://photos.test"+path+"/"+result.Session.ID+"/assets", bytes.NewReader(data))
				r.Header.Set("Origin", cfg.BaseURL)
				r.Header.Set("Content-Type", "image/png")
				r.Header.Set("Content-Disposition", `attachment; filename="a.png"`)
				w := httptest.NewRecorder()
				srv.Handler.ServeHTTP(w, r)
				if w.Code != 201 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			if calls != before || (!enabled && calls != 0) {
				t.Fatal("challenge per photo or disabled verifier called")
			}
			if enabled {
				call("POST", path, body, 403)
				var grants int
				db.QueryRow("SELECT count(*) FROM upload_sessions").Scan(&grants)
				if grants != 1 {
					t.Fatal("replayed token created another grant")
				}
			}
			var verified *string
			var expiry string
			db.QueryRow("SELECT challenge_verified_at,expires_at FROM upload_sessions WHERE id=?", result.Session.ID).Scan(&verified, &expiry)
			if (verified != nil) != enabled || expiry == "" {
				t.Fatal("grant metadata")
			}
			for _, suffix := range []string{"/prepare", "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/authorize", "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/complete"} {
				call("POST", path+"/"+result.Session.ID+"/assets"+suffix, `{"bad":"`+strings.Repeat("x", 5000)+`"}`, 413)
			}
			call("POST", "/api/admin/login", `{"password":"`+strings.Repeat("x", 2000)+`"}`, 413)
			for _, secret := range []string{"valid-private-token", "private-secret", "private-token"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("secret/token logged")
				}
			}
			// Persisted session columns contain only grant metadata, never the challenge token.
			var persisted string
			db.QueryRow("SELECT group_concat(id||created_at||updated_at||expires_at||COALESCE(challenge_verified_at,'')) FROM upload_sessions").Scan(&persisted)
			if strings.Contains(persisted, "private-token") {
				t.Fatal("token persisted")
			}
		})
	}
}

func TestRateScopesNATAndSpoofing(t *testing.T) {
	for _, tt := range []struct {
		scope string
		limit int
	}{{"login", 10}, {"session", 120}, {"prepare", 120}, {"authorize", 120}, {"complete", 300}, {"local", 120}} {
		t.Run(tt.scope, func(t *testing.T) {
			a := &application{baseURL: "http://test", logger: testLogger(), security: config.Security{}.Defaults(), limiter: abuse.NewLimiter(10000)}
			nextCalls := 0
			handler := a.guarded(tt.scope, func(w http.ResponseWriter, r *http.Request) { nextCalls++; w.WriteHeader(204) })
			call := func(event, session, spoof string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "http://test/", nil)
				r.Header.Set("Origin", "http://test")
				r.RemoteAddr = "203.0.113.4:20"
				r.Header.Set("X-Forwarded-For", spoof)
				r.SetPathValue("public_id", event)
				r.SetPathValue("session_id", session)
				w := httptest.NewRecorder()
				handler(w, r)
				return w
			}
			for i := range tt.limit {
				if w := call("event-a", "session-a", fmt.Sprint(i)); w.Code != 204 {
					t.Fatal("early", w.Code)
				}
			}
			w := call("event-a", "session-a", "8.8.8.8")
			if w.Code != 429 || w.Header().Get("Retry-After") == "" || nextCalls != tt.limit {
				t.Fatal("limit bypassed", w.Code, nextCalls)
			}
			if tt.scope != "login" {
				if w := call("event-b", "session-b", ""); w.Code != 204 {
					t.Fatal("events incorrectly shared limit")
				}
			}
			if tt.scope != "login" && tt.scope != "session" {
				if w := call("event-a", "session-b", ""); w.Code != 204 {
					t.Fatal("NAT sessions incorrectly shared limit")
				}
			}
		})
	}
	// Trusted proxies separate clients, while X-Real-IP/CF headers remain irrelevant.
	a := &application{baseURL: "http://test", logger: testLogger(), security: config.Security{RateMultiplier: 1, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}, limiter: abuse.NewLimiter(100)}
	handler := a.guarded("login", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for i := range 11 {
		r := httptest.NewRequest("POST", "http://test", nil)
		r.RemoteAddr = "10.0.0.1:12"
		r.Header.Set("Origin", "http://test")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != 204 {
			t.Fatal("trusted clients combined")
		}
	}
}

func TestMaintenanceRunsDuringUptimeAndStops(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assets, _ := web.Assets()
	srv, err := New(t.Context(), config.Config{DataDir: dir, AdminPassword: "maintenance-test-password"}, db, assets, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler.(*maintainedHandler)
	on := true
	e, err := events.New(db).Create(t.Context(), events.Input{Name: "maintenance", Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.uploads.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := db.Exec("INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_backend_id,expected_size_bytes) VALUES(?,?,?,'stale.png',?,'pending','2000-01-01T00:00:00Z',1,100)", id, e.ID, session.ID, fmt.Sprintf("e%d_%s", e.ID, id)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); h.maintain(ctx, time.Millisecond) }()
	defer cancel()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("no periodic reclamation")
		case <-tick.C:
			var count int
			if err := db.QueryRow("SELECT count(*) FROM assets").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == 0 {
				cancel()
				select {
				case <-done:
					return
				case <-time.After(time.Second):
					t.Fatal("maintenance did not stop")
				}
			}
		}
	}
}

func TestRateBeforeVerifierAndConcurrentAdmission(t *testing.T) {
	a := &application{baseURL: "http://test", logger: testLogger(), security: config.Security{}.Defaults(), limiter: abuse.NewLimiter(1000)}
	var mu sync.Mutex
	calls := 0
	handler := a.guarded("session", func(w http.ResponseWriter, r *http.Request) { mu.Lock(); calls++; mu.Unlock(); w.WriteHeader(201) })
	var wg sync.WaitGroup
	for range 150 {
		wg.Go(func() {
			r := httptest.NewRequest("POST", "http://test", nil)
			r.Header.Set("Origin", "http://test")
			r.SetPathValue("public_id", "event")
			handler(httptest.NewRecorder(), r)
		})
	}
	wg.Wait()
	if calls != 120 {
		t.Fatal("external-work admission raced", calls)
	}
}
