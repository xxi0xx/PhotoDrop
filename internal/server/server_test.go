package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"photodrop/web"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEmbeddedFrontendAndHealth(t *testing.T) {
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(":8080", assets, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}
	health := request(http.MethodGet, "/healthz")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &body); err != nil || body.Status != "ok" || health.Code != http.StatusOK {
		t.Fatalf("health response: %d %s, error=%v", health.Code, health.Body, err)
	}
	if health.Header().Get("Content-Type") != "application/json" || health.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("health headers: %v", health.Header())
	}
	root := request(http.MethodGet, "/")
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "<title>PhotoDrop</title>") || !strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("root response: %d %s", root.Code, root.Body)
	}
	// Follow the actual generated asset paths, protecting the frontend/Go
	// embedding boundary instead of assuming a particular hashed filename.
	assetsRE := regexp.MustCompile(`(?:src|href)="(/assets/[^"\s]+)"`)
	links := assetsRE.FindAllStringSubmatch(root.Body.String(), -1)
	if len(links) < 2 {
		t.Fatal("root must reference compiled JS and CSS")
	}
	for _, link := range links {
		res := request(http.MethodGet, link[1])
		if res.Code != http.StatusOK || res.Body.Len() == 0 || strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("broken compiled asset %s: %d", link[1], res.Code)
		}
	}
	for _, path := range []string{"/missing", "/assets/missing.js", "/assets/", "/api/missing", "/healthz/", "/src/App.svelte", "/photodrop.db", "/debug/pprof/"} {
		if res := request(http.MethodGet, path); res.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, res.Code)
		}
	}
	if res := request(http.MethodPost, "/healthz"); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz: expected 405, got %d", res.Code)
	}
	if _, err := New(":8080", fstest.MapFS{}, testLogger()); err == nil {
		t.Error("accepted a missing frontend")
	}
}

func TestGracefulShutdownDrainsRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte("finished"))
	})}
	t.Cleanup(func() { srv.Close() })
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, srv, listener, testLogger()) }()
	response := make(chan string, 1)
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		res, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			response <- err.Error()
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		response <- string(body)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-served:
		close(release)
		t.Fatalf("server returned before draining the active request: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if body := <-response; body != "finished" {
		t.Fatalf("active request interrupted: %s", body)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestHealthCheck(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("wrong health check path: %s", r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			if err := CheckHealth(t.Context(), srv.Listener.Addr().String()); (err == nil) != (status == http.StatusOK) {
				t.Fatalf("status %d: %v", status, err)
			}
		})
	}
}
