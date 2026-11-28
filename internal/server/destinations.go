package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/oauth2"
	"photodrop/internal/auth"
	"photodrop/internal/destinations"
	"photodrop/internal/integrations/immich"
)

// Account linking (plan: "Immich link and unlink flow"). PhotoDrop drives
// Immich's own OAuth login server-to-server. The browser only carries
// redirects; this file owns the browser-bound transaction and the result page.

const (
	linkCookie       = "photodrop_link"
	linkCookiePath   = "/api/admin/destinations"
	linkCallbackPath = "/api/admin/destinations/immich/callback"
)

func (a *application) linkingCookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	expires := time.Now().Add(destinations.LinkLifetime)
	if age < 0 {
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: linkCookie, Value: value, Path: linkCookiePath, HttpOnly: true,
		Secure: strings.HasPrefix(a.baseURL, "https://") || r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: age, Expires: expires})
}

type destinationAccount struct {
	destinations.Public
	Target       string `json:"target"`
	StatusReason string `json:"status_reason,omitempty"`
}

type destinationTarget struct {
	Key      string `json:"key"`
	Linkable bool   `json:"linkable"`
	Reason   string `json:"reason,omitempty"`
}

// listDestinations shows the caller's own accounts and whether each target can
// be linked now. Admins see other hosts' accounts only through the host list.
func (a *application) listDestinations(w http.ResponseWriter, r *http.Request, s auth.Session) {
	accounts, err := destinations.ForHost(r.Context(), a.db, s.Host.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	result := struct {
		Accounts []destinationAccount `json:"accounts"`
		Targets  []destinationTarget  `json:"targets"`
	}{Accounts: []destinationAccount{}, Targets: []destinationTarget{}}
	for _, t := range a.links.LinkTargets() {
		account, has := accounts[t.ID]
		if has {
			result.Accounts = append(result.Accounts, destinationAccount{Public: account.Public(), Target: t.Key, StatusReason: account.StatusReason})
		}
		view := destinationTarget{Key: t.Key}
		switch {
		case !a.keyring.Configured():
			view.Reason = "secrets_key"
		case a.baseURL == "":
			view.Reason = "base_url"
		case !t.HasURL:
			view.Reason = "no_url"
		case has && account.Source == destinations.SourceConfig:
			view.Reason = "config_account"
		default:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := a.links.LinkCapable(ctx, t.Key)
			cancel()
			switch {
			case errors.Is(err, destinations.ErrLinkVersion):
				view.Reason = "immich_version"
			case err != nil:
				view.Reason = "immich_unavailable"
			}
		}
		if t.Ignored && view.Reason == "" && !has {
			view.Reason = "config_key_ignored"
		}
		view.Linkable = view.Reason == ""
		result.Targets = append(result.Targets, view)
	}
	writeJSON(w, 200, result)
}

// keyName is the visible name of the minted Immich key, at most 100 characters.
func (a *application) keyName(displayName string) string {
	host := a.baseURL
	if u, err := url.Parse(a.baseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	name := "PhotoDrop · " + host + " · " + displayName
	if utf8.RuneCountInString(name) > 100 {
		name = string([]rune(name)[:100])
	}
	return name
}

func linkFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, destinations.ErrLinkVersion):
		apiError(w, 422, "immich_version", "This Immich server must be upgraded to 3.4.2 or later before accounts can be linked.", nil)
	case errors.Is(err, destinations.ErrLinkSecretsKey):
		apiError(w, 422, "secrets_key", "Account linking needs a secrets key; ask the administrator to set PHOTODROP_SECRETS_KEY_FILE.", nil)
	default:
		apiError(w, 502, "link_unavailable", "Immich could not start account linking. Try again later.", nil)
	}
}

