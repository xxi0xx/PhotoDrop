package immich

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"photodrop/internal/config"
)

type Target struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Available bool   `json:"available"`
	client    API
}
type Targets struct {
	ActiveKey string
	entries   map[int64]Target
}

func Reconcile(ctx context.Context, db *sql.DB, cfg config.Config) (*Targets, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	keys := make([]string, 0, len(cfg.ImmichTargets))
	for key := range cfg.ImmichTargets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		one := cfg.ImmichTargets[key]
		if one.URL == "" {
			continue
		}
		normalized, err := config.NormalizeImmichURL(one.URL)
		if err != nil {
			return nil, err
		}
		var stored string
		err = tx.QueryRowContext(ctx, "SELECT base_url FROM immich_targets WHERE key=?", key).Scan(&stored)
		switch err {
		case sql.ErrNoRows:
			_, err = tx.ExecContext(ctx, "INSERT INTO immich_targets(key,base_url,created_at) VALUES(?,?,?)", key, normalized, now())
		case nil:
			if stored != normalized {
				return nil, fmt.Errorf("Immich target %q changed server identity; use a new target key", key)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,key,base_url FROM immich_targets ORDER BY id")
	if err != nil {
		return nil, err
	}
	result := &Targets{ActiveKey: cfg.ImmichTarget, entries: map[int64]Target{}}
	for rows.Next() {
		var t Target
		var base string
		if err := rows.Scan(&t.ID, &t.Key, &base); err != nil {
			rows.Close()
			return nil, err
		}
		one := cfg.ImmichTargets[t.Key]
		if one.APIKey != "" && one.URL != "" {
			client, err := NewClient(config.Immich{URL: base, APIKey: one.APIKey})
			if err != nil {
				rows.Close()
				return nil, err
			}
			t.client = client
			t.Available = true
		}
		result.entries[t.ID] = t
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (t *Targets) ByKey(key string) (Target, error) {
	if key == "" {
		key = t.ActiveKey
	}
	for _, one := range t.entries {
		if one.Key == key {
			return one, nil
		}
	}
	return Target{}, ErrCredentials
}
func (t *Targets) client(id int64) (API, error) {
	one, ok := t.entries[id]
	if !ok || one.client == nil {
		return nil, ErrCredentials
	}
	return one.client, nil
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
