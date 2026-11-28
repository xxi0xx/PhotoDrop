package main

import (
	"context"
	"fmt"
	"io"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/destinations"
	"photodrop/internal/secrets"
)

// rotateSecrets re-encrypts stored linked credentials under the active key. It
// prints counts only. To rotate: make the new key active, list the old key in
// PHOTODROP_SECRETS_PREVIOUS_KEY_FILES, restart, run this, then remove the old key.
func rotateSecrets(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 0 {
		return destinations.ErrRotateUsage
	}
	cfg, err := config.LoadSecrets()
	if err != nil {
		return err
	}
	ring, err := secrets.Load(cfg.Secrets.KeyFile, cfg.Secrets.PreviousKeyFiles)
	if err != nil {
		return err
	}
	if !ring.Configured() {
		return fmt.Errorf("%w: set PHOTODROP_SECRETS_KEY_FILE", secrets.ErrNoKey)
	}
	db, err := database.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	counts, err := destinations.Rotate(ctx, db, ring)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Linked credentials: %d. Re-encrypted: %d. Already on the active key: %d. Changed during the run and left alone: %d.\n", counts.Linked, counts.Rotated, counts.Current, counts.Changed)
	return nil
}
