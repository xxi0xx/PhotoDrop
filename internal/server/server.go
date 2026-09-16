// Package server owns HTTP routing and graceful server shutdown.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"photodrop/internal/abuse"
	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/events"
	"photodrop/internal/integrations/immich"
	"photodrop/internal/media"
	"photodrop/internal/storage"
)

func New(ctx context.Context, cfg config.Config, db *sql.DB, assets fs.FS, logger *slog.Logger) (*http.Server, error) {
	return newServer(ctx, cfg, db, assets, logger, nil)
}

// The verifier seam is package-private for deterministic tests; production has
// no environment setting, header, or query parameter selecting a fake verifier.
func newServer(ctx context.Context, cfg config.Config, db *sql.DB, assets fs.FS, logger *slog.Logger, verifier abuse.Verifier) (*http.Server, error) {
	cfg.Security = cfg.Security.Defaults()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded frontend (run npm ci and npm run build in web/ before building Go): %w", err)
	}
	admin, err := auth.New(ctx, db, cfg.AdminPassword)
	if err != nil {
		return nil, err
	}
	objects, err := storage.NewLocal(filepath.Join(cfg.DataDir, "uploads"))
	if err != nil {
		return nil, err
	}
	uploads := media.New(db, objects, logger)
	backends, err := storage.Reconcile(ctx, db, cfg, logger)
	if err != nil {
		return nil, err
	}
	uploads.ConfigureBackends(backends)
	targets, err := immich.Reconcile(ctx, db, cfg)
	if err != nil {
		return nil, err
	}
	imports := immich.New(db, uploads, targets, logger)
	uploads.ConfigureSecurity(cfg.Security)
	connectSrc := "'self'"
	origins, err := backends.Origins(ctx)
	if err != nil {
		return nil, fmt.Errorf("configure direct uploads: %w", err)
	}
	for _, origin := range origins {
		connectSrc += " " + origin
	}
	// Remote cleanup is best effort within a small startup budget. A temporary
	// S3 outage must not take admin pages or the process health check offline.
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 2*time.Second)
	cleanupErr := uploads.Cleanup(cleanupCtx)
	cleanupCancel()
	if cleanupErr != nil && !errors.Is(cleanupErr, context.DeadlineExceeded) && !errors.Is(cleanupErr, context.Canceled) {
		err := cleanupErr
		return nil, fmt.Errorf("clean incomplete uploads: %w", err)
	}
	if cleanupErr != nil {
		logger.Warn("startup media cleanup deferred")
	}
	if cfg.MaxFileSize == 0 {
		cfg.MaxFileSize = config.DefaultMaxFileSize
	}
	app := &application{events: events.New(db), auth: admin, media: uploads, maxFileSize: cfg.MaxFileSize, baseURL: cfg.BaseURL, index: index, logger: logger, security: cfg.Security, limiter: abuse.NewLimiter(10000), challengeSlots: make(chan struct{}, 8), loginSlots: make(chan struct{}, 2)}
	if cfg.Security.TurnstileSiteKey != "" {
		app.verifier = verifier
		if app.verifier == nil {
			app.verifier = abuse.NewTurnstile(cfg.Security.TurnstileSecretKey)
		}
	}
	app.immich = imports
	mux := http.NewServeMux()
	app.routes(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	fileServer := http.FileServerFS(assets)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		info, err := fs.Stat(assets, name)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		// Only registered application pages get the Svelte shell. Unknown URLs
		// and missing assets remain real 404s; no directory listings are exposed.
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		scripts, frames := "'self'", "'none'"
		if cfg.Security.TurnstileSiteKey != "" {
			scripts += " https://challenges.cloudflare.com"
			frames = "https://challenges.cloudflare.com"
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src "+connectSrc+"; script-src "+scripts+"; frame-src "+frames+"; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if strings.HasPrefix(cfg.BaseURL, "https://") || r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/e/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
		mux.ServeHTTP(w, r)
	})
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           &maintainedHandler{Handler: handler, uploads: uploads, logger: logger, imports: imports},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}, nil
}

// Serve waits for cancellation and drains HTTP requests before it returns.
func Serve(ctx context.Context, srv *http.Server, listener net.Listener, logger *slog.Logger) error {
	maintenanceCtx, stopMaintenance := context.WithCancel(ctx)
	maintenanceDone := make(chan struct{})
	importsDone := make(chan struct{})
	go func() {
		defer close(importsDone)
		if h, ok := srv.Handler.(*maintainedHandler); ok && h.imports != nil {
			h.imports.Run(maintenanceCtx)
		}
	}()
	go func() {
		defer close(maintenanceDone)
		if h, ok := srv.Handler.(*maintainedHandler); ok {
			h.maintain(maintenanceCtx, 5*time.Minute)
		}
	}()
	defer func() { stopMaintenance(); <-maintenanceDone; <-importsDone }()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(listener) }()
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
		<-result
		return fmt.Errorf("drain HTTP requests: %w", err)
	}
	if err := <-result; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	logger.Info("HTTP server stopped")
	return nil
}
