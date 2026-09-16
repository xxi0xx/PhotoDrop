package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"

	"photodrop/internal/config"
	"photodrop/internal/database"
	portable "photodrop/internal/export"
	"photodrop/internal/media"
	"photodrop/internal/storage"
)

func exportEvent(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.Int64("event", 0, "internal event ID shown in the admin URL")
	output := flags.String("output", "", "new output directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *id < 1 || *output == "" {
		return errors.New("usage: photodrop export --event ID --output NEW_DIRECTORY")
	}
	cfg, err := config.LoadExport()
	if err != nil {
		return err
	}
	db, err := database.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	local, err := storage.NewLocal(filepath.Join(cfg.DataDir, "uploads"))
	if err != nil {
		return err
	}
	backends, err := storage.Reconcile(ctx, db, cfg, logger)
	if err != nil {
		return err
	}
	source := media.New(db, local, logger)
	source.ConfigureBackends(backends)
	return portable.Run(ctx, source, *id, *output, logger)
}
