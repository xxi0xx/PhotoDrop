package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func master(fill byte) []byte { return bytes.Repeat([]byte{fill}, keyBytes) }

func writeKey(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

var binding = Binding{AccountID: 7, Provider: "immich", TargetKey: "home", RemoteUserID: "user-1", RemoteCredentialID: "key-1"}

func TestRoundTripAndTampering(t *testing.T) {
	ring, err := FromKeys(master(1))
	if err != nil {
		t.Fatal(err)
	}
	aad, err := AAD(binding)
	if err != nil {
		t.Fatal(err)
	}
	keyID, nonce, ciphertext, err := ring.Encrypt(aad, []byte("immich-api-key-secret"))
	if err != nil || keyID != ring.ActiveKeyID() || len(nonce) != 12 || bytes.Contains(ciphertext, []byte("immich-api-key-secret")) {
		t.Fatal("encrypt", keyID, err)
	}
	plaintext, err := ring.Decrypt(keyID, nonce, ciphertext, aad)
	if err != nil || string(plaintext) != "immich-api-key-secret" {
		t.Fatal("decrypt", err)
	}
	_, nonce2, _, _ := ring.Encrypt(aad, []byte("immich-api-key-secret"))
	if bytes.Equal(nonce, nonce2) {
		t.Fatal("nonce reused")
	}
	other := binding
	other.AccountID = 8
	otherAAD, _ := AAD(other)
	tampered := bytes.Clone(ciphertext)
	tampered[0] ^= 1
	badNonce := bytes.Clone(nonce)
	badNonce[0] ^= 1
	for name, try := range map[string]func() ([]byte, error){
		"ciphertext copied to another account": func() ([]byte, error) { return ring.Decrypt(keyID, nonce, ciphertext, otherAAD) },
		"tampered ciphertext":                  func() ([]byte, error) { return ring.Decrypt(keyID, nonce, tampered, aad) },
		"wrong nonce":                          func() ([]byte, error) { return ring.Decrypt(keyID, badNonce, ciphertext, aad) },
		"short nonce":                          func() ([]byte, error) { return ring.Decrypt(keyID, nonce[:8], ciphertext, aad) },
		"truncated ciphertext":                 func() ([]byte, error) { return ring.Decrypt(keyID, nonce, ciphertext[:4], aad) },
	} {
		if out, err := try(); !errors.Is(err, ErrDecrypt) || out != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := ring.Decrypt("0123456789abcdef", nonce, ciphertext, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatal("unknown key id", err)
	}
	var none *Keyring
	if _, err := none.Decrypt(keyID, nonce, ciphertext, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatal("nil keyring", err)
	}
	if _, _, _, err := none.Encrypt(aad, []byte("x")); !errors.Is(err, ErrNoKey) {
		t.Fatal("nil keyring encrypted", err)
	}
	for _, size := range []int{0, MaxPlaintext + 1} {
		if _, _, _, err := ring.Encrypt(aad, make([]byte, size)); err == nil {
			t.Fatal("plaintext size accepted", size)
		}
	}
}

func TestPreviousKeysDecryptOnly(t *testing.T) {
	old, _ := FromKeys(master(1))
	aad, _ := AAD(binding)
	oldID, nonce, ciphertext, err := old.Encrypt(aad, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	ring, err := FromKeys(master(2), master(1))
	if err != nil {
		t.Fatal(err)
	}
	if ring.ActiveKeyID() == oldID || !ring.Has(oldID) {
		t.Fatal("previous key not loaded as decrypt-only")
	}
	if plaintext, err := ring.Decrypt(oldID, nonce, ciphertext, aad); err != nil || string(plaintext) != "secret" {
		t.Fatal("previous key cannot decrypt", err)
	}
	newID, _, _, err := ring.Encrypt(aad, []byte("secret"))
	if err != nil || newID != ring.ActiveKeyID() || newID == oldID {
		t.Fatal("encryption must use the active key", newID, err)
	}
}

// The derivation, cipher and AAD layout are pinned: changing any of them would
// make every stored credential unreadable. The expected values were computed
// independently with Node.js crypto (hkdfSync + aes-256-gcm).
func TestKnownAnswer(t *testing.T) {
	m := make([]byte, keyBytes)
	for i := range m {
		m[i] = byte(i)
	}
	if got := KeyID(m); got != "97997a3134f9ceb5" {
		t.Fatalf("key id %s", got)
	}
	ring, _ := FromKeys(m)
	aad, _ := AAD(binding)
	if string(aad) != "photodrop/destination-credential/v1|7|immich|home|user-1|key-1" {
		t.Fatalf("AAD %q", aad)
	}
	nonce := make([]byte, nonceBytes)
	sealed := ring.keys[ring.active].aead.Seal(nil, nonce, []byte("vector"), aad)
	if got := hex.EncodeToString(sealed); got != "848f9858ceccf912c1dcc62c3ca33eba251c4dd14938" {
		t.Fatalf("ciphertext %s", got)
	}
}

func TestAADRejectsAmbiguousFields(t *testing.T) {
	for name, mutate := range map[string]func(*Binding){
		"zero account":        func(b *Binding) { b.AccountID = 0 },
		"empty provider":      func(b *Binding) { b.Provider = "" },
		"separator in key":    func(b *Binding) { b.TargetKey = "home|x" },
		"separator in user":   func(b *Binding) { b.RemoteUserID = "a|b" },
		"control character":   func(b *Binding) { b.RemoteCredentialID = "key\n1" },
		"empty credential":    func(b *Binding) { b.RemoteCredentialID = "" },
		"oversized field":     func(b *Binding) { b.RemoteUserID = strings.Repeat("u", 513) },
		"delete control char": func(b *Binding) { b.TargetKey = "home\x7f" },
	} {
		b := binding
		mutate(&b)
		if _, err := AAD(b); !errors.Is(err, ErrAAD) {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestLoadKeyFiles(t *testing.T) {
	raw := master(9)
	valid := []string{
		base64.StdEncoding.EncodeToString(raw) + "\n",
		base64.RawStdEncoding.EncodeToString(raw),
		"  " + base64.URLEncoding.EncodeToString(raw) + "\r\n",
		base64.RawURLEncoding.EncodeToString(raw),
	}
	for _, contents := range valid {
		ring, err := Load(writeKey(t, contents, 0o400), nil)
		if err != nil || !ring.Configured() || ring.ActiveKeyID() != KeyID(raw) {
			t.Fatalf("valid key file %q rejected: %v", contents, err)
		}
	}
	malformed := map[string]string{
		"empty":          "",
		"not base64":     "this is not base64 at all!",
		"16 bytes":       base64.StdEncoding.EncodeToString(raw[:16]),
		"33 bytes":       base64.StdEncoding.EncodeToString(append(bytes.Clone(raw), 1)),
		"two keys":       base64.StdEncoding.EncodeToString(raw) + "\n" + base64.StdEncoding.EncodeToString(raw),
		"oversized file": strings.Repeat("A", maxKeyFile+1),
	}
	for name, contents := range malformed {
		_, err := Load(writeKey(t, contents, 0o400), nil)
		if err == nil {
			t.Fatalf("%s key file accepted", name)
		}
		if contents != "" && len(contents) < maxKeyFile && strings.Contains(err.Error(), strings.TrimSpace(contents)) {
			t.Fatalf("%s: error echoes the file contents", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing key file accepted")
	}
	if _, err := Load(t.TempDir(), nil); err == nil {
		t.Fatal("directory accepted as key file")
	}
	ring, err := Load("", nil)
	if err != nil || ring.Configured() || ring.ActiveKeyID() != "" {
		t.Fatal("no key must load an empty keyring", err)
	}
	if _, err := Load("", []string{writeKey(t, valid[0], 0o400)}); err == nil {
		t.Fatal("previous keys without an active key accepted")
	}
	active := writeKey(t, valid[0], 0o400)
	if _, err := Load(active, []string{writeKey(t, valid[1], 0o400)}); err == nil {
		t.Fatal("active key repeated as a previous key accepted")
	}
	previous := writeKey(t, base64.StdEncoding.EncodeToString(master(3)), 0o400)
	ring, err = Load(active, []string{previous})
	if err != nil || !ring.Has(KeyID(master(3))) || ring.ActiveKeyID() != KeyID(raw) {
		t.Fatal("previous key file", err)
	}
	if _, err := Load(active, []string{writeKey(t, "garbage", 0o400)}); err == nil {
		t.Fatal("malformed previous key file accepted")
	}
}

func TestOverPermissiveKeyFileRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	contents := base64.StdEncoding.EncodeToString(master(4))
	for _, mode := range []os.FileMode{0o440, 0o404, 0o600 | 0o020, 0o644, 0o666} {
		if _, err := Load(writeKey(t, contents, mode), nil); err == nil || !strings.Contains(err.Error(), "chmod 400") {
			t.Fatalf("mode %o accepted: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o400, 0o600} {
		if _, err := Load(writeKey(t, contents, mode), nil); err != nil {
			t.Fatalf("mode %o rejected: %v", mode, err)
		}
	}
}

func TestKeyringRedactsItself(t *testing.T) {
	raw := master(5)
	ring, _ := FromKeys(raw)
	for _, text := range []string{ring.String(), ring.GoString(), fmt.Sprint(ring), fmt.Sprintf("%v %+v %#v %s", ring, ring, ring, ring)} {
		for _, secret := range []string{string(raw), hex.EncodeToString(raw), base64.StdEncoding.EncodeToString(raw), ring.ActiveKeyID()} {
			if strings.Contains(text, secret) {
				t.Fatalf("keyring formatting leaked key material: %q", text)
			}
		}
	}
}
