package immich

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/destinations"
)

func TestProviderValidatesConfigAccounts(t *testing.T) {
	keys := map[string][]string{"good-key": requiredPermissions, "narrow-key": {"album.read"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		if key == "server-error" {
			w.WriteHeader(502)
			return
		}
		permissions, ok := keys[key]
		if !ok {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/api-keys/me":
			jsonResponse(w, map[string]any{"permissions": permissions})
		case "/api/server/version":
			jsonResponse(w, Version{3, 4, 2})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	targets, err := Reconcile(t.Context(), db, config.Config{ImmichTargets: map[string]config.Immich{"home": {URL: server.URL, APIKey: "good-key"}}})
	if err != nil {
		t.Fatal(err)
	}
	home, _ := targets.ByKey("home")
	p := NewProvider(targets)
	account := destinations.Account{ID: 1, Provider: "immich", TargetID: home.ID, Source: destinations.SourceConfig}
	if p.Name() != "immich" {
		t.Fatal(p.Name())
	}
	if status, err := p.Validate(t.Context(), account, []byte("good-key")); err != nil || !status.Valid {
		t.Fatal("valid key", status, err)
	}
	for _, key := range []string{"revoked-key", "narrow-key"} {
		status, err := p.Validate(t.Context(), account, []byte(key))
		if err != nil || status.Valid || status.Reason != ErrAuth.Error() {
			t.Fatal("rejected key must be a status, not an error", key, status, err)
		}
	}
	if status, err := p.Validate(t.Context(), account, []byte("server-error")); err == nil || status.Valid {
		t.Fatal("a server failure must be an error so account state is left alone", status)
	}
	if _, err := p.Validate(t.Context(), destinations.Account{Provider: "immich", TargetID: 999}, []byte("good-key")); !errors.Is(err, ErrCredentials) {
		t.Fatal("unknown target", err)
	}
	if _, err := p.BeginLink(t.Context(), destinations.LinkRequest{}); !errors.Is(err, destinations.ErrLinkUnsupported) {
		t.Fatal("linking must be unavailable until it is built", err)
	}
}
