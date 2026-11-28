package auth

import (
	"database/sql"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"photodrop/internal/database"
	"photodrop/internal/testutil/oidctest"
)

func hostsDB(t *testing.T) (*sql.DB, *Manager) {
	t.Helper()
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := initialize(t.Context(), db, "host-test-password", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return db, m
}

func TestOIDCRoleDerivationAdminWinsAndProfileClaims(t *testing.T) {
	s, p, _ := oidcFixture(t)
	s.cfg.AllowedSubjects, s.cfg.AllowedGroups = nil, []string{"photodrop-admins"}
	s.cfg.HostSubjects, s.cfg.HostGroups = []string{"listed-host"}, []string{"photodrop-hosts"}
	for _, tc := range []struct {
		name  string
		fault oidctest.Faults
		role  string // "" means denied
		label string
	}{
		{"admin group", oidctest.Faults{Groups: []string{"photodrop-admins"}, Name: "Ada Admin", Email: "ada@example.test", EmailVerified: true}, RoleAdmin, "Ada Admin"},
		{"admin wins over host", oidctest.Faults{Groups: []string{"photodrop-hosts", "photodrop-admins"}}, RoleAdmin, "Host"},
		{"host group", oidctest.Faults{Subject: "host-a", Groups: []string{"photodrop-hosts"}, PreferredUsername: "hosta"}, RoleHost, "hosta"},
		{"host subject", oidctest.Faults{Subject: "listed-host", Groups: []string{}, Email: "listed@example.test", EmailVerified: true}, RoleHost, "listed@example.test"},
		{"unverified email is dropped", oidctest.Faults{Subject: "listed-host", Groups: []string{}, Email: "unverified@example.test"}, RoleHost, "Host"},
		{"no list matches", oidctest.Faults{Subject: "stranger", Groups: []string{"other"}}, "", ""},
		{"removed from every group", oidctest.Faults{Subject: "host-a", Groups: []string{}}, "", ""},
	} {
		p.SetFaults(tc.fault)
		state, browser, code := oidcFlow(t, s)
		id, err := s.Complete(t.Context(), state, browser, code, false)
		if tc.role == "" {
			if !errors.Is(err, ErrOIDC) || id.Role != "" {
				t.Fatalf("%s: expected denial, got %+v %v", tc.name, id, err)
			}
			continue
		}
		if err != nil || id.Role != tc.role || id.Name != tc.label || id.Issuer != s.cfg.Issuer || id.Subject == "" {
			t.Fatalf("%s: got %+v %v", tc.name, id, err)
		}
		if wantEmail := map[bool]string{true: tc.fault.Email}[tc.fault.EmailVerified]; id.Email != wantEmail {
			t.Fatalf("%s: email %q", tc.name, id.Email)
		}
	}
}

func TestDisplayNameFallbacks(t *testing.T) {
	for _, tc := range []struct{ name, preferred, email, want string }{
		{"  Grace  ", "g", "g@x.test", "Grace"},
		{"", "grace", "g@x.test", "grace"},
		{"\x00\x01", "", "g@x.test", "g@x.test"},
		{"", "", "", "Host"},
	} {
		if got := DisplayName(tc.name, tc.preferred, tc.email); got != tc.want {
			t.Fatalf("DisplayName(%q,%q,%q) = %q", tc.name, tc.preferred, tc.email, got)
		}
	}
	long := make([]rune, 250)
	for i := range long {
		long[i] = 'é'
	}
	if got := []rune(DisplayName(string(long), "", "")); len(got) != 200 {
		t.Fatal("display name not bounded to 200 characters", len(got))
	}
}

func TestResolveHostClaimsHostOneOnceThenCreatesHosts(t *testing.T) {
	db, m := hostsDB(t)
	const issuer = "https://idp.test/application/o/drop/"
	first, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "admin-a", Email: "a@example.test", Name: "Admin A", Role: RoleAdmin})
	if err != nil || first != PasswordHostID {
		t.Fatal("first admin login did not claim host 1", first, err)
	}
	again, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "admin-a", Name: "Renamed", Role: RoleAdmin})
	if err != nil || again != PasswordHostID {
		t.Fatal("repeat login moved the admin", again, err)
	}
	second, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "admin-b", Name: "Admin B", Role: RoleAdmin})
	if err != nil || second == PasswordHostID {
		t.Fatal("a second admin claimed host 1", second, err)
	}
	host, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "host-c", Name: "Host C", Role: RoleHost})
	if err != nil || host == PasswordHostID || host == second {
		t.Fatal("new host", host, err)
	}
	var name, role string
	var email *string
	if err := db.QueryRow("SELECT display_name, role, email FROM hosts WHERE id = 1").Scan(&name, &role, &email); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed" || role != RoleAdmin || email != nil {
		t.Fatal("login did not refresh host 1", name, role, email)
	}
	// A host-role identity never claims host 1, even when host 1 is unclaimed.
	db2, m2 := hostsDB(t)
	id, err := m2.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "only-host", Role: RoleHost})
	var claimed *string
	db2.QueryRow("SELECT oidc_subject FROM hosts WHERE id = 1").Scan(&claimed)
	if err != nil || id == PasswordHostID || claimed != nil {
		t.Fatal("host-role identity claimed host 1")
	}
	// Role is re-derived at every login.
	if _, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "host-c", Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT role FROM hosts WHERE id = ?", host).Scan(&role)
	if role != RoleAdmin {
		t.Fatal("role not updated on login")
	}
	if _, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "host-c", Role: "owner"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("unknown role accepted")
	}
	if _, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Role: RoleHost}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("empty subject accepted")
	}
}

