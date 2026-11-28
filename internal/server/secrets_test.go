package server

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/secrets"
	"photodrop/web"
)

// Startup: no key is fine, a valid key loads, a malformed or missing key file
// stops startup, and nothing about the key reaches the logs.
func TestStartupSecretsKeyFile(t *testing.T) {
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	master := bytes.Repeat([]byte{3}, 32)
	encoded := base64.StdEncoding.EncodeToString(master)
	dir := t.TempDir()
	write := func(name, contents string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0o400); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good, bad := write("good", encoded+"\n"), write("bad", "not-a-key")
	for _, tc := range []struct {
		name, active string
		previous     []string
		ok           bool
		log          string
	}{
		{"no key", "", nil, true, "secrets key not configured"},
		{"valid key", good, nil, true, "secrets key loaded"},
		{"malformed key", bad, nil, false, ""},
		{"missing key", filepath.Join(dir, "missing"), nil, false, ""},
		{"malformed previous key", good, []string{bad}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := database.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var logs bytes.Buffer
			cfg := config.Config{ListenAddr: ":8080", DataDir: t.TempDir(), AdminPassword: "server-test-password", Secrets: config.Secrets{KeyFile: tc.active, PreviousKeyFiles: tc.previous}}
			_, err = New(t.Context(), cfg, db, assets, slog.New(slog.NewTextHandler(&logs, nil)))
			if (err == nil) != tc.ok {
				t.Fatalf("startup error = %v", err)
			}
			if tc.ok && !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("missing log %q in %s", tc.log, logs.String())
			}
			if err != nil && strings.Contains(err.Error(), "not-a-key") {
				t.Fatal("startup error echoed key file contents")
			}
			for _, secret := range []string{encoded, secrets.KeyID(master)} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("key material or key id logged")
				}
			}
		})
	}
}
