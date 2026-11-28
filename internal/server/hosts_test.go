package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"photodrop/internal/auth"
	"photodrop/internal/events"
	"photodrop/internal/testutil/oidctest"
)

type actor struct {
	cookie  *http.Cookie
	session auth.Session
}

func newHostsFixture(t *testing.T) *oidcHTTPFixture {
	t.Helper()
	f := newOIDCHTTP(t, "password+oidc")
	f.cfg.OIDC.HostGroups = []string{"photodrop-hosts"}
	f.restart()
	return f
}

// signIn completes one OIDC sign-in as the identity the provider is told to issue.
func (f *oidcHTTPFixture) signIn(fault oidctest.Faults) actor {
	f.t.Helper()
	f.p.SetFaults(fault)
	callback, binding := f.authorize()
	w := f.call("GET", callback, "", "", "", binding)
	if w.Code != 303 || w.Header().Get("Location") != "/admin" {
		f.t.Fatal("sign-in failed", w.Code, w.Header().Get("Location"))
	}
	c := findCookie(f.t, w, sessionCookie)
	var s auth.Session
	if w := f.call("GET", "/api/admin/session", "", "", "", c); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
		f.t.Fatal("session lookup failed")
	}
	return actor{c, s}
}

func (f *oidcHTTPFixture) as(a actor, method, path, body string) (int, string) {
	f.t.Helper()
	w := f.call(method, path, body, a.session.CSRFToken, f.cfg.BaseURL, a.cookie)
	return w.Code, w.Body.String()
}

func (f *oidcHTTPFixture) createEvent(a actor, name string) events.Event {
	f.t.Helper()
	code, body := f.as(a, "POST", "/api/admin/events", `{"name":"`+name+`","enabled":true}`)
	var out struct{ Event events.Event }
	if code != 201 || json.Unmarshal([]byte(body), &out) != nil {
		f.t.Fatal("create event", code, body)
	}
	return out.Event
}

func routePath(pattern string, id int64) string {
	return strings.Replace(pattern, "{id}", strconv.FormatInt(id, 10), 1)
}

func routeBody(method string) string {
	if method == "GET" || method == "DELETE" {
		return ""
	}
	return `{}`
}

func TestEventRoutesAreScopedToTheOwnerHost(t *testing.T) {
	f := newHostsFixture(t)
	admin := f.signIn(oidctest.Faults{Name: "The Admin"})
	hostA := f.signIn(oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}, Name: "Host A"})
	hostB := f.signIn(oidctest.Faults{Subject: "host-b", Groups: []string{"photodrop-hosts"}, Name: "Host B"})
	if admin.session.Host != (auth.SessionHost{ID: auth.PasswordHostID, Role: auth.RoleAdmin, DisplayName: "The Admin"}) {
		t.Fatal("first admin sign-in did not claim host 1", admin.session.Host)
	}
	if hostA.session.Host.Role != auth.RoleHost || hostA.session.Host.DisplayName != "Host A" || hostA.session.Host.ID == hostB.session.Host.ID {
		t.Fatal("host sessions", hostA.session.Host, hostB.session.Host)
	}
	eventA := f.createEvent(hostA, "Event A")
	adminEvent := f.createEvent(admin, "Admin event")
	if eventA.Owner.ID != hostA.session.Host.ID || eventA.Owner.DisplayName != "Host A" || adminEvent.Owner.ID != auth.PasswordHostID {
		t.Fatal("POST did not set the owner to the caller", eventA.Owner, adminEvent.Owner)
	}

	list := func(a actor) []events.Event {
		t.Helper()
		code, body := f.as(a, "GET", "/api/admin/events", "")
		var out struct{ Events []events.Event }
		if code != 200 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatal("list", code, body)
		}
		return out.Events
	}
	if got := list(hostA); len(got) != 1 || got[0].ID != eventA.ID {
		t.Fatal("host A list", got)
	}
	if got := list(hostB); len(got) != 0 {
		t.Fatal("host B saw another host's events", got)
	}
	if got := list(admin); len(got) != 2 {
		t.Fatal("admin list", got)
	}

	// Every event route: another host's event looks exactly like a missing one.
	missing := int64(999999)
	routes := (&application{}).eventRoutes()
	if len(routes) < 10 {
		t.Fatal("route table shrank", len(routes))
	}
	for _, route := range routes {
		if route.method == "DELETE" {
			continue // Exercised last, below, because it destroys the event.
		}
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			wantCode, wantBody := f.as(hostB, route.method, routePath(route.path, missing), routeBody(route.method))
			if route.admin {
				if code, _ := f.as(hostA, route.method, routePath(route.path, eventA.ID), routeBody(route.method)); code != 403 {
					t.Fatal("host reached an admin-only route", code)
				}
				if code, body := f.as(admin, route.method, routePath(route.path, missing), routeBody(route.method)); code != 404 || !strings.Contains(body, "Event not found") {
					t.Fatal("admin route did not 404 a missing event", code, body)
				}
				return
			}
			if wantCode != 404 || !strings.Contains(wantBody, "Event not found") {
				t.Fatal("missing event response", wantCode, wantBody)
			}
			code, body := f.as(hostB, route.method, routePath(route.path, eventA.ID), routeBody(route.method))
			if code != wantCode || body != wantBody {
				t.Fatal("another host's event was distinguishable from a missing one", code, body)
			}
			for name, a := range map[string]actor{"owner": hostA, "admin": admin} {
				if code, body := f.as(a, route.method, routePath(route.path, eventA.ID), routeBody(route.method)); code == 403 || body == wantBody {
					t.Fatalf("%s was refused: %d %s", name, code, body)
				}
			}
		})
	}
	if code, _ := f.as(hostB, "DELETE", routePath("/api/admin/events/{id}", eventA.ID), ""); code != 404 {
		t.Fatal("host deleted another host's event", code)
	}
	if code, _ := f.as(hostA, "GET", routePath("/api/admin/events/{id}", eventA.ID), ""); code != 200 {
		t.Fatal("event vanished after a refused delete", code)
	}
	if code, _ := f.as(hostA, "DELETE", routePath("/api/admin/events/{id}", eventA.ID), ""); code != 204 {
		t.Fatal("owner could not delete", code)
	}
	// Admins may delete any host's event.
	eventB := f.createEvent(hostB, "Event B")
	if code, _ := f.as(admin, "DELETE", routePath("/api/admin/events/{id}", eventB.ID), ""); code != 204 {
		t.Fatal("admin could not delete a host's event", code)
	}
}

