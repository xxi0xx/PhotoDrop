package immich

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"photodrop/internal/config"
	"photodrop/internal/destinations"
)

// The Immich link flow (plan: "Immich link and unlink flow"). PhotoDrop drives
// Immich's own OAuth login server-to-server, uses the short Immich session only
// to check identity and mint or delete a key, then logs that session out on
// every exit path. Responses are size-bounded and mapped to fixed errors.

const (
	linkLimit = 64 << 10  // ordinary link responses
	userLimit = 256 << 10 // GET /users/me
)

func (p *Provider) linkClient(ctx context.Context, targetKey string) (*Client, error) {
	t, err := p.targets.ByKey(targetKey)
	if err != nil || t.base == "" || targetKey == "" {
		return nil, destinations.ErrLinkFailed
	}
	c, err := anonymous(t.base)
	if err != nil {
		return nil, destinations.ErrLinkFailed
	}
	var version Version
	if err := c.json(ctx, "GET", "/server/version", nil, &version, 4096); err != nil {
		return nil, destinations.ErrLinkFailed
	}
	if !SupportsLink(version) {
		return nil, destinations.ErrLinkVersion
	}
	return c, nil
}

// LinkCapable probes whether a target can run the link flow now.
func (p *Provider) LinkCapable(ctx context.Context, targetKey string) error {
	_, err := p.linkClient(ctx, targetKey)
	return err
}

// BeginLink asks Immich for the identity provider URL using PhotoDrop's own
// state and PKCE challenge, and refuses anything that does not echo them.
func (p *Provider) BeginLink(ctx context.Context, req destinations.LinkRequest) (string, error) {
	c, err := p.linkClient(ctx, req.TargetKey)
	if err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	in := map[string]string{"redirectUri": req.CallbackURL, "state": req.State, "codeChallenge": req.Challenge}
	if err := c.json(ctx, "POST", "/oauth/authorize", in, &out, linkLimit); err != nil {
		return "", destinations.ErrLinkFailed
	}
	location, err := url.Parse(out.URL)
	if err != nil || !config.ValidOIDCURL(out.URL) {
		return "", destinations.ErrLinkFailed
	}
	q := location.Query()
	for _, key := range []string{"state", "redirect_uri", "code_challenge", "code_challenge_method"} {
		if len(q[key]) > 1 {
			return "", destinations.ErrLinkFailed
		}
	}
	if q.Get("state") != req.State || q.Get("redirect_uri") != req.CallbackURL {
		return "", destinations.ErrLinkFailed
	}
	// Immich adds PKCE only when the provider advertises it; if present it must be ours.
	if q.Has("code_challenge") && (q.Get("code_challenge") != req.Challenge || q.Get("code_challenge_method") != "S256") {
		return "", destinations.ErrLinkFailed
	}
	return out.URL, nil
}

type loginResponse struct {
	AccessToken string `json:"accessToken"`
	UserID      string `json:"userId"`
}

type immichUser struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	OAuthID string `json:"oauthId"`
}

func (immichUser) String() string { return "Immich user (redacted)" }

// session exchanges the authorization code through Immich and returns a client
// authenticated by the resulting short session, plus a logout function the
// caller must defer. The code exchange itself happens inside Immich.
func (p *Provider) session(ctx context.Context, tx destinations.LinkTransaction, callbackURL string) (*Client, immichUser, func(), error) {
	noop := func() {}
	c, err := p.linkClient(ctx, tx.TargetKey)
	if err != nil {
		return nil, immichUser{}, noop, err
	}
	var login loginResponse
	in := map[string]string{"url": callbackURL, "state": tx.State, "codeVerifier": tx.Verifier}
	if err := c.json(ctx, "POST", "/oauth/callback", in, &login, linkLimit); err != nil {
		return nil, immichUser{}, noop, destinations.ErrLinkFailed
	}
	if login.AccessToken == "" || len(login.AccessToken) > 4096 || !uuid.MatchString(login.UserID) {
		return nil, immichUser{}, noop, destinations.ErrLinkFailed
	}
	authed := c.as(login.AccessToken, "")
	logout := func() {
		// The session must die on every exit path. Use a fresh context: the
		// request may already be cancelled. Retry once, then warn.
		for attempt := range 2 {
			done, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := authed.json(done, "POST", "/auth/logout", nil, nil, linkLimit)
			cancel()
			if err == nil {
				return
			}
			if attempt == 1 && p.logger != nil {
				p.logger.Warn("Immich link session logout failed")
			}
		}
	}
	var me immichUser
	if err := authed.json(ctx, "GET", "/users/me", nil, &me, userLimit); err != nil || me.ID != login.UserID {
		logout()
		return nil, immichUser{}, noop, destinations.ErrLinkFailed
	}
	return authed, me, logout, nil
}

