package auth

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/testutil/oidctest"
)

func TestOIDCProviderTransportBoundsAndRedirects(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			return
		}
		io.WriteString(w, strings.Repeat("x", (1<<20)+1))
	}))
	defer remote.Close()
	s := NewOIDC(nil, config.OIDC{}, "https://drop.test")
	for _, path := range []string{"/redirect", "/oversized"} {
		r, err := http.NewRequestWithContext(t.Context(), "POST", remote.URL+path, strings.NewReader("private-token-request"))
		if err != nil {
			t.Fatal(err)
		}
		r.SetBasicAuth("private-client", "private-secret")
		response, err := s.client.Do(r)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("provider redirect/oversized response accepted", path)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("provider redirect forwarded a confidential request")
	}
}

func oidcFixture(t *testing.T) (*OIDC, *oidctest.Provider, *sql.DB) {
	t.Helper()
	p, err := oidctest.New("")
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(p)
	t.Cleanup(remote.Close)
	p.Issuer = remote.URL + "/application/o/photodrop/"
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := config.OIDC{Issuer: p.Issuer, ClientID: oidctest.ClientID, ClientSecret: oidctest.Secret, Scopes: []string{"openid", "profile", "email"}, GroupsClaim: "groups", AllowedGroups: []string{"photodrop-admins"}}
	return NewOIDC(db, c, "https://drop.test"), p, db
}
func oidcFlow(t *testing.T, s *OIDC) (state, browser, code string) {
	t.Helper()
	location, browser, err := s.Begin(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("redirect_uri") != "https://drop.test"+OIDCCallbackPath || !validToken(q.Get("state")) || !validToken(q.Get("nonce")) || !validToken(browser) || q.Get("code_challenge_method") != "S256" {
		t.Fatal("unsafe authorization request")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatal("provider rejected auth request", r.StatusCode)
	}
	callback, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return callback.Query().Get("state"), browser, callback.Query().Get("code")
}
func TestOIDCProtocolAndRotation(t *testing.T) {
	s, p, db := oidcFixture(t)
	var lastState, lastBrowser string
	for i := 0; i < 2; i++ {
		state, browser, code := oidcFlow(t, s)
		if state == lastState || browser == lastBrowser {
			t.Fatal("reused random values")
		}
		lastState, lastBrowser = state, browser
		var storedState, nonce, verifier, binding string
		var created, expires int64
		if err := db.QueryRow("SELECT state_hash,nonce_hash,pkce_verifier,browser_hash,created_at,expires_at FROM oidc_transactions").Scan(&storedState, &nonce, &verifier, &binding, &created, &expires); err != nil {
			t.Fatal(err)
		}
		if storedState != tokenHash(state) || binding != tokenHash(browser) || len(nonce) != 64 || !validToken(verifier) || expires-created != 300 || state == browser || browser == verifier {
			t.Fatal("unsafe transaction storage")
		}
		if err := s.Complete(t.Context(), state, browser, code, false); err != nil {
			t.Fatal(err)
		}
		if err := s.Complete(t.Context(), state, browser, code, false); err == nil {
			t.Fatal("replay accepted")
		}
		var count int
		db.QueryRow("SELECT count(*) FROM oidc_transactions").Scan(&count)
		if count != 0 {
			t.Fatal("transaction/token retained")
		}
		if i == 0 {
			if err := p.Rotate(); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, exchanges, keys := p.Counts()
	if exchanges != 2 || keys != 2 {
		t.Fatal("JWKS rotation or replay exchange", exchanges, keys)
	}
}
func TestOIDCRejectsInvalidTokensAndClaims(t *testing.T) {
	s, p, _ := oidcFixture(t)
	for name, fault := range map[string]oidctest.Faults{
		"issuer": {Issuer: "https://wrong.test/"}, "audience": {Audience: "wrong-client"},
		"signature": {BadSignature: true}, "expiry": {Expired: true}, "nonce": {Nonce: "wrong-nonce"},
		"missing subject": {MissingSubject: true}, "missing ID token": {NoIDToken: true}, "token failure": {TokenFailure: true},
		"group denied": {Groups: []string{"other"}}, "group case sensitive": {Groups: []string{"PhotoDrop-admins"}},
		"group string": {Groups: "photodrop-admins"}, "group object": {Groups: map[string]bool{"photodrop-admins": true}},
		"group null entry": {Groups: []any{nil, "photodrop-admins"}}, "group number": {Groups: []any{42, "photodrop-admins"}},
	} {
		t.Run(name, func(t *testing.T) {
			p.SetFaults(fault)
			state, browser, code := oidcFlow(t, s)
			if err := s.Complete(t.Context(), state, browser, code, false); err == nil {
				t.Fatal("invalid response accepted")
			}
			if err := s.Complete(t.Context(), state, browser, code, false); err == nil {
				t.Fatal("failed callback reused")
			}
		})
	}
}
func TestOIDCAuthorizationORAndCustomClaim(t *testing.T) {
	s, p, _ := oidcFixture(t)
	for _, tc := range []struct {
		subjects, groups []string
		claim            string
		fault            oidctest.Faults
		allowed          bool
	}{
		{[]string{"stable-admin-subject"}, nil, "groups", oidctest.Faults{Groups: []string{"other"}}, true},
		{[]string{"another"}, nil, "groups", oidctest.Faults{}, false},
		{[]string{"another"}, []string{"photodrop-admins"}, "groups", oidctest.Faults{}, true},
		{[]string{"stable-admin-subject"}, []string{"other"}, "groups", oidctest.Faults{}, true},
		{[]string{"stable-admin-subject"}, []string{"other"}, "groups", oidctest.Faults{Groups: "malformed"}, false},
		{nil, []string{"photodrop-admins"}, "roles", oidctest.Faults{GroupsClaim: "roles"}, true},
		{nil, []string{"photodrop-admins"}, "missing", oidctest.Faults{}, false},
	} {
		s.cfg.AllowedSubjects = tc.subjects
		s.cfg.AllowedGroups = tc.groups
		s.cfg.GroupsClaim = tc.claim
		p.SetFaults(tc.fault)
		state, browser, code := oidcFlow(t, s)
		if err := s.Complete(t.Context(), state, browser, code, false); (err == nil) != tc.allowed {
			t.Fatal("authorization result", err)
		}
	}
}
func TestOIDCBindingExpiryPKCEAndConcurrentReplay(t *testing.T) {
	for _, fault := range []string{"binding", "expiry", "pkce", "provider error", "concurrent"} {
		t.Run(fault, func(t *testing.T) {
			s, p, db := oidcFixture(t)
			state, browser, code := oidcFlow(t, s)
			switch fault {
			case "binding":
				if s.Complete(t.Context(), state, randomToken(), code, false) == nil {
					t.Fatal("cross-browser callback accepted")
				}
				_, n, _ := p.Counts()
				if n != 0 {
					t.Fatal("exchanged unbound code")
				}
				if err := s.Complete(t.Context(), state, browser, code, false); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				db.Exec("UPDATE oidc_transactions SET expires_at=?", time.Now().Unix())
				if s.Complete(t.Context(), state, browser, code, false) == nil {
					t.Fatal("expired state accepted")
				}
				_, n, _ := p.Counts()
				if n != 0 {
					t.Fatal("exchanged expired code")
				}
			case "pkce":
				db.Exec("UPDATE oidc_transactions SET pkce_verifier=?", randomToken())
				if s.Complete(t.Context(), state, browser, code, false) == nil {
					t.Fatal("wrong PKCE accepted")
				}
			case "provider error":
				if s.Complete(t.Context(), state, browser, code, true) == nil || s.Complete(t.Context(), state, browser, code, false) == nil {
					t.Fatal("provider error failed to consume state")
				}
			case "concurrent":
				var success atomic.Int32
				var wg sync.WaitGroup
				start := make(chan struct{})
				for range 8 {
					wg.Go(func() {
						<-start
						if s.Complete(t.Context(), state, browser, code, false) == nil {
							success.Add(1)
						}
					})
				}
				close(start)
				wg.Wait()
				_, n, _ := p.Counts()
				if success.Load() != 1 || n != 1 {
					t.Fatal("concurrent replay", success.Load(), n)
				}
			}
		})
	}
}
func TestOIDCDiscoveryFailureRecoveryAndRestart(t *testing.T) {
	s, p, db := oidcFixture(t)
	if requests, _, _ := p.Counts(); requests != 0 {
		t.Fatal("startup network request")
	}
	for _, f := range []oidctest.Faults{{Unavailable: true}, {DiscoveryIssuer: "https://wrong.test/"}} {
		p.SetFaults(f)
		if _, _, err := s.Begin(t.Context(), ""); err == nil {
			t.Fatal("bad discovery accepted")
		}
	}
	p.SetFaults(oidctest.Faults{})
	state, browser, code := oidcFlow(t, s)
	// A fresh service instance has no cached provider or wake state.
	restarted := NewOIDC(db, s.cfg, "https://drop.test")
	if err := restarted.Complete(t.Context(), state, browser, code, false); err != nil {
		t.Fatal(err)
	}
	p.SetFaults(oidctest.Faults{Unavailable: true})
	if s.Complete(t.Context(), randomToken(), randomToken(), "private-code", false) == nil {
		t.Fatal("missing state accepted")
	}
}
func TestOIDCTransactionBoundsAndReplacement(t *testing.T) {
	s, _, db := oidcFixture(t)
	_, browser, _ := s.Begin(t.Context(), "")
	_, next, err := s.Begin(t.Context(), browser)
	if err != nil || next == browser {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM oidc_transactions").Scan(&count)
	if count != 1 {
		t.Fatal("old browser transaction retained")
	}
	_, err = db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<511) INSERT INTO oidc_transactions SELECT 'test-'||x,'nonce','verifier','browser',0,? FROM n`, time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Begin(t.Context(), ""); err == nil {
		t.Fatal("unbounded transactions")
	}
	db.Exec("UPDATE oidc_transactions SET expires_at=0")
	if _, _, err = s.Begin(t.Context(), ""); err != nil {
		t.Fatal("expired entries not cleaned", err)
	}
	db.QueryRow("SELECT count(*) FROM oidc_transactions").Scan(&count)
	if count != 1 {
		t.Fatal("expired entries retained")
	}
	var schema string
	db.QueryRow("SELECT sql FROM sqlite_master WHERE name='oidc_transactions'").Scan(&schema)
	for _, secret := range []string{"access_token", "refresh_token", "id_token", "authorization_code"} {
		if strings.Contains(schema, secret) {
			t.Fatal("provider token column")
		}
	}
}
