package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"photodrop/internal/config"
	"photodrop/internal/testutil/oidctest"
)

// fakeImmich is an httptest Immich 3.4.2 that implements just enough of the
// OAuth, user and API-key endpoints for the link flow, records every call, and
// can be told to misbehave. The fake authentik step is the test itself: it
// builds the callback query the identity provider would send.
type fakeImmich struct {
	mu sync.Mutex
	t  *testing.T
	// Configuration.
	user                                    immichTestUser
	version                                 string
	badState, badRedirect, badChallenge     bool
	narrowPermissions, wrongSelfID, bigUser bool
	deleteMissing                           bool
	serverError                             bool // /api-keys/me answers 502
	// Observations.
	challenge, redirect, expectedURL string
	tokens                           map[string]bool
	keys                             map[string]string // id -> secret
	logouts, creates, oauthCalls     int
	deleted                          []string
	bodies                           bytes.Buffer
}

type immichTestUser struct{ ID, Email, OAuthID string }

const testUUID = "00000000-0000-4000-8000-%012d"

func newFakeImmich(t *testing.T, user immichTestUser) (*fakeImmich, *httptest.Server) {
	f := &fakeImmich{t: t, user: user, version: `{"major":3,"minor":4,"patch":2}`, tokens: map[string]bool{}, keys: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeImmich) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var in map[string]any
	json.Unmarshal(body, &in)
	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		out, _ := json.Marshal(v)
		f.bodies.Write(out)
		w.Write(out)
	}
	session := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	authed := f.tokens[session]
	switch r.Method + " " + r.URL.Path {
	case "GET /api/server/version":
		w.Write([]byte(f.version))
	case "POST /api/oauth/authorize":
		state, _ := in["state"].(string)
		f.redirect, _ = in["redirectUri"].(string)
		f.challenge, _ = in["codeChallenge"].(string)
		q := url.Values{"state": {state}, "redirect_uri": {f.redirect}, "code_challenge": {f.challenge}, "code_challenge_method": {"S256"}, "client_id": {"immich"}}
		if f.badState {
			q.Set("state", "someone-else")
		}
		if f.badRedirect {
			q.Set("redirect_uri", "https://evil.example/callback")
		}
		if f.badChallenge {
			q.Set("code_challenge", "another-challenge")
		}
		reply(201, map[string]string{"url": "http://127.0.0.1:9/authorize?" + q.Encode()})
	case "POST /api/oauth/callback":
		f.oauthCalls++
		verifier, _ := in["codeVerifier"].(string)
		sum := sha256.Sum256([]byte(verifier))
		got, _ := in["url"].(string)
		if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge || (f.expectedURL != "" && got != f.expectedURL) {
			reply(400, map[string]string{"message": "OAuth authentication failed"})
			return
		}
		token := fmt.Sprintf("immich-session-%d", len(f.tokens)+1)
		f.tokens[token] = true
		reply(201, map[string]any{"accessToken": token, "userId": f.user.ID, "userEmail": f.user.Email})
	case "GET /api/users/me":
		if !authed {
			reply(401, nil)
			return
		}
		user := map[string]any{"id": f.user.ID, "email": f.user.Email, "oauthId": f.user.OAuthID}
		if f.bigUser {
			user["padding"] = strings.Repeat("x", 300<<10)
		}
		reply(200, user)
	case "POST /api/api-keys":
		if !authed {
			reply(401, nil)
			return
		}
		f.creates++
		id := fmt.Sprintf(testUUID, f.creates)
		secret := fmt.Sprintf("immich-api-secret-%d", f.creates)
		f.keys[id] = secret
		permissions := []string{"album.create", "album.read", "asset.upload", "albumAsset.create"}
		if f.narrowPermissions {
			permissions = permissions[:2]
		}
		reply(201, map[string]any{"secret": secret, "apiKey": map[string]any{"id": id, "permissions": permissions}})
	case "GET /api/api-keys/me":
		if f.serverError {
			reply(502, nil)
			return
		}
		for id, secret := range f.keys {
			if r.Header.Get("x-api-key") == secret {
				if f.wrongSelfID {
					id = fmt.Sprintf(testUUID, 999)
				}
				reply(200, map[string]any{"id": id, "permissions": []string{"album.create", "album.read", "asset.upload", "albumAsset.create"}})
				return
			}
		}
		reply(401, nil)
	case "POST /api/auth/logout":
		if authed {
			f.logouts++
			delete(f.tokens, session)
		}
		reply(200, map[string]any{"successful": true})
	default:
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/api-keys/") && authed {
			id := strings.TrimPrefix(r.URL.Path, "/api/api-keys/")
			f.deleted = append(f.deleted, id)
			if _, ok := f.keys[id]; !ok || f.deleteMissing {
				reply(404, nil)
				return
			}
			delete(f.keys, id)
			w.WriteHeader(204)
			return
		}
		f.t.Errorf("unexpected Immich call %s %s", r.Method, r.URL.Path)
		reply(404, nil)
	}
}

