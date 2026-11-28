package immich

import (
	"context"
	"errors"

	"photodrop/internal/config"
	"photodrop/internal/destinations"
)

// Provider is Immich's implementation of destinations.Provider. It validates
// credentials; linking (BeginLink, CompleteLink, Revoke) is not built yet, so
// those report destinations.ErrLinkUnsupported.
type Provider struct{ targets *Targets }

var _ destinations.Provider = (*Provider)(nil)

func NewProvider(targets *Targets) *Provider { return &Provider{targets: targets} }

func (*Provider) Name() string { return "immich" }

func (*Provider) BeginLink(context.Context, destinations.LinkRequest) (string, error) {
	return "", destinations.ErrLinkUnsupported
}

func (*Provider) CompleteLink(context.Context, destinations.LinkTransaction, string) (destinations.Linked, error) {
	return destinations.Linked{}, destinations.ErrLinkUnsupported
}

func (*Provider) Revoke(context.Context, destinations.LinkTransaction, string, destinations.Account) error {
	return destinations.ErrLinkUnsupported
}

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
