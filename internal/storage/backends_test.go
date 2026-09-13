package storage_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/storage"
)

func TestBackendRegistrationImmutabilityAndSecrets(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	c := config.S3{Bucket: "bucket-a", Endpoint: "https://store.test", Region: "auto", Prefix: "photos/", PathStyle: true, AccessKeyID: "private-test-id", SecretAccessKey: "private-test-secret", SessionToken: "private-test-token", PresignTTL: time.Minute}
	cfg := config.Config{StorageProvider: "s3", StorageBackendKey: "primary-r2", S3Backends: map[string]config.S3{"primary-r2": c}}
	first, err := storage.Reconcile(t.Context(), db, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	if first.ActiveID == 1 {
		t.Fatal("S3 not selected")
	}
	local, err := first.Resolve(1)
	if err != nil || local.Key != "local-default" || local.Type != "local" {
		t.Fatal("missing built-in local backend", err)
	}
	again, err := storage.Reconcile(t.Context(), db, cfg, logger)
	if err != nil || again.ActiveID != first.ActiveID {
		t.Fatal("backend registration not stable", err)
	}
	for _, change := range []func(*config.S3){func(c *config.S3) { c.Bucket = "bucket-b" }, func(c *config.S3) { c.Endpoint = "https://other.test" }, func(c *config.S3) { c.Prefix = "other/" }, func(c *config.S3) { c.Region = "us-east-1" }, func(c *config.S3) { c.PathStyle = false }} {
		changed := c
		change(&changed)
		cfg.S3Backends["primary-r2"] = changed
		if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err == nil || !strings.Contains(err.Error(), "choose a new") {
			t.Fatal("repointed existing backend", err)
		}
	}
	cfg.S3Backends["primary-r2"] = c
	changed := c
	changed.Bucket = "bucket-b"
	cfg.S3Backends["new-r2"] = changed
	cfg.StorageBackendKey = "new-r2"
	newer, err := storage.Reconcile(t.Context(), db, cfg, logger)
	if err != nil || newer.ActiveID == first.ActiveID {
		t.Fatal("new destination not independently registered", err)
	}
	rotated := c
	rotated.SecretAccessKey = "rotated-runtime-secret"
	cfg.S3Backends["primary-r2"] = rotated
	if _, err := storage.Reconcile(t.Context(), db, cfg, logger); err != nil {
		t.Fatal("credential rotation rejected", err)
	}
	delete(cfg.S3Backends, "primary-r2")
	registry, err := storage.Reconcile(t.Context(), db, cfg, logger)
	if err != nil {
		t.Fatal("missing historical credentials blocked startup", err)
	}
	if _, err := registry.Resolve(first.ActiveID); !errors.Is(err, storage.ErrBackend) || !strings.Contains(err.Error(), `"primary-r2"`) {
		t.Fatal("missing useful safe backend diagnostic", err)
	}
	if _, err := db.Exec("UPDATE storage_backends SET bucket='evil' WHERE id=?", first.ActiveID); err == nil {
		t.Fatal("database allowed repointing")
	}
	if _, err := db.Exec("DELETE FROM storage_backends WHERE id=?", first.ActiveID); err == nil {
		t.Fatal("database allowed identity reuse")
	}
	rows, err := db.Query("SELECT * FROM storage_backends")
	if err != nil {
		t.Fatal(err)
	}
	columns, _ := rows.Columns()
	serialized := ""
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(values)
		serialized += string(b)
	}
	rows.Close()
	for _, secret := range []string{c.AccessKeyID, c.SecretAccessKey, c.SessionToken, rotated.SecretAccessKey} {
		if strings.Contains(serialized+logs.String()+fmt.Sprint(cfg), secret) {
			t.Fatal("secret persisted or logged")
		}
	}
}