func (f *fakeImmich) snapshot() (logouts, creates int, deleted []string, live int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logouts, f.creates, append([]string(nil), f.deleted...), len(f.tokens)
}

type linkFixture struct {
	*oidcHTTPFixture
	immich *fakeImmich
	host   actor
}

// newLinkFixture runs PhotoDrop with a secrets key and one link-only Immich
// target served by the fake, and signs a host in through the OIDC fixture.
func newLinkFixture(t *testing.T, user immichTestUser, match string) *linkFixture {
	t.Helper()
	fake, srv := newFakeImmich(t, user)
	f := newHostsFixture(t)
	key := filepath.Join(t.TempDir(), "secrets-key")
	if err := os.WriteFile(key, []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))), 0o400); err != nil {
		t.Fatal(err)
	}
	f.cfg.Secrets = config.Secrets{KeyFile: key}
	f.cfg.ImmichTargets = map[string]config.Immich{"home": {URL: srv.URL, LinkMatch: match}}
	f.restart()
	host := f.signIn(oidctest.Faults{Subject: "host-subject", Groups: []string{"photodrop-hosts"}, Name: "Riley", Email: "riley@example.test", EmailVerified: true})
	return &linkFixture{oidcHTTPFixture: f, immich: fake, host: host}
}

// start begins a link (or unlink) and returns the state and browser cookie.
func (l *linkFixture) start(path, body string) (state string, browser *http.Cookie, code int, response string) {
	l.t.Helper()
	w := l.call("POST", path, body, l.host.session.CSRFToken, l.cfg.BaseURL, l.host.cookie)
	if w.Code != 200 {
		return "", nil, w.Code, w.Body.String()
	}
	var out struct{ Location string }
	json.Unmarshal(w.Body.Bytes(), &out)
	u, err := url.Parse(out.Location)
	if err != nil {
		l.t.Fatal(err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == linkCookie {
			browser = c
		}
	}
	if browser == nil || !browser.HttpOnly || browser.Path != linkCookiePath || browser.SameSite != http.SameSiteLaxMode || !browser.Secure {
		l.t.Fatal("missing or unsafe link cookie")
	}
	return u.Query().Get("state"), browser, 200, w.Body.String()
}

// finish plays the identity provider's redirect back to PhotoDrop.
func (l *linkFixture) finish(query string, cookies ...*http.Cookie) string {
	l.t.Helper()
	l.immich.mu.Lock()
	l.immich.expectedURL = l.cfg.BaseURL + linkCallbackPath + "?" + query
	l.immich.mu.Unlock()
	w := l.call("GET", linkCallbackPath+"?"+query, "", "", "", cookies...)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "/admin/account?result=") {
		l.t.Fatal("callback did not redirect to the result page", w.Code, w.Header())
	}
	return strings.TrimPrefix(w.Header().Get("Location"), "/admin/account?result=")
}

func (l *linkFixture) link() string {
	l.t.Helper()
	state, browser, code, body := l.start("/api/admin/destinations/immich/link", `{"target":"home"}`)
	if code != 200 {
		l.t.Fatal("link start", code, body)
	}
	return l.finish("code=one-time-code&state="+state+"&iss=http%3A%2F%2Fidp.test", l.host.cookie, browser)
}

