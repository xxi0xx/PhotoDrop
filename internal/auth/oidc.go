package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"photodrop/internal/config"
)

const OIDCLifetime = 5 * time.Minute
const OIDCCallbackPath = "/api/admin/oidc/callback"

var ErrOIDC = errors.New("OpenID Connect sign-in could not be completed")

// OIDC performs no network I/O until Begin/Complete. Only successful discovery
// is cached; transient failures cannot disable subsequent login attempts.
type OIDC struct {
	db       *sql.DB
	cfg      config.OIDC
	redirect string
	client   *http.Client
	mu       sync.Mutex
	provider *oidc.Provider
}

func NewOIDC(db *sql.DB, cfg config.OIDC, baseURL string) *OIDC {
	return &OIDC{db: db, cfg: cfg, redirect: baseURL + OIDCCallbackPath, client: &http.Client{
		Timeout: 10 * time.Second, Transport: oidcTransport{http.DefaultTransport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrOIDC },
	}}
}
func (s *OIDC) MatchesIssuer(issuer string) bool { return issuer == s.cfg.Issuer }

// Cap discovery/JWKS/token response memory and never forward secrets on redirects.
type oidcTransport struct{ base http.RoundTripper }

func (t oidcTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !config.ValidOIDCURL(r.URL.String()) {
		return nil, ErrOIDC
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, ErrOIDC
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, ErrOIDC
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (s *OIDC) discover(ctx context.Context) (*oidc.Provider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return s.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, s.client), s.cfg.Issuer)
	if err != nil {
		return nil, ErrOIDC
	}
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if err = p.Claims(&metadata); err != nil || !config.ValidOIDCURL(metadata.JWKS) || !config.ValidOIDCURL(p.Endpoint().AuthURL) || !config.ValidOIDCURL(p.Endpoint().TokenURL) {
		return nil, ErrOIDC
	}
	s.provider = p
	return p, nil
}
func (s *OIDC) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: s.cfg.ClientID, ClientSecret: s.cfg.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: s.redirect, Scopes: s.cfg.Scopes}
}

// Begin persists only hashes (except the short-lived PKCE verifier). Bound the
// transaction table independently of rate limiting; replace this browser's old flow.
func (s *OIDC) Begin(ctx context.Context, previousBrowser string) (location, browser string, err error) {
	p, err := s.discover(ctx)
	if err != nil {
		return "", "", ErrOIDC
	}
	state, nonce, verifier, browser := randomToken(), randomToken(), oauth2.GenerateVerifier(), randomToken()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", ErrOIDC
	}
	defer tx.Rollback()
	stamp := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `DELETE FROM oidc_transactions WHERE state_hash IN (SELECT state_hash FROM oidc_transactions WHERE expires_at<=? ORDER BY expires_at LIMIT 512) OR browser_hash=?`, stamp, tokenHash(previousBrowser)); err != nil {
		return "", "", ErrOIDC
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM oidc_transactions").Scan(&count); err != nil || count >= 512 {
		return "", "", ErrOIDC
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oidc_transactions(state_hash,nonce_hash,pkce_verifier,browser_hash,created_at,expires_at) VALUES(?,?,?,?,?,?)`, tokenHash(state), tokenHash(nonce), verifier, tokenHash(browser), stamp, stamp+int64(OIDCLifetime.Seconds())); err != nil {
		return "", "", ErrOIDC
	}
	if err = tx.Commit(); err != nil {
		return "", "", ErrOIDC
	}
	return s.oauth(p).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), browser, nil
}

// Complete consumes valid browser-bound state before any exchange. A failed or
// concurrent callback cannot retry the transaction; start a new sign-in instead.
// The returned identity's role comes from the configured lists at this login:
// the admin lists win over the host lists, and no match is ErrOIDC.
func (s *OIDC) Complete(ctx context.Context, state, browser, code string, providerError bool) (Identity, error) {
	if !validToken(state) || !validToken(browser) {
		return Identity{}, ErrOIDC
	}
	var nonceHash, verifier string
	err := s.db.QueryRowContext(ctx, `DELETE FROM oidc_transactions WHERE state_hash=? AND browser_hash=? AND expires_at>? RETURNING nonce_hash,pkce_verifier`, tokenHash(state), tokenHash(browser), time.Now().Unix()).Scan(&nonceHash, &verifier)
	if err != nil || providerError || code == "" || len(code) > 4096 {
		return Identity{}, ErrOIDC
	}
	p, err := s.discover(ctx)
	if err != nil {
		return Identity{}, ErrOIDC
	}
	token, err := s.oauth(p).Exchange(oidc.ClientContext(ctx, s.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, ErrOIDC
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, ErrOIDC
	}
	id, err := p.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(ctx, raw)
	if err != nil || id.Issuer != s.cfg.Issuer || strings.TrimSpace(id.Subject) == "" || subtle.ConstantTimeCompare([]byte(tokenHash(id.Nonce)), []byte(nonceHash)) != 1 {
		return Identity{}, ErrOIDC
	}
	var claims map[string]json.RawMessage
	if err = id.Claims(&claims); err != nil {
		return Identity{}, ErrOIDC
	}
	// OIDC Core recommends checking azp for multiple audiences and whenever present.
	if value, exists := claims["azp"]; exists {
		var party string
		if json.Unmarshal(value, &party) != nil || party != s.cfg.ClientID {
			return Identity{}, ErrOIDC
		}
	} else if len(id.Audience) > 1 {
		return Identity{}, ErrOIDC
	}
	var groups []string
	if len(s.cfg.AllowedGroups)+len(s.cfg.HostGroups) > 0 {
		if value, exists := claims[s.cfg.GroupsClaim]; exists {
			var entries []json.RawMessage
			if json.Unmarshal(value, &entries) != nil || entries == nil {
				return Identity{}, ErrOIDC
			}
			for _, entry := range entries {
				var group string
				if len(entry) == 0 || entry[0] != '"' || json.Unmarshal(entry, &group) != nil {
					return Identity{}, ErrOIDC
				}
				groups = append(groups, group)
			}
		}
	}
	matches := func(subjects, allowedGroups []string) bool {
		if slices.Contains(subjects, id.Subject) {
			return true
		}
		for _, group := range groups {
			if slices.Contains(allowedGroups, group) {
				return true
			}
		}
		return false
	}
	result := Identity{Issuer: id.Issuer, Subject: id.Subject, Groups: groups}
	switch {
	case matches(s.cfg.AllowedSubjects, s.cfg.AllowedGroups):
		result.Role = RoleAdmin
	case matches(s.cfg.HostSubjects, s.cfg.HostGroups):
		result.Role = RoleHost
	default:
		return Identity{}, ErrOIDC
	}
	// Profile claims are display data only. A claim of the wrong type is ignored.
	text := func(name string) string {
		var value string
		if raw, ok := claims[name]; ok && json.Unmarshal(raw, &value) == nil {
			return value
		}
		return ""
	}
	// The email is kept only when the provider asserts it is verified: Immich
	// account linking can match on it (owner decision, 2026-11-28).
	var verified bool
	if raw, ok := claims["email_verified"]; ok && json.Unmarshal(raw, &verified) == nil && verified {
		result.Email = text("email")
	}
	result.Name = DisplayName(text("name"), text("preferred_username"), result.Email)
	return result, nil
}
