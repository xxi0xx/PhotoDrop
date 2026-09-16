package immich

import (
	"encoding/json"
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
	if err != nil || !target.Available {
		t.Fatal(err)
	}
	cfg.ImmichTargets["home"] = config.Immich{URL: "https://photos.example", APIKey: "rotated-runtime-secret"}
	two, err := Reconcile(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := two.ByKey("home")
	if same.ID != target.ID {
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
	if _, err := missing.client(target.ID); err != ErrCredentials {
		t.Fatal(err)
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
	for _, secret := range []string{"target-secret-never-persist", "rotated-runtime-secret"} {
		if strings.Contains(string(disk), secret) {
			t.Fatal("secret persisted")
		}
	}
}
