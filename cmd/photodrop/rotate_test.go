package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photodrop/internal/secrets"
)

func TestRotateSecretsCommand(t *testing.T) {
	t.Setenv("PHOTODROP_DATA_DIR", t.TempDir())
	t.Setenv("PHOTODROP_SECRETS_KEY_FILE", "")
	t.Setenv("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES", "")
	var out bytes.Buffer
	if err := rotateSecrets(t.Context(), nil, &out); !errors.Is(err, secrets.ErrNoKey) || out.Len() != 0 {
		t.Fatal("rotation without a key", err)
	}
	if err := rotateSecrets(t.Context(), []string{"--force"}, &out); err == nil {
		t.Fatal("unexpected argument accepted")
	}
	key := bytes.Repeat([]byte{7}, 32)
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHOTODROP_SECRETS_KEY_FILE", path)
	if err := rotateSecrets(t.Context(), nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.HasPrefix(got, "Linked credentials: 0. Re-encrypted: 0.") || strings.Contains(got, secrets.KeyID(key)) {
		t.Fatalf("unexpected output %q", got)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte("not a key"), 0o400)
	t.Setenv("PHOTODROP_SECRETS_KEY_FILE", bad)
	if err := rotateSecrets(t.Context(), nil, &out); err == nil {
		t.Fatal("malformed key file accepted")
	}
}
