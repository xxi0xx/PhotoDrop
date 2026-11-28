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
	"photodrop/internal/secrets"
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

// usable reports whether an account can make Immich requests now: a config
// account needs its configured key; a linked account needs a loaded key that can
// decrypt its stored credential.
func (s *Service) usable(t Target, a destinations.Account) bool {
	if a.Status != destinations.StatusActive {
		return false
	}
	switch a.Source {
	case destinations.SourceConfig:
		return t.client != nil
	case destinations.SourceLinked:
		return t.base != "" && s.keyring.Has(a.KeyID) && len(a.Ciphertext) > 0
	}
	return false
}

// withAccount is the target as seen through one account.
func (s *Service) withAccount(t Target, a destinations.Account) Target {
	public := a.Public()
	t.Account = &public
	t.Available = s.usable(t, a)
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
			view := s.withAccount(one, a)
			result.Targets = append(result.Targets, view)
			if view.Key == s.targets.ActiveKey && view.Available {
				result.ActiveTarget = view.Key
			}
		}
	}
	sort.Slice(result.Targets, func(i, j int) bool { return result.Targets[i].Key < result.Targets[j].Key })
	return result, nil
}

// accountClient resolves the Immich client for an account. A linked account's
// key is decrypted here, just before the client is built, and lives only as
// long as that client. q must be the open transaction when the caller holds
// one: the database has a single connection.
func (s *Service) accountClient(ctx context.Context, q rowQuerier, accountID int64) (API, error) {
	a, err := destinations.Get(ctx, q, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCredentials
	}
	if err != nil {
		return nil, err
	}
	t, ok := s.targets.entries[a.TargetID]
	if !ok || !s.usable(t, a) {
		return nil, ErrCredentials
	}
	if a.Source == destinations.SourceConfig {
		return t.client, nil
	}
	secret, err := s.decrypt(t, a)
	if err != nil {
		return nil, ErrCredentials
	}
	defer clear(secret)
	client, err := NewClient(config.Immich{URL: t.base, APIKey: string(secret)})
	if err != nil {
		return nil, ErrCredentials
	}
	return &linkedClient{Client: client, account: a, service: s}, nil
}

func (s *Service) decrypt(t Target, a destinations.Account) ([]byte, error) {
	aad, err := secrets.AAD(secrets.Binding{AccountID: a.ID, Provider: a.Provider, TargetKey: t.Key, RemoteUserID: a.RemoteUserID, RemoteCredentialID: a.RemoteCredentialID})
	if err != nil {
		return nil, err
	}
	return s.keyring.Decrypt(a.KeyID, a.Nonce, a.Ciphertext, aad)
}

// linkedClient validates that a linked key is still the one PhotoDrop minted:
// its id must equal the stored remote credential id and its permissions must
// still cover the four required ones. A rejected or replaced key marks the
// account needs_relink; transport errors and 5xx leave it alone.
type linkedClient struct {
	*Client
	account destinations.Account
	service *Service
}

func (c *linkedClient) Validate(ctx context.Context) (Version, error) {
	id, err := c.Client.keyID(ctx)
	switch {
	case errors.Is(err, ErrAuth):
		c.service.needsRelink(c.account, destinations.ReasonRejected)
		return Version{}, err
	case err != nil:
		return Version{}, err
	case id != c.account.RemoteCredentialID:
		c.service.needsRelink(c.account, destinations.ReasonKeyChanged)
		return Version{}, ErrAuth
	}
	v, err := c.Client.Validate(ctx)
	if errors.Is(err, ErrAuth) {
		c.service.needsRelink(c.account, destinations.ReasonRejected)
	}
	if err == nil {
		_ = destinations.MarkValidated(ctx, c.service.db, c.account.ID)
	}
	return v, err
}

func (s *Service) needsRelink(a destinations.Account, reason string) {
	done, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := destinations.MarkNeedsRelink(done, s.db, a.ID, reason); err == nil && s.logger != nil {
		s.logger.Warn("linked Immich account needs relinking", "account_id", a.ID)
	}
}

// ValidateLinked checks every active linked account once, in the background at
// startup. It never blocks startup and never deletes anything remotely.
func (s *Service) ValidateLinked(ctx context.Context) {
	accounts, err := destinations.LinkedAccounts(ctx, s.db)
	if err != nil {
		return
	}
	for _, a := range accounts {
		client, err := s.accountClient(ctx, s.db, a.ID)
		if err != nil {
			continue
		}
		check, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, _ = client.Validate(check)
		cancel()
	}
}

// ConfigureSecrets gives the service the keyring for linked accounts.
func (s *Service) ConfigureSecrets(ring *secrets.Keyring) { s.keyring = ring }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
