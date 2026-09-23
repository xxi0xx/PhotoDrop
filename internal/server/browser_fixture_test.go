package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"photodrop/internal/abuse"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/testutil/s3test"
	"photodrop/web"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Deliberately compiled only by `go test`, never by cmd/photodrop or Docker's
// production build. This localhost-only disposable fixture exercises the real
// frontend and application handlers with injectable test dependencies. It has no
// production equivalent, secret, bypass header, or configurable Siteverify URL.
func TestSecurityBrowserFixture(t *testing.T) {
	if os.Getenv("PHOTODROP_BROWSER_FIXTURE") != "1" {
		t.Skip("explicit interactive browser fixture")
	}
	fixtureDir := os.Getenv("PHOTODROP_BROWSER_FIXTURE_DIR")
	if fixtureDir == "" {
		fixtureDir = filepath.Join("..", "..", ".tmp", "gate5-browser-data")
	}
	dir, err := filepath.Abs(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const origin = "http://localhost:8082"
	objectStores := map[string]*s3test.Server{}
	for _, port := range []int{18890, 18891} {
		objects := s3test.New(origin)
		objectStores[fmt.Sprint(port)] = objects
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: objects, ReadHeaderTimeout: time.Second}
		go srv.Serve(ln)
		defer srv.Close()
	}
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.RWMutex
	var current *http.Server
	var checks atomic.Int64
	var localBodies, sessionRequests atomic.Int64
	var failUpload, failComplete atomic.Bool
	var delayMS atomic.Int64
	var deterministicWidget bool
	type settings struct {
		Provider         string `json:"provider"`
		Challenge        string `json:"challenge"`
		Widget           string `json:"widget"`
		RejectCompletion bool   `json:"reject_completion"`
		FailNextUpload   bool   `json:"fail_next_upload"`
		FailNextComplete bool   `json:"fail_next_complete"`
		DelayMS          int64  `json:"delay_ms"`
	}
	set := func(value settings) error {
		failUpload.Store(value.FailNextUpload)
		failComplete.Store(value.FailNextComplete)
		delayMS.Store(max(0, min(10000, value.DelayMS)))
		deterministicWidget = value.Widget == "deterministic"
		for _, objects := range objectStores {
			status := 0
			if value.RejectCompletion {
				status = http.StatusServiceUnavailable
			}
			objects.Fault("HEAD", status)
		}
		cfg := config.Config{DataDir: dir, BaseURL: origin, AdminPassword: "temporary-gate5-browser-password", MaxFileSize: 1 << 20, StorageProvider: "local", Security: config.Security{SessionTTL: time.Minute}, S3Backends: map[string]config.S3{}}
		for i, key := range []string{"s3-a", "s3-b"} {
			cfg.S3Backends[key] = config.S3{Bucket: "photos", Endpoint: fmt.Sprintf("http://localhost:%d", 18890+i), Region: s3test.Region, Prefix: key + "/", PathStyle: true, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PresignTTL: time.Minute}
		}
		if value.Provider != "" && value.Provider != "local" {
			cfg.StorageProvider = "s3"
			cfg.StorageBackendKey = value.Provider
		}
		if value.Challenge != "" && value.Challenge != "off" {
			cfg.Security.TurnstileSiteKey = "1x00000000000000000000AA"
			cfg.Security.TurnstileSecretKey = "test-only-injected-verifier"
		}
		verifier := verifierFunc(func(ctx context.Context, token string) (abuse.Verification, error) {
			checks.Add(1)
			if value.Challenge == "reject" || token != "XXXX.DUMMY.TOKEN.XXXX" {
				return abuse.Verification{}, nil
			}
			return abuse.Verification{Success: true, Hostname: "localhost", Action: abuse.ChallengeAction}, nil
		})
		app, err := newServer(t.Context(), cfg, db, assets, testLogger(), verifier)
		if err != nil {
			return err
		}
		current = app
		return nil
	}
	if err := set(settings{}); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	var stopOnce sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/widget.js" {
			w.Header().Set("Content-Type", "application/javascript")
			// Replaces only the external widget, never the application's token handling
			// or server validation. Available exclusively in this loopback test binary.
			fmt.Fprint(w, `window.turnstile = {
render(element, options) {
  const id = crypto.randomUUID(); element.id = id;
  element.dataset.widgetSize = options.size;
  element.style.width = options.size === 'compact' ? '150px' : '300px';
  element.style.minHeight = options.size === 'compact' ? '140px' : '65px';
  const button = document.createElement('button'); button.type = 'button';
  button.textContent = 'Complete test verification';
  button.onclick = () => { button.textContent = 'Test verification complete'; button.disabled = true; options.callback('XXXX.DUMMY.TOKEN.XXXX'); };
  element.append(button); return id;
}, remove(id) { document.getElementById(id)?.replaceChildren(); }, reset() {}
};`)
			return
		}
		if r.URL.Path == "/__test/widget" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!doctype html><title>Standalone official Turnstile test</title><script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script><h1>Standalone official Turnstile test</h1><div class="cf-turnstile" data-sitekey="1x00000000000000000000AA" data-action="photodrop_upload"></div>`)
			return
		}
		if r.URL.Path == "/__test/settings" && r.Method == "POST" {
			var value settings
			if !decodeJSON(w, r, &value, 1024) {
				return
			}
			mu.Lock()
			err := set(value)
			mu.Unlock()
			if err != nil {
				t.Error(err)
				http.Error(w, "fixture setup failed", 500)
				return
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/expire" && r.Method == "POST" {
			if _, err := db.Exec("UPDATE upload_sessions SET expires_at='2000-01-01T00:00:00Z'"); err != nil {
				t.Error(err)
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/cleanup" && r.Method == "POST" {
			mu.Lock()
			defer mu.Unlock()
			db.Exec("UPDATE assets SET created_at='2000-01-01T00:00:00Z',authorized_until='2000-01-01T00:00:00Z' WHERE status='pending'")
			if err := current.Handler.(*maintainedHandler).uploads.Cleanup(r.Context()); err != nil {
				t.Error(err)
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/state" && r.Method == "GET" {
			var count, ready, bytes, reserved int64
			db.QueryRow("SELECT count(*),COALESCE(sum(status='ready'),0),COALESCE(sum(CASE WHEN status='ready' THEN size_bytes ELSE 0 END),0),COALESCE(sum(CASE WHEN status='pending' THEN expected_size_bytes ELSE 0 END),0) FROM assets").Scan(&count, &ready, &bytes, &reserved)
			var puts int
			for _, store := range objectStores {
				for _, request := range store.Requests() {
					if request.Method == "PUT" {
						puts++
					}
				}
			}
			writeJSON(w, 200, map[string]any{"verification_calls": checks.Load(), "assets": count, "ready": ready, "bytes": bytes, "reserved": reserved, "local_bodies": localBodies.Load(), "session_requests": sessionRequests.Load(), "direct_puts": puts})
			return
		}
		if r.URL.Path == "/__test/stop" && r.Method == "POST" {
			w.WriteHeader(204)
			stopOnce.Do(func() { close(stopped) })
			return
		}
		mu.RLock()
		defer mu.RUnlock()
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/public/events/") {
			if strings.HasSuffix(r.URL.Path, "/upload-sessions") {
				sessionRequests.Add(1)
			}
			isUpload := strings.HasSuffix(r.URL.Path, "/assets")
			isComplete := strings.HasSuffix(r.URL.Path, "/complete")
			if isUpload {
				localBodies.Add(1)
			}
			if isUpload || isComplete {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Duration(delayMS.Load()) * time.Millisecond):
				}
				if (isUpload && failUpload.CompareAndSwap(true, false)) || (isComplete && failComplete.CompareAndSwap(true, false)) {
					apiError(w, 503, "storage_unavailable", "Photo storage is temporarily unavailable. Please try again later.", nil)
					return
				}
			}
		}
		if deterministicWidget && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/e/") {
			response := httptest.NewRecorder()
			current.Handler.ServeHTTP(response, r)
			for key, values := range response.Header() {
				w.Header()[key] = values
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(response.Code)
			fmt.Fprint(w, strings.Replace(response.Body.String(), "</head>", `<script src="/__test/widget.js"></script></head>`, 1))
			return
		}
		current.Handler.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:8082")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go srv.Serve(ln)
	json.NewEncoder(os.Stdout).Encode(map[string]string{"browser_fixture": origin})
	select {
	case <-stopped:
	case <-time.After(2 * time.Hour):
		t.Error("browser fixture timed out")
	}
}
