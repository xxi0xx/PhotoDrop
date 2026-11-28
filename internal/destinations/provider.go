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

// LinkTransaction is the consumed, browser-bound state of a link or unlink,
// plus what the provider needs to verify identity. The registry supplies Check
// and Store, so the provider never touches SQLite or the keyring while still
// running them inside the remote session they depend on.
type LinkTransaction struct {
	Purpose   string // "link" or "unlink"
	HostID    int64
	TargetKey string
	AccountID int64 // the account being relinked or unlinked; 0 for a new link
	State     string
	Verifier  string

	// Identity matching (link step 7).
	MatchMode            string // "subject" or "email"
	HostSubject          string
	HostEmail            string // verified email, or ""
	ExpectedRemoteUserID string // relink and unlink: the stored remote user

	KeyName              string // name for the minted API key
	PreviousCredentialID string // relink: the old remote key, deleted after Store

	// Check rejects a remote user another live account already holds (step 8).
	Check func(ctx context.Context, remoteUserID string) error
	// Store persists the verified credential (step 11). If it fails, the
	// provider deletes the key it minted before returning the error.
	Store func(ctx context.Context, linked Linked) error
}

func (LinkTransaction) String() string     { return "destination link transaction (redacted)" }
func (t LinkTransaction) GoString() string { return t.String() }

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

// Link outcomes. Each maps to one fixed result code shown to the host.
var (
	ErrLinkUnsupported = errors.New("linking is not available for this destination")
	ErrLinkFailed      = errors.New("linking could not be completed")
	ErrWrongUser       = errors.New("the Immich user does not belong to this host")
	ErrSubjectMismatch = errors.New("the Immich user matches by email but not by identity subject")
	ErrInUse           = errors.New("this Immich user is already linked to another account")
	ErrLinkVersion     = errors.New("this Immich server is too old for account linking")
	ErrLinkSecretsKey  = errors.New("secrets key not configured")
)