// beginLink runs link step 1 to 3 for a link, relink or unlink and returns the
// identity provider URL for the browser.
func (a *application) beginLink(w http.ResponseWriter, r *http.Request, s auth.Session, purpose string, target immich.LinkTarget, accountID int64) {
	if !a.keyring.Configured() {
		linkFailure(w, destinations.ErrLinkSecretsKey)
		return
	}
	if a.baseURL == "" || !target.HasURL {
		apiError(w, 422, "link_unavailable", "This deployment cannot link Immich accounts.", nil)
		return
	}
	state, browser, verifier := auth.RandomToken(), auth.RandomToken(), oauth2.GenerateVerifier()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	location, err := a.links.BeginLink(ctx, destinations.LinkRequest{HostID: s.Host.ID, TargetKey: target.Key,
		CallbackURL: a.baseURL + linkCallbackPath, State: state, Challenge: oauth2.S256ChallengeFromVerifier(verifier)})
	if err != nil {
		linkFailure(w, err)
		return
	}
	if err := destinations.SaveTransaction(ctx, a.db, destinations.Pending{StateHash: auth.TokenHash(state), Purpose: purpose, Provider: "immich",
		HostID: s.Host.ID, SessionHash: auth.TokenHash(cookieToken(r)), BrowserHash: auth.TokenHash(browser),
		TargetID: target.ID, AccountID: accountID, Verifier: verifier}); err != nil {
		linkFailure(w, err)
		return
	}
	a.linkingCookie(w, r, browser, int(destinations.LinkLifetime.Seconds()))
	a.logger.Info("Immich account link started", "host_id", s.Host.ID, "purpose", purpose)
	writeJSON(w, 200, map[string]string{"location": location})
}

