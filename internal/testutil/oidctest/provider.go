// Package oidctest is a disposable signed OIDC provider, never imported by production.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const ClientID = "photodrop-test"
const Secret = "disposable-oidc-client-secret"

type Faults struct {
	Unavailable                                                    bool
	DiscoveryIssuer, Issuer, Audience, Nonce, Subject              string
	MissingSubject, Expired, BadSignature, NoIDToken, TokenFailure bool
	Groups                                                         any
	GroupsClaim                                                    string
}
type flow struct{ Nonce, Challenge, Redirect string }
type Provider struct {
	Issuer                    string
	mu                        sync.Mutex
	key                       *rsa.PrivateKey
	badKey                    *rsa.PrivateKey
	kid                       string
	faults                    Faults
	codes                     map[string]flow
	requests, exchanges, jwks int
}

func New(issuer string) (*Provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	bad, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Provider{Issuer: issuer, key: key, badKey: bad, kid: "first", codes: map[string]flow{}}, nil
}
func (p *Provider) SetFaults(f Faults) { p.mu.Lock(); defer p.mu.Unlock(); p.faults = f }
func (p *Provider) Counts() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, p.exchanges, p.jwks
}
func (p *Provider) Rotate() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.key = key
	p.kid = "rotated"
	return nil
}
func (p *Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	f := p.faults
	if f.Unavailable {
		http.Error(w, "test provider unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		issuer := p.Issuer
		if f.DiscoveryIssuer != "" {
			issuer = f.DiscoveryIssuer
		}
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": p.Issuer + "authorize", "token_endpoint": p.Issuer + "token", "jwks_uri": p.Issuer + "jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}})
	case strings.HasSuffix(r.URL.Path, "/authorize"):
		q := r.URL.Query()
		if q.Get("client_id") != ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("nonce") == "" || q.Get("state") == "" || !strings.Contains(" "+q.Get("scope")+" ", " openid ") {
			http.Error(w, "invalid authorization", 400)
			return
		}
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || redirect.Host == "" {
			http.Error(w, "invalid redirect", 400)
			return
		}
		var raw [32]byte
		rand.Read(raw[:])
		code := base64.RawURLEncoding.EncodeToString(raw[:])
		p.codes[code] = flow{q.Get("nonce"), q.Get("code_challenge"), redirect.String()}
		values := redirect.Query()
		values.Set("code", code)
		values.Set("state", q.Get("state"))
		values.Set("iss", p.Issuer)
		redirect.RawQuery = values.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
	case strings.HasSuffix(r.URL.Path, "/jwks"):
		p.jwks++
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: p.kid, Algorithm: "RS256", Use: "sig"}}})
	case strings.HasSuffix(r.URL.Path, "/token"):
		p.exchanges++
		if f.TokenFailure {
			http.Error(w, "test secret upstream response", 503)
			return
		}
		if r.ParseForm() != nil {
			http.Error(w, "bad request", 400)
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != ClientID || secret != Secret {
			http.Error(w, "invalid client", 401)
			return
		}
		code := r.Form.Get("code")
		transaction, ok := p.codes[code]
		delete(p.codes, code)
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != transaction.Redirect || base64.RawURLEncoding.EncodeToString(digest[:]) != transaction.Challenge {
			http.Error(w, "invalid grant or PKCE", 400)
			return
		}
		issuer, audience, nonce, subject := p.Issuer, ClientID, transaction.Nonce, "stable-admin-subject"
		if f.Issuer != "" {
			issuer = f.Issuer
		}
		if f.Audience != "" {
			audience = f.Audience
		}
		if f.Nonce != "" {
			nonce = f.Nonce
		}
		if f.Subject != "" {
			subject = f.Subject
		}
		if f.MissingSubject {
			subject = ""
		}
		exp := time.Now().Add(time.Minute).Unix()
		if f.Expired {
			exp = time.Now().Add(-time.Minute).Unix()
		}
		groups := f.Groups
		if groups == nil {
			groups = []string{"photodrop-admins"}
		}
		groupClaim := f.GroupsClaim
		if groupClaim == "" {
			groupClaim = "groups"
		}
		claims := map[string]any{"iss": issuer, "aud": audience, "sub": subject, "iat": time.Now().Unix(), "exp": exp, "nonce": nonce, groupClaim: groups}
		key := p.key
		if f.BadSignature {
			key = p.badKey
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", p.kid).WithType("JWT"))
		if err != nil {
			panic(err)
		}
		payload, _ := json.Marshal(claims)
		signed, err := signer.Sign(payload)
		if err != nil {
			panic(err)
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			panic(err)
		}
		response := map[string]any{"access_token": "test-access-token-never-persist", "refresh_token": "test-refresh-token-never-persist", "token_type": "Bearer", "expires_in": 60}
		if !f.NoIDToken {
			response["id_token"] = raw
		}
		json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, fmt.Sprint("unknown test endpoint"), 404)
	}
}
