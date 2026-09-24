package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"photodrop/internal/buildinfo"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/server"
	"photodrop/web"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.Error("PhotoDrop failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println(buildinfo.String())
		return nil
	}
	if len(args) > 0 && args[0] == "export" {
		return exportEvent(ctx, args[1:], logger)
	}
	if len(args) != 0 && !(len(args) == 1 && args[0] == "healthcheck") {
		return fmt.Errorf("usage: photodrop [version | healthcheck | export --event ID --output DIRECTORY]")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if len(args) == 1 {
		return server.CheckHealth(ctx, cfg.ListenAddr)
	}
	startupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	db, err := database.Open(startupCtx, cfg.DataDir)
	defer cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("close database", "error", err)
		}
	}()
	assets, err := web.Assets()
	if err != nil {
		return fmt.Errorf("load frontend: %w", err)
	}
	logger.Info("database initialized and migrations verified")
	srv, err := server.New(startupCtx, cfg, db, assets, logger)
	cfg.AdminPassword = ""
	cancel()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	logger.Info("PhotoDrop started", "address", listener.Addr().String(), "database", filepath.Join(cfg.DataDir, "photodrop.db"))
	return server.Serve(ctx, srv, listener, logger)
}