func (a *application) startLink(w http.ResponseWriter, r *http.Request, s auth.Session) {
	var input struct {
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	target, ok := a.links.LinkTargetByKey(input.Target)
	if !ok {
		apiError(w, 422, "validation", "Choose a configured Immich server", map[string]string{"target": "Unknown Immich server"})
		return
	}
	accounts, err := destinations.ForHost(r.Context(), a.db, s.Host.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	// Invariant 5: one live account per host per server. An existing linked
	// account is relinked in place; a config account is managed in the environment.
	var accountID int64
	if existing, has := accounts[target.ID]; has {
		if existing.Source != destinations.SourceLinked {
			apiError(w, 409, "config_account", "This server already uses your API key from the deployment configuration.", nil)
			return
		}
		accountID = existing.ID
	}
	a.beginLink(w, r, s, "link", target, accountID)
}

// ownLinkedAccount returns one of the caller's live linked accounts, or writes 404.
func (a *application) ownLinkedAccount(w http.ResponseWriter, r *http.Request, s auth.Session) (destinations.Account, immich.LinkTarget, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	account, getErr := destinations.Get(r.Context(), a.db, id)
	target, known := a.links.LinkTargetByID(account.TargetID)
	if err != nil || getErr != nil || !known || account.HostID != s.Host.ID || account.Source != destinations.SourceLinked || account.Status == destinations.StatusRevoked {
		apiError(w, 404, "not_found", "Account not found", nil)
		return destinations.Account{}, immich.LinkTarget{}, false
	}
	return account, target, true
}

func (a *application) startUnlink(w http.ResponseWriter, r *http.Request, s auth.Session) {
	if !emptyControl(w, r) {
		return
	}
	account, target, ok := a.ownLinkedAccount(w, r, s)
	if !ok {
		return
	}
	a.beginLink(w, r, s, "unlink", target, account.ID)
}

// forgetAccount removes a linked account locally when the remote key cannot be
// revoked through Immich, and names the key to delete by hand.
func (a *application) forgetAccount(w http.ResponseWriter, r *http.Request, s auth.Session) {
	if !emptyControl(w, r) {
		return
	}
	account, _, ok := a.ownLinkedAccount(w, r, s)
	if !ok {
		return
	}
	if err := destinations.Revoke(r.Context(), a.db, s.Host.ID, account.ID); err != nil {
		a.fail(w, err)
		return
	}
	identity, err := a.auth.HostLinkIdentity(r.Context(), s.Host.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.logger.Info("Immich account forgotten", "host_id", s.Host.ID, "account_id", account.ID)
	writeJSON(w, 200, map[string]string{"key_name": a.keyName(identity.DisplayName)})
}

func validLinkToken(token string) bool {
	value, err := base64.RawURLEncoding.DecodeString(token)
	return len(token) == 43 && err == nil && len(value) == 32
}

// linkCallback is where the identity provider returns the browser. It consumes
// the browser-bound transaction, finishes the link or unlink server-to-server,
// and always ends with a fixed result code on the Account page.
func (a *application) linkCallback(w http.ResponseWriter, r *http.Request) {
	result := func(code string) {
		a.linkingCookie(w, r, "", -1)
		http.Redirect(w, r, "/admin/account?result="+code, http.StatusSeeOther)
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || a.links == nil {
		result("link_failed")
		return
	}
	for _, key := range []string{"state", "code", "error", "iss"} {
		if len(query[key]) > 1 {
			result("link_failed")
			return
		}
	}
	session, err := a.auth.Lookup(r.Context(), cookieToken(r))
	browser, cookieErr := r.Cookie(linkCookie)
	state := query.Get("state")
	if err != nil || cookieErr != nil || !validLinkToken(state) || !validLinkToken(browser.Value) {
		a.logger.Info("Immich account link failed", "reason", "session_or_state")
		result("link_failed")
		return
	}
	pending, err := destinations.ConsumeTransaction(r.Context(), a.db, auth.TokenHash(state), auth.TokenHash(browser.Value))
	if err != nil || pending.SessionHash != auth.TokenHash(cookieToken(r)) || pending.HostID != session.Host.ID || query.Has("error") || query.Get("code") == "" {
		a.logger.Info("Immich account link failed", "reason", "transaction")
		result("link_failed")
		return
	}
	target, known := a.links.LinkTargetByID(pending.TargetID)
	identity, idErr := a.auth.HostLinkIdentity(r.Context(), pending.HostID)
	if !known || idErr != nil {
		result("link_failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tx := destinations.LinkTransaction{Purpose: pending.Purpose, HostID: pending.HostID, TargetKey: target.Key, AccountID: pending.AccountID,
		State: state, Verifier: pending.Verifier, MatchMode: a.linkMatch[target.Key], HostSubject: identity.Subject, HostEmail: identity.Email,
		KeyName: a.keyName(identity.DisplayName)}
	if tx.MatchMode == "" {
		tx.MatchMode = "subject"
	}
	callbackURL := a.baseURL + linkCallbackPath + "?" + r.URL.RawQuery
	var account destinations.Account
	if pending.AccountID != 0 {
		account, err = destinations.Get(ctx, a.db, pending.AccountID)
		if err != nil || account.HostID != pending.HostID || account.Source != destinations.SourceLinked || account.Status == destinations.StatusRevoked {
			result("link_failed")
			return
		}
		tx.ExpectedRemoteUserID, tx.PreviousCredentialID = account.RemoteUserID, account.RemoteCredentialID
	}
	if pending.Purpose == "unlink" {
		err = a.links.Revoke(ctx, tx, callbackURL, account)
		if err == nil {
			err = destinations.Revoke(ctx, a.db, pending.HostID, account.ID)
		}
	} else {
		tx.Check = func(ctx context.Context, remoteUserID string) error {
			return destinations.CheckRemoteUser(ctx, a.db, target.ID, remoteUserID, pending.AccountID)
		}
		tx.Store = func(ctx context.Context, linked destinations.Linked) error {
			_, err := destinations.StoreLinked(ctx, a.db, a.keyring, pending.HostID, target.ID, target.Key, pending.AccountID, linked)
			return err
		}
		_, err = a.links.CompleteLink(ctx, tx, callbackURL)
	}
	code := destinations.ResultCode(pending.Purpose, err)
	a.logger.Info("Immich account link finished", "host_id", pending.HostID, "purpose", pending.Purpose, "result", code)
	result(code)
}
