package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"photodrop/internal/config"
)

type Backend struct {
	ID        int64
	Key, Type string
	Direct    Direct
}
type Backends struct {
	ActiveID int64
	entries  map[int64]Backend
}

func LocalBackends() *Backends {
	return &Backends{ActiveID: 1, entries: map[int64]Backend{1: {ID: 1, Key: config.LocalBackendKey, Type: "local"}}}
}
func (b *Backends) Resolve(id int64) (Backend, error) {
	entry, ok := b.entries[id]
	if !ok {
		return Backend{}, fmt.Errorf("%w: unknown storage backend", ErrBackend)
	}
	if entry.Type == "s3" && entry.Direct == nil {
		return Backend{}, fmt.Errorf("%w: storage backend %q is not configured for this runtime", ErrBackend, entry.Key)
	}
	return entry, nil
}
func (b *Backends) Origins(ctx context.Context) ([]string, error) {
	origins := map[string]bool{}
	for _, entry := range b.entries {
		if s3, ok := entry.Direct.(*S3); ok {
			origin, err := s3.UploadOrigin(ctx)
			if err != nil {
				return nil, err
			}
			origins[origin] = true
		}
	}
	out := []string{}
	for origin := range origins {
		out = append(out, origin)
	}
	sort.Strings(out)
	return out, nil
}

// Reconcile runs before cleanup or HTTP serving. Only non-secret addressing
// fields are persisted. Registration and all legacy associations commit together.
func Reconcile(ctx context.Context, db *sql.DB, cfg config.Config, logger *slog.Logger) (*Backends, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runtime := map[string]Direct{}
	legacy := map[string][]int64{}
	registered := []string{}
	keys := []string{}
	for key := range cfg.S3Backends {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !config.ValidBackendKey(key) || key == config.LocalBackendKey {
			return nil, fmt.Errorf("invalid S3 backend key")
		}
		c := cfg.S3Backends[key]
		if !c.Configured() {
			return nil, fmt.Errorf("storage backend %q requires runtime credentials", key)
		}
		var id int64
		var kind, endpoint, bucket, region, prefix string
		var pathStyle bool
		err := tx.QueryRowContext(ctx, "SELECT id,type,endpoint,bucket,region,path_style,prefix FROM storage_backends WHERE key=?", key).Scan(&id, &kind, &endpoint, &bucket, &region, &pathStyle, &prefix)
		if err == sql.ErrNoRows {
			result, err := tx.ExecContext(ctx, "INSERT INTO storage_backends(key,type,endpoint,bucket,region,path_style,prefix,created_at) VALUES(?,'s3',?,?,?,?,?,?)", key, c.Endpoint, c.Bucket, c.Region, c.PathStyle, c.Prefix, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return nil, err
			}
			id, err = result.LastInsertId()
			if err != nil {
				return nil, err
			}
			registered = append(registered, key)
		} else if err != nil {
			return nil, err
		} else if kind != "s3" || endpoint != c.Endpoint || bucket != c.Bucket || region != c.Region || pathStyle != c.PathStyle || prefix != c.Prefix {
			logger.Error("storage backend configuration mismatch", "backend_key", key)
			return nil, fmt.Errorf("storage backend %q addressing differs from its persisted destination; choose a new PHOTODROP_STORAGE_BACKEND_KEY (or named backend key)", key)
		}
		client := NewS3(c)
		runtime[key] = client
		legacy[client.Target()] = append(legacy[client.Target()], id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT storage_target FROM assets WHERE storage_backend_id IS NULL AND storage_provider='s3'
 UNION SELECT storage_target FROM s3_cleanup WHERE storage_backend_id IS NULL`)
	if err != nil {
		return nil, err
	}
	targets := []string{}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	migrated := int64(0)
	for _, target := range targets {
		ids := legacy[target]
		if len(ids) != 1 {
			return nil, fmt.Errorf("cannot identify legacy Gate 4 S3 storage safely: supply its original S3 configuration with an explicit backend key (PHOTODROP_STORAGE_BACKEND_KEY or PHOTODROP_S3_BACKENDS); each legacy target must match exactly one configured backend")
		}
		for _, table := range []string{"assets", "s3_cleanup"} {
			result, err := tx.ExecContext(ctx, "UPDATE "+table+" SET storage_backend_id=? WHERE storage_backend_id IS NULL AND storage_target=?", ids[0], target)
			if err != nil {
				return nil, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return nil, err
			}
			migrated += n
		}
	}
	var unresolved int
	if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM assets WHERE storage_backend_id IS NULL)+(SELECT count(*) FROM s3_cleanup WHERE storage_backend_id IS NULL)").Scan(&unresolved); err != nil {
		return nil, err
	}
	if unresolved != 0 {
		return nil, fmt.Errorf("unresolved legacy asset storage backend; restore the original Gate 4 configuration")
	}
	result := &Backends{ActiveID: 1, entries: map[int64]Backend{}}
	rows, err = tx.QueryContext(ctx, "SELECT id,key,type FROM storage_backends")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var entry Backend
		if err := rows.Scan(&entry.ID, &entry.Key, &entry.Type); err != nil {
			rows.Close()
			return nil, err
		}
		entry.Direct = runtime[entry.Key]
		result.entries[entry.ID] = entry
		if cfg.StorageProvider == "s3" && entry.Key == cfg.StorageBackendKey {
			result.ActiveID = entry.ID
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	active, err := result.Resolve(result.ActiveID)
	if err != nil {
		return nil, err
	}
	if cfg.StorageProvider == "s3" && active.Type != "s3" {
		return nil, fmt.Errorf("PHOTODROP_STORAGE_BACKEND_KEY must select a configured S3 backend")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	for _, key := range registered {
		logger.Info("storage backend registered", "backend_key", key)
	}
	if migrated > 0 {
		logger.Info("legacy storage associations migrated", "records", migrated)
	}
	logger.Info("active storage backend resolved", "backend_key", active.Key)
	return result, nil
}
