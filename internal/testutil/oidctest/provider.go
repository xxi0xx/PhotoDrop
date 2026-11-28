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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const ClientID = "photodrop-test"
const Secret = "disposable-oidc-client-secret"

// Faults selects the identity the provider issues next and injects failures.
// Subject, Groups and the profile claims describe the signed-in user.
type Faults struct {
	Unavailable                                                    bool
	DiscoveryIssuer, Issuer, Audience, Nonce, Subject              string
	MissingSubject, Expired, BadSignature, NoIDToken, TokenFailure bool
	Groups                                                         any
	GroupsClaim                                                    string
	// Profile claims, omitted when empty. EmailVerified adds email_verified.
	Email, Name, PreferredUsername string
	EmailVerified                  bool
}

// Client is a registered relying party. An empty Redirects list accepts any
// redirect URI (the historical PhotoDrop fixture); otherwise the match is exact.
type Client struct {
	Secret       string
	Redirects    []string
	RequireNonce bool
}

// Exchange records one token request, for tests that check what a relying
// party sent (for example the redirect_uri Immich derives from a callback URL).
type Exchange struct {
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
	AuthMethod  string `json:"auth_method"`
	Accepted    bool   `json:"accepted"`
}

type flow struct{ Client, Nonce, Challenge, Redirect string }
type Provider struct {
	Issuer                    string
	mu                        sync.Mutex
	key                       *rsa.PrivateKey
	badKey                    *rsa.PrivateKey
	kid                       string
	faults                    Faults
	clients                   map[string]Client
	codes                     map[string]flow
	access                    map[string]map[string]any
	history                   []Exchange
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
	return &Provider{Issuer: issuer, key: key, badKey: bad, kid: "first", codes: map[string]flow{}, access: map[string]map[string]any{},
		clients: map[string]Client{ClientID: {Secret: Secret, RequireNonce: true}}}, nil
}

// SetClient registers or replaces a relying party.
func (p *Provider) SetClient(id string, c Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[id] = c
}
func (p *Provider) SetFaults(f Faults) { p.mu.Lock(); defer p.mu.Unlock(); p.faults = f }
func (p *Provider) Counts() (int, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, p.exchanges, p.jwks
}

// Exchanges returns the token requests seen so far, oldest first.
func (p *Provider) Exchanges() []Exchange {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.history)
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

func randomToken() string {
	var raw [32]byte
	rand.Read(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// clientAuth accepts client_secret_basic or client_secret_post.
func clientAuth(r *http.Request) (id, secret, method string) {
	if id, secret, ok := r.BasicAuth(); ok {
		return id, secret, "client_secret_basic"
	}
	return r.Form.Get("client_id"), r.Form.Get("client_secret"), "client_secret_post"
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
		json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": p.Issuer + "authorize", "token_endpoint": p.Issuer + "token", "userinfo_endpoint": p.Issuer + "userinfo", "jwks_uri": p.Issuer + "jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"}, "authorization_response_iss_parameter_supported": true})
	case strings.HasSuffix(r.URL.Path, "/authorize"):
		q := r.URL.Query()
		client, known := p.clients[q.Get("client_id")]
		if !known || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || (client.RequireNonce && q.Get("nonce") == "") || q.Get("state") == "" || !strings.Contains(" "+q.Get("scope")+" ", " openid ") {
			http.Error(w, "invalid authorization", 400)
			return
		}
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || redirect.Host == "" || (len(client.Redirects) > 0 && !slices.Contains(client.Redirects, q.Get("redirect_uri"))) {
			http.Error(w, "invalid redirect", 400)
			return
		}
		code := randomToken()
		p.codes[code] = flow{q.Get("client_id"), q.Get("nonce"), q.Get("code_challenge"), q.Get("redirect_uri")}
		values := redirect.Query()
		values.Set("code", code)
		values.Set("state", q.Get("state"))
		values.Set("iss", p.Issuer)
		redirect.RawQuery = values.Encode()
		http.Redirect(w, r, redirect.String(), http.StatusSeeOther)
	case strings.HasSuffix(r.URL.Path, "/jwks"):
		p.jwks++
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: p.kid, Algorithm: "RS256", Use: "sig"}}})
	case strings.HasSuffix(r.URL.Path, "/userinfo"):
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, known := p.access[token]
		if !ok || !known {
			http.Error(w, "invalid token", 401)
			return
		}
		json.NewEncoder(w).Encode(claims)
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
		id, secret, method := clientAuth(r)
		record := Exchange{ClientID: id, RedirectURI: r.Form.Get("redirect_uri"), AuthMethod: method}
		defer func() { p.history = append(p.history, record) }()
		client, known := p.clients[id]
		if !known || secret != client.Secret {
			http.Error(w, "invalid client", 401)
			return
		}
		code := r.Form.Get("code")
		transaction, ok := p.codes[code]
		delete(p.codes, code)
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || transaction.Client != id || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != transaction.Redirect || base64.RawURLEncoding.EncodeToString(digest[:]) != transaction.Challenge {
			http.Error(w, "invalid grant or PKCE", 400)
			return
		}
		issuer, audience, nonce, subject := p.Issuer, id, transaction.Nonce, "stable-admin-subject"
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
		claims := map[string]any{"iss": issuer, "aud": audience, "sub": subject, "iat": time.Now().Unix(), "exp": exp, groupClaim: groups}
		if nonce != "" {
			claims["nonce"] = nonce
		}
		profile := map[string]any{"sub": subject}
		for name, value := range map[string]string{"email": f.Email, "name": f.Name, "preferred_username": f.PreferredUsername} {
			if value != "" {
				claims[name], profile[name] = value, value
			}
		}
		if f.Email != "" {
			claims["email_verified"], profile["email_verified"] = f.EmailVerified, f.EmailVerified
		}
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
		accessToken := "test-access-token-never-persist"
		if id != ClientID {
			accessToken = randomToken()
		}
		p.access[accessToken] = profile
		response := map[string]any{"access_token": accessToken, "refresh_token": "test-refresh-token-never-persist", "token_type": "Bearer", "expires_in": 60}
		if !f.NoIDToken {
			response["id_token"] = raw
		}
		record.Accepted = true
		json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, fmt.Sprint("unknown test endpoint"), 404)
	}
}
