package immich

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"photodrop/internal/config"
	"photodrop/internal/destinations"
)

// Provider is Immich's implementation of destinations.Provider: credential
// validation here, and the OAuth-driven link flow in link.go.
type Provider struct {
	targets *Targets
	logger  *slog.Logger
}

var _ destinations.Provider = (*Provider)(nil)

func NewProvider(targets *Targets, logger *slog.Logger) *Provider {
	return &Provider{targets: targets, logger: logger}
}

func (*Provider) Name() string { return "immich" }

// Validate checks a credential against the account's Immich server: the key
// must authenticate and carry the four required permissions, and the server
// must speak API major version 3. A rejected key is a Status, not an error, so
// callers can mark the account; transport failures and 5xx are errors, which
// must leave account state alone.
func (p *Provider) Validate(ctx context.Context, account destinations.Account, secret []byte) (destinations.Status, error) {
	t, ok := p.targets.entries[account.TargetID]
	if !ok || t.base == "" || account.Provider != "immich" {
		return destinations.Status{}, ErrCredentials
	}
	client, err := NewClient(config.Immich{URL: t.base, APIKey: string(secret)})
	if err != nil {
		return destinations.Status{}, ErrCredentials
	}
	_, err = client.Validate(ctx)
	switch {
	case err == nil:
		return destinations.Status{Valid: true}, nil
	case errors.Is(err, ErrAuth):
		return destinations.Status{Reason: ErrAuth.Error()}, nil
	default:
		return destinations.Status{}, err
	}
}

// LinkTarget is a target as account linking sees it.
type LinkTarget struct {
	ID      int64
	Key     string
	HasURL  bool // an API URL is configured, so hosts can link here
	Ignored bool // a configured key was ignored for host 1's linked account
}

// LinkTargets lists every known target, sorted by key.
func (p *Provider) LinkTargets() []LinkTarget {
	out := make([]LinkTarget, 0, len(p.targets.entries))
	for _, t := range p.targets.entries {
		out = append(out, LinkTarget{ID: t.ID, Key: t.Key, HasURL: t.base != "", Ignored: slices.Contains(p.targets.Ignored, t.Key)})
	}
	slices.SortFunc(out, func(a, b LinkTarget) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// LinkTargetByKey and LinkTargetByID find one target.
func (p *Provider) LinkTargetByKey(key string) (LinkTarget, bool) {
	for _, t := range p.LinkTargets() {
		if t.Key == key {
			return t, true
		}
	}
	return LinkTarget{}, false
}

func (p *Provider) LinkTargetByID(id int64) (LinkTarget, bool) {
	for _, t := range p.LinkTargets() {
		if t.ID == id {
			return t, true
		}
	}
	return LinkTarget{}, false
}