func (l *linkFixture) accounts() (out []struct {
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	RemoteEmail string `json:"remote_email"`
	Target      string `json:"target"`
}) {
	l.t.Helper()
	code, body := l.as(l.host, "GET", "/api/admin/destinations", "")
	var list struct {
		Accounts json.RawMessage
	}
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || json.Unmarshal(list.Accounts, &out) != nil {
		l.t.Fatal("list destinations", code, body)
	}
	for _, secret := range []string{"immich-api-secret", "immich-session", "credential_ciphertext", "0123456789abcdef"} {
		if strings.Contains(body, secret) {
			l.t.Fatal("destination list exposed secret material", body)
		}
	}
	return out
}

var matchingUser = immichTestUser{ID: fmt.Sprintf(testUUID, 1001), Email: "riley@example.test", OAuthID: "host-subject"}

func TestLinkStoresAnEncryptedScopedKeyAndLogsOut(t *testing.T) {
	l := newLinkFixture(t, matchingUser, "")
	if got := l.link(); got != "linked" {
		t.Fatal("link result", got)
	}
	logouts, creates, deleted, live := l.immich.snapshot()
	if logouts != 1 || creates != 1 || len(deleted) != 0 || live != 0 {
		t.Fatal("session not logged out or key churn", logouts, creates, deleted, live)
	}
	accounts := l.accounts()
	if len(accounts) != 1 || accounts[0].Source != "linked" || accounts[0].Status != "active" || accounts[0].RemoteEmail != "riley@example.test" {
		t.Fatalf("accounts %+v", accounts)
	}
	var keyID, nonce, ciphertext, credential, user string
	l.db.QueryRow("SELECT credential_key_id, hex(credential_nonce), hex(credential_ciphertext), remote_credential_id, remote_user_id FROM destination_accounts WHERE credential_source='linked'").Scan(&keyID, &nonce, &ciphertext, &credential, &user)
	if keyID == "" || nonce == "" || ciphertext == "" || credential != fmt.Sprintf(testUUID, 1) || user != matchingUser.ID {
		t.Fatal("credential not stored encrypted", keyID, credential, user)
	}
	disk, _ := os.ReadFile(filepath.Join(l.cfg.DataDir, "photodrop.db"))
	if bytes.Contains(disk, []byte("immich-api-secret-1")) || bytes.Contains(disk, []byte("immich-session")) {
		t.Fatal("plaintext key or session token persisted")
	}
	// The host now has a usable target for its own events.
	code, body := l.as(l.host, "GET", "/api/admin/immich/targets", "")
	if code != 200 || !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"source":"linked"`) {
		t.Fatal("linked account not usable", body)
	}
	// Relink replaces the key in place and deletes the old one with the same session.
	if got := l.link(); got != "linked" {
		t.Fatal("relink result", got)
	}
	logouts, creates, deleted, live = l.immich.snapshot()
	if logouts != 2 || creates != 2 || len(deleted) != 1 || deleted[0] != fmt.Sprintf(testUUID, 1) || live != 0 {
		t.Fatal("relink did not delete the old key", logouts, creates, deleted)
	}
	if accounts := l.accounts(); len(accounts) != 1 {
		t.Fatal("relink created a second account", accounts)
	}
	// Unlink deletes the stored key remotely and revokes the account locally.
	id := l.accounts()[0].ID
	state, browser, code2, body2 := l.start(fmt.Sprintf("/api/admin/destinations/%d/unlink", id), "{}")
	if code2 != 200 {
		t.Fatal("unlink start", code2, body2)
	}
	if got := l.finish("code=c&state="+state, l.host.cookie, browser); got != "unlinked" {
		t.Fatal("unlink result", got)
	}
	logouts, _, deleted, live = l.immich.snapshot()
	if logouts != 3 || deleted[len(deleted)-1] != fmt.Sprintf(testUUID, 2) || live != 0 || len(l.accounts()) != 0 {
		t.Fatal("unlink", logouts, deleted, live)
	}
	var status string
	var stored any
	l.db.QueryRow("SELECT status, credential_ciphertext FROM destination_accounts WHERE id = ?", id).Scan(&status, &stored)
	if status != "revoked" || stored != nil {
		t.Fatal("unlinked account kept its credential", status)
	}
	// No secret ever reached logs or responses.
	for _, secret := range []string{"immich-api-secret", "immich-session", "one-time-code"} {
		if strings.Contains(l.logs.String(), secret) {
			t.Fatal("secret material logged:", secret)
		}
	}
}

func TestLinkRefusesWrongPeopleAndBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   immichTestUser
		match  string
		setup  func(*fakeImmich)
		result string
		keyDel bool // a minted key must have been deleted again
	}{
		{"wrong user by subject", immichTestUser{ID: matchingUser.ID, Email: "other@example.test", OAuthID: "someone-else"}, "", nil, "link_wrong_user", false},
		{"subject mismatch with matching email", immichTestUser{ID: matchingUser.ID, Email: "RILEY@example.test", OAuthID: "other-subject-mode"}, "", nil, "link_subject_mismatch", false},
		{"wrong user by email", immichTestUser{ID: matchingUser.ID, Email: "other@example.test", OAuthID: "host-subject"}, "email", nil, "link_wrong_user", false},
		{"email match mode", immichTestUser{ID: matchingUser.ID, Email: "Riley@Example.test", OAuthID: "unrelated"}, "email", nil, "linked", false},
		{"key permissions mismatch", matchingUser, "", func(f *fakeImmich) { f.narrowPermissions = true }, "link_failed", true},
		{"self-check id mismatch", matchingUser, "", func(f *fakeImmich) { f.wrongSelfID = true }, "link_failed", true},
		{"oversized user response", matchingUser, "", func(f *fakeImmich) { f.bigUser = true }, "link_failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLinkFixture(t, tc.user, tc.match)
			if tc.setup != nil {
				tc.setup(l.immich)
			}
			if got := l.link(); got != tc.result {
				t.Fatal("result", got)
			}
			logouts, creates, deleted, live := l.immich.snapshot()
			if logouts != 1 || live != 0 {
				t.Fatal("logout not attempted on this exit path", logouts, live)
			}
			if tc.keyDel && (creates != 1 || len(deleted) != 1) {
				t.Fatal("minted key not deleted after failure", creates, deleted)
			}
			if tc.result != "linked" && (creates > len(deleted) || len(l.accounts()) != 0) {
				t.Fatal("a refused link left a key or an account", creates, deleted)
			}
		})
	}
}

func TestLinkStartValidatesImmichAndTheTarget(t *testing.T) {
	for name, setup := range map[string]func(*fakeImmich){
		"state not echoed":       func(f *fakeImmich) { f.badState = true },
		"redirect_uri changed":   func(f *fakeImmich) { f.badRedirect = true },
		"code_challenge changed": func(f *fakeImmich) { f.badChallenge = true },
		"server below 3.4.2":     func(f *fakeImmich) { f.version = `{"major":3,"minor":4,"patch":1}` },
		"unsupported major":      func(f *fakeImmich) { f.version = `{"major":4,"minor":0,"patch":0}` },
	} {
		t.Run(name, func(t *testing.T) {
			l := newLinkFixture(t, matchingUser, "")
			setup(l.immich)
			_, browser, code, body := l.start("/api/admin/destinations/immich/link", `{"target":"home"}`)
			if code == 200 || browser != nil {
				t.Fatal("link started despite", name, body)
			}
			var pending int
			l.db.QueryRow("SELECT count(*) FROM destination_link_transactions").Scan(&pending)
			if pending != 0 {
				t.Fatal("a transaction was written")
			}
		})
	}
	l := newLinkFixture(t, matchingUser, "")
	if _, _, code, _ := l.start("/api/admin/destinations/immich/link", `{"target":"missing"}`); code != 422 {
		t.Fatal("unknown target", code)
	}
	if code, _ := l.as(l.host, "POST", "/api/admin/destinations/immich/link", `{"target":"home","extra":1}`); code != 400 {
		t.Fatal("unknown field", code)
	}
	if w := l.call("POST", "/api/admin/destinations/immich/link", `{"target":"home"}`, "", l.cfg.BaseURL, l.host.cookie); w.Code != 403 {
		t.Fatal("link without CSRF", w.Code)
	}
	code, body := l.as(l.host, "GET", "/api/admin/destinations", "")
	if code != 200 || !strings.Contains(body, `"linkable":true`) {
		t.Fatal("target not linkable", body)
	}
	l.immich.version = `{"major":3,"minor":2,"patch":1}`
	if _, body := l.as(l.host, "GET", "/api/admin/destinations", ""); !strings.Contains(body, `"reason":"immich_version"`) {
		t.Fatal("old server not reported", body)
	}
}

func TestLinkCallbackRejectsReplayedOrForeignTransactions(t *testing.T) {
	l := newLinkFixture(t, matchingUser, "")
	other := l.signIn(oidctest.Faults{Subject: "other-host", Groups: []string{"photodrop-hosts"}})
	l.host = l.signIn(oidctest.Faults{Subject: "host-subject", Groups: []string{"photodrop-hosts"}, Email: "riley@example.test", EmailVerified: true})
	for _, tc := range []struct {
		name  string
		query func(state string) string
		jar   func(browser *http.Cookie) []*http.Cookie
	}{
		{"no browser cookie", func(s string) string { return "code=c&state=" + s }, func(*http.Cookie) []*http.Cookie { return []*http.Cookie{l.host.cookie} }},
		{"another browser", func(s string) string { return "code=c&state=" + s }, func(b *http.Cookie) []*http.Cookie {
			return []*http.Cookie{l.host.cookie, {Name: linkCookie, Value: strings.Repeat("A", 43)}}
		}},
		{"another session", func(s string) string { return "code=c&state=" + s }, func(b *http.Cookie) []*http.Cookie { return []*http.Cookie{other.cookie, b} }},
		{"duplicate state", func(s string) string { return "code=c&state=" + s + "&state=" + s }, func(b *http.Cookie) []*http.Cookie { return []*http.Cookie{l.host.cookie, b} }},
		{"duplicate code", func(s string) string { return "code=c&code=d&state=" + s }, func(b *http.Cookie) []*http.Cookie { return []*http.Cookie{l.host.cookie, b} }},
		{"provider error", func(s string) string { return "error=access_denied&state=" + s }, func(b *http.Cookie) []*http.Cookie { return []*http.Cookie{l.host.cookie, b} }},
		{"wrong state", func(s string) string { return "code=c&state=" + strings.Repeat("B", 43) }, func(b *http.Cookie) []*http.Cookie { return []*http.Cookie{l.host.cookie, b} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, browser, code, body := l.start("/api/admin/destinations/immich/link", `{"target":"home"}`)
			if code != 200 {
				t.Fatal(code, body)
			}
			if got := l.finish(tc.query(state), tc.jar(browser)...); got != "link_failed" {
				t.Fatal("foreign callback accepted", got)
			}
		})
	}
	// A valid callback works once; replaying it fails.
	state, browser, _, _ := l.start("/api/admin/destinations/immich/link", `{"target":"home"}`)
	query := "code=c&state=" + state
	if got := l.finish(query, l.host.cookie, browser); got != "linked" {
		t.Fatal("valid callback", got)
	}
	if got := l.finish(query, l.host.cookie, browser); got != "link_failed" {
		t.Fatal("replayed callback accepted", got)
	}
	// Expired transactions are refused.
	state, browser, _, _ = l.start("/api/admin/destinations/immich/link", `{"target":"home"}`)
	l.db.Exec("UPDATE destination_link_transactions SET expires_at = 1")
	if got := l.finish("code=c&state="+state, l.host.cookie, browser); got != "link_failed" {
		t.Fatal("expired transaction accepted", got)
	}
	_, creates, _, _ := l.immich.snapshot()
	if creates != 1 {
		t.Fatal("refused callbacks minted keys", creates)
	}
}

func TestLinkConflictsAndForget(t *testing.T) {
	l := newLinkFixture(t, matchingUser, "")
	// Another host already holds this Immich user on the target.
	l.db.Exec(`INSERT INTO hosts(id,role,display_name,oidc_issuer,oidc_subject,created_at) VALUES(50,'host','Other','https://idp.test/','fifty','now')`)
	l.db.Exec(`INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,remote_user_id,status,created_at,updated_at)
	    SELECT 'immich',50,id,'linked',?,'active','now','now' FROM immich_targets WHERE key='home'`, matchingUser.ID)
	if got := l.link(); got != "link_in_use" {
		t.Fatal("in-use user linked", got)
	}
	if _, creates, _, _ := l.immich.snapshot(); creates != 0 {
		t.Fatal("a key was minted for an in-use user")
	}
	// The conflict appears only at store time (a concurrent link): key deleted.
	l.db.Exec("DELETE FROM destination_accounts WHERE host_id = 50")
	l.db.Exec(`CREATE TRIGGER race BEFORE UPDATE OF credential_ciphertext ON destination_accounts
	    BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: destination_accounts.remote_user_id'); END`)
	if got := l.link(); got != "link_in_use" {
		t.Fatal("store-time conflict", got)
	}
	_, creates, deleted, live := l.immich.snapshot()
	if creates != 1 || len(deleted) != 1 || live != 0 || len(l.accounts()) != 0 {
		t.Fatal("store-time conflict left a key or account", creates, deleted, live)
	}
	l.db.Exec("DROP TRIGGER race")
	if got := l.link(); got != "linked" {
		t.Fatal(got)
	}
	// Forget revokes locally and names the key to delete by hand.
	id := l.accounts()[0].ID
	code, body := l.as(l.host, "POST", fmt.Sprintf("/api/admin/destinations/%d/forget", id), "{}")
	if code != 200 || !strings.Contains(body, `"key_name":"PhotoDrop · drop.test · Riley"`) || len(l.accounts()) != 0 {
		t.Fatal("forget", code, body)
	}
	// Someone else's account id is not found.
	stranger := l.signIn(oidctest.Faults{Subject: "stranger", Groups: []string{"photodrop-hosts"}})
	if code, _ := l.as(stranger, "POST", fmt.Sprintf("/api/admin/destinations/%d/unlink", id), "{}"); code != 404 {
		t.Fatal("unlinked another host's account", code)
	}
}