func sameEmail(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// matchHost is link step 7. Subject mode compares Immich's oauthId with the
// host's identity subject; email mode compares verified emails. A subject
// mismatch whose emails match is reported separately so the operator can set
// LINK_MATCH=email; it never falls back to email automatically.
func matchHost(tx destinations.LinkTransaction, me immichUser) error {
	if tx.ExpectedRemoteUserID != "" && me.ID != tx.ExpectedRemoteUserID {
		return destinations.ErrWrongUser
	}
	if tx.MatchMode == "email" {
		if sameEmail(me.Email, tx.HostEmail) {
			return nil
		}
		return destinations.ErrWrongUser
	}
	if me.OAuthID != "" && me.OAuthID == tx.HostSubject {
		return nil
	}
	if sameEmail(me.Email, tx.HostEmail) {
		return destinations.ErrSubjectMismatch
	}
	return destinations.ErrWrongUser
}

func validSecret(secret string) bool {
	return secret != "" && len(secret) <= 4096 && strings.IndexFunc(secret, unicode.IsControl) < 0
}

func samePermissions(got []string) bool {
	return len(got) == len(requiredPermissions) && !slices.ContainsFunc(requiredPermissions, func(p string) bool { return !slices.Contains(got, p) })
}

// CompleteLink is link steps 6 to 12: exchange, identity check, uniqueness,
// mint a 4-permission key, self-check it, store it, and log out. Anything it
// created remotely is deleted if a later step fails.
func (p *Provider) CompleteLink(ctx context.Context, tx destinations.LinkTransaction, callbackURL string) (destinations.Linked, error) {
	authed, me, logout, err := p.session(ctx, tx, callbackURL)
	if err != nil {
		return destinations.Linked{}, err
	}
	defer logout()
	if err := matchHost(tx, me); err != nil {
		return destinations.Linked{}, err
	}
	if tx.Check != nil {
		if err := tx.Check(ctx, me.ID); err != nil {
			return destinations.Linked{}, err
		}
	}
	var minted struct {
		Secret string `json:"secret"`
		APIKey struct {
			ID          string   `json:"id"`
			Permissions []string `json:"permissions"`
		} `json:"apiKey"`
	}
	if err := authed.json(ctx, "POST", "/api-keys", map[string]any{"name": tx.KeyName, "permissions": requiredPermissions}, &minted, linkLimit); err != nil {
		return destinations.Linked{}, destinations.ErrLinkFailed
	}
	remove := func() {
		_ = authed.json(context.Background(), "DELETE", "/api-keys/"+minted.APIKey.ID, nil, nil, linkLimit)
	}
	if !uuid.MatchString(minted.APIKey.ID) {
		return destinations.Linked{}, destinations.ErrLinkFailed
	}
	if !validSecret(minted.Secret) || !samePermissions(minted.APIKey.Permissions) {
		remove()
		return destinations.Linked{}, destinations.ErrLinkFailed
	}
	// Self-check with the new key itself.
	var self struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	if err := authed.as("", minted.Secret).json(ctx, "GET", "/api-keys/me", nil, &self, linkLimit); err != nil || self.ID != minted.APIKey.ID || !samePermissions(self.Permissions) {
		remove()
		return destinations.Linked{}, destinations.ErrLinkFailed
	}
	secret := []byte(minted.Secret)
	minted.Secret = ""
	defer clear(secret)
	linked := destinations.Linked{RemoteUserID: me.ID, RemoteEmail: me.Email, RemoteCredentialID: minted.APIKey.ID, Secret: secret}
	if tx.Store != nil {
		if err := tx.Store(ctx, linked); err != nil {
			remove()
			return destinations.Linked{}, err
		}
	}
	// Relink: the same session deletes the old key after the new one is stored.
	if tx.PreviousCredentialID != "" && uuid.MatchString(tx.PreviousCredentialID) && tx.PreviousCredentialID != minted.APIKey.ID {
		if err := authed.json(ctx, "DELETE", "/api-keys/"+tx.PreviousCredentialID, nil, nil, linkLimit); err != nil && !errors.Is(err, ErrMissing) && p.logger != nil {
			p.logger.Warn("old Immich key could not be deleted after relink")
		}
	}
	linked.Secret = nil
	return linked, nil
}

// Revoke is unlink steps 2 and 3: confirm the session is the stored Immich user,
// then delete the stored key. A key that is already gone counts as done.
func (p *Provider) Revoke(ctx context.Context, tx destinations.LinkTransaction, callbackURL string, account destinations.Account) error {
	authed, me, logout, err := p.session(ctx, tx, callbackURL)
	if err != nil {
		return err
	}
	defer logout()
	if me.ID != account.RemoteUserID {
		return destinations.ErrWrongUser
	}
	if !uuid.MatchString(account.RemoteCredentialID) {
		return destinations.ErrLinkFailed
	}
	if err := authed.json(ctx, "DELETE", "/api-keys/"+account.RemoteCredentialID, nil, nil, linkLimit); err != nil && !errors.Is(err, ErrMissing) {
		return destinations.ErrLinkFailed
	}
	return nil
}
