package immich

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/destinations"
)

// Target is an Immich server as one host sees it: Available and Account
// describe that host's account there.
type Target struct {
	ID        int64                `json:"id"`
	Key       string               `json:"key"`
	Available bool                 `json:"available"`
	PublicURL string               `json:"public_url,omitempty"`
	Account   *destinations.Public `json:"account,omitempty"`
	client    API                  // the config account's client, when its key is configured
	base      string               // normalized API origin; never serialized
}
type Targets struct {
	ActiveKey string
	// Ignored lists target keys whose configured API key was ignored because
	// host 1 already has a linked account on that target.
	Ignored []string
	entries map[int64]Target
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
		t.base = base
		one := cfg.ImmichTargets[t.Key]
		if one.PublicURL != "" {
			t.PublicURL, err = config.NormalizeImmichURL(one.PublicURL)
			if err != nil {
				rows.Close()
				return nil, err
			}
		}
		if one.APIKey != "" && one.URL != "" {
			client, err := NewClient(config.Immich{URL: base, APIKey: one.APIKey})
			if err != nil {
				rows.Close()
				return nil, err
			}
			t.client = client
		}
		result.entries[t.ID] = t
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Config accounts follow configured keys. A missing key never changes a row.
	ids := make([]int64, 0, len(result.entries))
	for id := range result.entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		t := result.entries[id]
		ignored, err := destinations.ReconcileConfig(ctx, tx, t.ID, t.client != nil)
		if err != nil {
			return nil, err
		}
		if ignored {
			result.Ignored = append(result.Ignored, t.Key)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// ByKey finds a target; an empty key means the deployment default.
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

type TargetCatalog struct {
	ActiveTarget string   `json:"active_target"`
	Targets      []Target `json:"targets"`
}

// usable reports whether an account can make Immich requests now. Linked
// accounts need the credential flow, which this deployment does not run yet.
func (t Target) usable(a destinations.Account) bool {
	return a.Status == destinations.StatusActive && a.Source == destinations.SourceConfig && t.client != nil
}

// withAccount is the target as seen through one account.
func (t Target) withAccount(a destinations.Account) Target {
	public := a.Public()
	t.Account = &public
	t.Available = t.usable(a)
	return t
}

// TargetCatalog lists the targets where a host has a live account. Discovery is
// local configuration only, not a health probe. Internal API URLs and
// credentials never appear in Target's JSON representation. The default target
// is reported only when the host can use it.
func (s *Service) TargetCatalog(ctx context.Context, hostID int64) (TargetCatalog, error) {
	accounts, err := destinations.ForHost(ctx, s.db, hostID)
	if err != nil {
		return TargetCatalog{}, err
	}
	result := TargetCatalog{Targets: []Target{}}
	for _, one := range s.targets.entries {
		if a, ok := accounts[one.ID]; ok {
			view := one.withAccount(a)
			result.Targets = append(result.Targets, view)
			if view.Key == s.targets.ActiveKey && view.Available {
				result.ActiveTarget = view.Key
			}
		}
	}
	sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].Key < result.Targets[j].Key })
	return result, nil
}

// accountClient resolves the Immich client for an account. Only active config
// accounts with a configured key have one in this release.
// q must be the open transaction when the caller holds one: the database has a
// single connection.
func (s *Service) accountClient(ctx context.Context, q rowQuerier, accountID int64) (API, error) {
	a, err := destinations.Get(ctx, q, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentials
	}
	if err != nil {
		return nil, err
	}
	t, ok := s.targets.entries[a.TargetID]
	if !ok || !t.usable(a) {
		return nil, ErrCredentials
	}
	return t.client, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
