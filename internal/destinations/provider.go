package destinations

import (
	"context"
	"errors"
)

// Provider is one destination service, such as Immich. Providers never touch
// SQLite or the keyring: the registry owns persistence, encryption and
// authorization, so a second provider only adds an implementation and widens
// the provider CHECK in a new migration.
type Provider interface {
	Name() string // "immich"
	// BeginLink returns the provider URL to send the browser to.
	BeginLink(ctx context.Context, req LinkRequest) (location string, err error)
	// CompleteLink verifies identity and returns a credential to store. It must
	// revoke anything it created remotely if it returns an error.
	CompleteLink(ctx context.Context, tx LinkTransaction, callbackURL string) (Linked, error)
	Revoke(ctx context.Context, tx LinkTransaction, callbackURL string, account Account) error
	Validate(ctx context.Context, account Account, secret []byte) (Status, error)
}

// LinkRequest starts a link or unlink for one host on one provider endpoint.
type LinkRequest struct {
	HostID      int64
	TargetKey   string
	CallbackURL string
	State       string
	Challenge   string // PKCE S256 code challenge
}

// LinkTransaction is the consumed, browser-bound state of a link or unlink.
type LinkTransaction struct {
	Purpose   string // "link" or "unlink"
	HostID    int64
	TargetKey string
	AccountID int64
	State     string
	Verifier  string
}

// Linked is a verified remote identity plus the credential to store.
type Linked struct {
	RemoteUserID, RemoteEmail, RemoteCredentialID string
	Secret                                        []byte // the caller encrypts, then zeroes
}

func (Linked) String() string     { return "linked destination credential (redacted)" }
func (l Linked) GoString() string { return l.String() }

// Status is the outcome of validating a stored credential.
type Status struct {
	Valid  bool
	Reason string // a fixed, safe string when not valid
}

// ErrLinkUnsupported is returned by providers or deployments that cannot link.
var ErrLinkUnsupported = errors.New("linking is not available for this destination")