func TestHostSessionsDisableAndPasswordRotation(t *testing.T) {
	db, m := hostsDB(t)
	const issuer = "https://idp.test/"
	hostID, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "host-x", Name: "Host X", Role: RoleHost})
	if err != nil {
		t.Fatal(err)
	}
	hostToken, hostSession, err := m.CreateSession(t.Context(), "", hostID)
	if err != nil || hostSession.Host != (SessionHost{hostID, RoleHost, "Host X"}) || hostSession.Admin() {
		t.Fatal("host session", hostSession, err)
	}
	adminToken, adminSession, err := m.Login(t.Context(), "host-test-password", "")
	if err != nil || adminSession.Host.ID != PasswordHostID || !adminSession.Admin() {
		t.Fatal("password login must sign in host 1 as admin", adminSession, err)
	}
	if looked, err := m.Lookup(t.Context(), hostToken); err != nil || looked.Host.ID != hostID || looked.Host.Role != RoleHost {
		t.Fatal("lookup lost the host", looked, err)
	}
	if err := m.SetDisabled(t.Context(), hostID, hostID, true); !errors.Is(err, ErrSelf) {
		t.Fatal("self-disable allowed")
	}
	if err := m.SetDisabled(t.Context(), PasswordHostID, 999, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unknown host", err)
	}
	if err := m.SetDisabled(t.Context(), PasswordHostID, hostID, true); err != nil {
		t.Fatal(err)
	}
	var remaining int
	db.QueryRow("SELECT count(*) FROM host_sessions WHERE host_id = ?", hostID).Scan(&remaining)
	if _, err := m.Lookup(t.Context(), hostToken); !errors.Is(err, ErrUnauthorized) || remaining != 0 {
		t.Fatal("disabling did not end the host's sessions")
	}
	if _, _, err := m.CreateSession(t.Context(), "", hostID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("disabled host got a session")
	}
	if _, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "host-x", Role: RoleHost}); !errors.Is(err, ErrHostDisabled) {
		t.Fatal("disabled host resolved", err)
	}
	if err := m.SetDisabled(t.Context(), PasswordHostID, hostID, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateSession(t.Context(), "", hostID); err != nil {
		t.Fatal("re-enabled host cannot sign in", err)
	}
	// A password change revokes only host 1's sessions.
	if _, err := initialize(t.Context(), db, "rotated-host-test-password", bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	var admin, others int
	db.QueryRow("SELECT count(*) FROM host_sessions WHERE host_id = 1").Scan(&admin)
	db.QueryRow("SELECT count(*) FROM host_sessions WHERE host_id <> 1").Scan(&others)
	if _, err := m.Lookup(t.Context(), adminToken); !errors.Is(err, ErrUnauthorized) || admin != 0 || others != 1 {
		t.Fatal("password rotation revoked the wrong sessions", admin, others)
	}
	hosts, err := m.Hosts(t.Context())
	if err != nil || len(hosts) != 2 || !hosts[0].Password || hosts[0].OIDC || !hosts[1].OIDC || hosts[1].Password || hosts[1].DisplayName != "Host X" {
		t.Fatalf("host list %+v %v", hosts, err)
	}
}

// While password sign-in is enabled, host 1 stays the administrator even if its
// OIDC identity is later only in the host lists. Without a password, the role
// follows the identity like any other host.
func TestHostOneKeepsAdminWhilePasswordSignInIsEnabled(t *testing.T) {
	db, m := hostsDB(t)
	const issuer = "https://idp.test/"
	if id, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "owner", Role: RoleAdmin}); err != nil || id != PasswordHostID {
		t.Fatal("claim", id, err)
	}
	role := func() string {
		var r string
		db.QueryRow("SELECT role FROM hosts WHERE id = 1").Scan(&r)
		return r
	}
	if _, err := m.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "owner", Role: RoleHost}); err != nil || role() != RoleAdmin {
		t.Fatal("host 1 lost admin while password sign-in is enabled", role(), err)
	}
	if _, session, err := m.Login(t.Context(), "host-test-password", ""); err != nil || !session.Admin() {
		t.Fatal("password sign-in is not admin", session.Host, err)
	}
	oidcOnly := NewSessionOnly(db)
	if _, err := oidcOnly.ResolveHost(t.Context(), Identity{Issuer: issuer, Subject: "owner", Role: RoleHost}); err != nil || role() != RoleHost {
		t.Fatal("without password sign-in, host 1's role must follow its identity", role(), err)
	}
}
