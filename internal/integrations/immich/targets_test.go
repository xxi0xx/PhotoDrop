package immich

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photodrop/internal/config"
	"photodrop/internal/database"
)

func TestTargetIdentityAndSecrets(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ImmichTarget: "home", ImmichTargets: map[string]config.Immich{"home": {URL: "https://PHOTOS.EXAMPLE:443/", APIKey: "target-secret-never-persist"}}}
	one, err := Reconcile(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	target, err := one.ByKey("")
	if err != nil || target.client == nil {
		t.Fatal(err)
	}
	cfg.ImmichTargets["home"] = config.Immich{URL: "https://photos.example", APIKey: "rotated-runtime-secret", PublicURL: "https://browser.example"}
	two, err := Reconcile(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := two.ByKey("home")
	if same.ID != target.ID || same.PublicURL != "https://browser.example" {
		t.Fatal("normalized target changed identity")
	}
	cfg.ImmichTargets["home"] = config.Immich{URL: "https://another.example", APIKey: "secret"}
	if _, err := Reconcile(t.Context(), db, cfg); err == nil {
		t.Fatal("changed server accepted")
	}
	cfg.ImmichTargets = nil
	missing, err := Reconcile(t.Context(), db, cfg)
	if err != nil {
		t.Fatal("historical credentials blocked startup", err)
	}
	if gone, err := missing.ByKey("home"); err != nil || gone.client != nil {
		t.Fatal("historical target kept a client", err)
	}
	b, _ := json.Marshal(target)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "photos.example") {
		t.Fatal("response leaked target secrets/internals")
	}
	db.Close()
	disk, err := os.ReadFile(filepath.Join(dir, "photodrop.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"target-secret-never-persist", "rotated-runtime-secret", "browser.example"} {
		if strings.Contains(string(disk), secret) {
			t.Fatal("secret persisted")
		}
	}
}

// Reconcile's config-account rules: a configured key creates host 1's config
// account once; a URL-only target gets none and stays linkable; a removed key
// leaves the row unchanged but unusable; and a key configured where host 1 has
// already linked an account is ignored.
func TestReconcileConfigAccounts(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts := func(key string) (count int, status string) {
		t.Helper()
		db.QueryRow(`SELECT count(*), COALESCE(max(a.status),'') FROM destination_accounts a JOIN immich_targets t ON t.id=a.immich_target_id WHERE t.key=? AND a.credential_source='config' AND a.host_id=1`, key).Scan(&count, &status)
		return
	}
	cfg := config.Config{ImmichTarget: "home", ImmichTargets: map[string]config.Immich{
		"home":      {URL: "https://home.example", APIKey: "home-secret"},
		"link-only": {URL: "https://link.example"},
	}}
	for range 2 {
		if _, err := Reconcile(t.Context(), db, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if n, status := accounts("home"); n != 1 || status != "active" {
		t.Fatal("configured key must create exactly one config account", n, status)
	}
	if n, _ := accounts("link-only"); n != 0 {
		t.Fatal("URL-only target got a config account")
	}
	s := New(db, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	removed := cfg
	removed.ImmichTargets = map[string]config.Immich{"link-only": {URL: "https://link.example"}}
	targets, err := Reconcile(t.Context(), db, removed)
	if err != nil {
		t.Fatal(err)
	}
	s.targets = targets
	if n, status := accounts("home"); n != 1 || status != "active" {
		t.Fatal("a missing key changed the config account row", n, status)
	}
	catalog, err := s.TargetCatalog(t.Context(), 1)
	if err != nil || len(catalog.Targets) != 1 || catalog.Targets[0].Available || catalog.Targets[0].Account == nil || catalog.ActiveTarget != "" {
		t.Fatalf("missing key must leave the target visible but unavailable: %+v %v", catalog, err)
	}
	// Host 1 links its own account on the link-only server, then a key appears.
	if _, err := db.Exec(`INSERT INTO destination_accounts(provider,host_id,immich_target_id,credential_source,remote_user_id,status,created_at,updated_at)
	    SELECT 'immich',1,id,'linked','admin-user','active','now','now' FROM immich_targets WHERE key='link-only'`); err != nil {
		t.Fatal(err)
	}
	withKey := cfg
	withKey.ImmichTargets = map[string]config.Immich{"home": cfg.ImmichTargets["home"], "link-only": {URL: "https://link.example", APIKey: "late-secret"}}
	targets, err = Reconcile(t.Context(), db, withKey)
	if err != nil || len(targets.Ignored) != 1 || targets.Ignored[0] != "link-only" {
		t.Fatal("key must be ignored where host 1 has linked", targets, err)
	}
	if n, _ := accounts("link-only"); n != 0 {
		t.Fatal("config account created next to host 1's linked account")
	}
	// Other hosts see no targets until they have an account.
	s.targets = targets
	if other, err := s.TargetCatalog(t.Context(), 2); err != nil || len(other.Targets) != 0 || other.ActiveTarget != "" {
		t.Fatalf("host without accounts saw targets: %+v", other)
	}
}