func TestLinkedAccountValidation(t *testing.T) {
	l := newLinkFixture(t, matchingUser, "")
	if l.link() != "linked" {
		t.Fatal("link")
	}
	event := l.createEvent(l.host, "Linked event")
	path := fmt.Sprintf("/api/admin/events/%d/immich/test", event.ID)
	if code, body := l.as(l.host, "POST", path, `{"target":"home"}`); code != 200 {
		t.Fatal("linked account test", code, body)
	}
	// A 5xx leaves the account alone.
	l.immich.mu.Lock()
	l.immich.serverError = true
	l.immich.mu.Unlock()
	if code, _ := l.as(l.host, "POST", path, `{"target":"home"}`); code != 503 {
		t.Fatal("5xx not reported", code)
	}
	var unchanged string
	l.db.QueryRow("SELECT status FROM destination_accounts WHERE credential_source='linked'").Scan(&unchanged)
	if unchanged != "active" {
		t.Fatal("a server error changed account state", unchanged)
	}
	l.immich.mu.Lock()
	l.immich.serverError = false
	l.immich.mu.Unlock()
	// A rejected key (401) marks it needs_relink.
	l.immich.mu.Lock()
	saved := l.immich.keys
	l.immich.keys = map[string]string{}
	l.immich.mu.Unlock()
	if code, _ := l.as(l.host, "POST", path, `{"target":"home"}`); code != 503 {
		t.Fatal("rejected key not reported", code)
	}
	var status, reason string
	l.db.QueryRow("SELECT status, status_reason FROM destination_accounts WHERE credential_source='linked'").Scan(&status, &reason)
	if status != "needs_relink" || reason == "" {
		t.Fatal("401 did not mark needs_relink", status, reason)
	}
	l.immich.mu.Lock()
	l.immich.keys = saved
	l.immich.mu.Unlock()
	if code, _ := l.as(l.host, "GET", "/api/admin/immich/targets", ""); code != 200 {
		t.Fatal(code)
	}
	// Relink restores it.
	if l.link() != "linked" {
		t.Fatal("relink from needs_relink")
	}
	l.db.QueryRow("SELECT status FROM destination_accounts WHERE credential_source='linked' AND status <> 'revoked'").Scan(&status)
	if status != "active" {
		t.Fatal("relink did not reactivate", status)
	}
	_ = slog.Default
}