func TestOwnershipTransferRules(t *testing.T) {
	f := newHostsFixture(t)
	admin := f.signIn(oidctest.Faults{})
	hostA := f.signIn(oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}, Name: "Host A"})
	hostB := f.signIn(oidctest.Faults{Subject: "host-b", Groups: []string{"photodrop-hosts"}, Name: "Host B"})
	e := f.createEvent(hostA, "Transfer")
	path := routePath("/api/admin/events/{id}/owner", e.ID)
	to := func(id int64) string { return `{"host_id":` + strconv.FormatInt(id, 10) + `}` }
	if code, _ := f.as(hostA, "PUT", path, to(hostB.session.Host.ID)); code != 403 {
		t.Fatal("host transferred ownership", code)
	}
	if code, body := f.as(admin, "PUT", path, to(424242)); code != 422 || !strings.Contains(body, "owner_host") {
		t.Fatal("unknown new owner", code, body)
	}
	if code, _ := f.as(admin, "PUT", "/api/admin/hosts/"+strconv.FormatInt(hostB.session.Host.ID, 10), `{"disabled":true}`); code != 204 {
		t.Fatal("disable host B", code)
	}
	if code, body := f.as(admin, "PUT", path, to(hostB.session.Host.ID)); code != 422 || !strings.Contains(body, "owner_host") {
		t.Fatal("transfer to a disabled host", code, body)
	}
	if code, _ := f.as(admin, "PUT", "/api/admin/hosts/"+strconv.FormatInt(hostB.session.Host.ID, 10), `{"disabled":false}`); code != 204 {
		t.Fatal("enable host B", code)
	}
	hostB = f.signIn(oidctest.Faults{Subject: "host-b", Groups: []string{"photodrop-hosts"}, Name: "Host B"})

	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'home','https://immich.test','old')`)
	// A queued job blocks the transfer.
	exec(`INSERT INTO immich_event_imports(id,event_id,target_id,album_name,album_marker,album_state,created_at,updated_at) VALUES(1,?,1,'Transfer','marker-new','new','old','old')`, e.ID)
	exec(`INSERT INTO integration_jobs(event_import_id,status,mode,created_at) VALUES(1,'queued','new','old')`)
	if code, body := f.as(admin, "PUT", path, to(hostB.session.Host.ID)); code != 409 || !strings.Contains(body, "immich_active") {
		t.Fatal("transfer with a queued job", code, body)
	}
	// With the job finished, a 'new' binding is deleted with the transfer.
	exec(`UPDATE integration_jobs SET status='failed'`)
	code, body := f.as(admin, "PUT", path, to(hostB.session.Host.ID))
	var out struct{ Event events.Event }
	if code != 200 || json.Unmarshal([]byte(body), &out) != nil || out.Event.Owner.ID != hostB.session.Host.ID || out.Event.Owner.DisplayName != "Host B" {
		t.Fatal("transfer", code, body)
	}
	var bindings int
	f.db.QueryRow("SELECT count(*) FROM immich_event_imports WHERE event_id = ?", e.ID).Scan(&bindings)
	if bindings != 0 {
		t.Fatal("'new' binding survived the transfer")
	}
	if code, _ := f.as(hostA, "GET", routePath("/api/admin/events/{id}", e.ID), ""); code != 404 {
		t.Fatal("previous owner still sees the event", code)
	}
	if code, _ := f.as(hostB, "GET", routePath("/api/admin/events/{id}", e.ID), ""); code != 200 {
		t.Fatal("new owner cannot see the event", code)
	}
	// Once an album exists in the owner's Immich account, the owner is fixed.
	for _, state := range []string{"creating", "ready"} {
		exec(`DELETE FROM immich_event_imports`)
		exec(`INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,album_state,created_at,updated_at) VALUES(?,1,'Transfer','marker-`+state+`',?,'old','old')`, e.ID, state)
		if code, body := f.as(admin, "PUT", path, to(hostA.session.Host.ID)); code != 409 || !strings.Contains(body, "owner_locked") {
			t.Fatal("transfer with album state", state, code, body)
		}
	}
}

func TestHostAdministrationAndRemovedUsers(t *testing.T) {
	f := newHostsFixture(t)
	password := f.password()
	var passwordSession auth.Session
	if w := f.call("GET", "/api/admin/session", "", "", "", password); json.Unmarshal(w.Body.Bytes(), &passwordSession) != nil || passwordSession.Host.ID != auth.PasswordHostID || !passwordSession.Admin() {
		t.Fatal("password sign-in is not host 1", passwordSession.Host)
	}
	admin := f.signIn(oidctest.Faults{})
	host := f.signIn(oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}, Email: "a@example.test"})
	if admin.session.Host.ID != auth.PasswordHostID {
		t.Fatal("admin did not claim host 1")
	}
	if code, _ := f.as(host, "GET", "/api/admin/hosts", ""); code != 403 {
		t.Fatal("host listed hosts", code)
	}
	if code, _ := f.as(host, "PUT", "/api/admin/hosts/1", `{"disabled":true}`); code != 403 {
		t.Fatal("host disabled the admin", code)
	}
	code, body := f.as(admin, "GET", "/api/admin/hosts", "")
	var out struct{ Hosts []auth.Host }
	if code != 200 || json.Unmarshal([]byte(body), &out) != nil || len(out.Hosts) != 2 || out.Hosts[1].Email == nil || *out.Hosts[1].Email != "a@example.test" {
		t.Fatal("host list", code, body)
	}
	if strings.Contains(body, "host-a") || strings.Contains(body, "stable-admin-subject") || strings.Contains(body, f.p.Issuer) {
		t.Fatal("host list exposed OIDC identifiers", body)
	}
	if code, body := f.as(admin, "PUT", "/api/admin/hosts/1", `{"disabled":true}`); code != 409 || !strings.Contains(body, "host_self") {
		t.Fatal("admin disabled themself", code, body)
	}
	for _, input := range []string{`{}`, `{"disabled":"yes"}`, `{"role":"admin"}`} {
		if code, _ := f.as(admin, "PUT", "/api/admin/hosts/"+strconv.FormatInt(host.session.Host.ID, 10), input); code != 422 && code != 400 {
			t.Fatal("invalid host update accepted", input, code)
		}
	}
	if code, _ := f.as(admin, "PUT", "/api/admin/hosts/424242", `{"disabled":true}`); code != 404 {
		t.Fatal("unknown host", code)
	}
	if code, _ := f.as(admin, "PUT", "/api/admin/hosts/"+strconv.FormatInt(host.session.Host.ID, 10), `{"disabled":true}`); code != 204 {
		t.Fatal("disable host", code)
	}
	if code, _ := f.as(host, "GET", "/api/admin/session", ""); code != 401 {
		t.Fatal("disabled host kept a session", code)
	}
	f.p.SetFaults(oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}})
	callback, binding := f.authorize()
	if w := f.call("GET", callback, "", "", "", binding); w.Header().Get("Location") != "/admin/login?error=oidc" {
		t.Fatal("disabled host signed in again")
	}
	if code, _ := f.as(admin, "PUT", "/api/admin/hosts/"+strconv.FormatInt(host.session.Host.ID, 10), `{"disabled":false}`); code != 204 {
		t.Fatal("enable host", code)
	}
	host = f.signIn(oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}})
	// Removed from every group: the next sign-in is refused, while the existing
	// session lasts until it expires (Disable ends it immediately).
	f.p.SetFaults(oidctest.Faults{Subject: "host-a", Groups: []string{}})
	callback, binding = f.authorize()
	if w := f.call("GET", callback, "", "", "", binding); w.Header().Get("Location") != "/admin/login?error=oidc" {
		t.Fatal("removed user signed in")
	}
	if code, _ := f.as(host, "GET", "/api/admin/session", ""); code != 200 {
		t.Fatal("existing session ended without Disable", code)
	}
	if strings.Contains(f.logs.String(), "a@example.test") {
		t.Fatal("email logged")
	}
}
