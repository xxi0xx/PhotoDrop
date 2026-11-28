// Package secrets encrypts stored destination credentials under a master key
// that lives in a file outside SQLite, backups of /data, logs and the browser.
//
// Each master key file holds 32 random bytes, base64-encoded. A key's id is the
// first 16 hex characters of SHA-256("photodrop-key-id" ‖ key), so no manual
// numbering is needed. Ciphertexts use AES-256-GCM under a key derived with
// HKDF-SHA256, with a fresh random nonce and additional data (AAD) that binds
// each ciphertext to its database row.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Version names the derivation, cipher and AAD layout used for every ciphertext.
const Version = "photodrop/destination-credential/v1"

const (
	keyBytes   = 32
	nonceBytes = 12
	// maxKeyFile bounds what is read from a key file: base64 of 32 bytes plus
	// generous room for whitespace.
	maxKeyFile = 256
	// MaxPlaintext bounds one stored credential.
	MaxPlaintext = 16 << 10
)

var (
	// ErrUnknownKey means a ciphertext names a key id that no loaded key has.
	ErrUnknownKey = errors.New("credential cannot be decrypted; restore the secrets key or relink")
	// ErrNoKey means no active secrets key is configured.
	ErrNoKey = errors.New("secrets key not configured")
	// ErrDecrypt means authentication failed: wrong key, wrong row, or tampering.
	ErrDecrypt = errors.New("credential could not be decrypted")
	// ErrAAD means an AAD field would make the encoding ambiguous.
	ErrAAD = errors.New("credential binding field is invalid")
)

type key struct {
	id   string
	aead cipher.AEAD
}

// Keyring holds the active key, used for encryption, and retired keys, used for
// decryption only. A nil or empty Keyring has no key and refuses to encrypt.
type Keyring struct {
	active string
	keys   map[string]key
}

func (*Keyring) String() string     { return "secrets keyring (keys redacted)" }
func (k *Keyring) GoString() string { return k.String() }

// KeyID derives the public id of a master key.
func KeyID(master []byte) string {
	sum := sha256.Sum256(append([]byte("photodrop-key-id"), master...))
	return hex.EncodeToString(sum[:])[:16]
}

func newKey(master []byte) (key, error) {
	derived, err := hkdf.Key(sha256.New, master, nil, Version, keyBytes)
	if err != nil {
		return key{}, err
	}
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return key{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return key{}, err
	}
	return key{id: KeyID(master), aead: aead}, nil
}

// ParseKey decodes a key file's contents: 32 bytes in standard or URL-safe
// base64, with or without padding, surrounded by optional whitespace.
func ParseKey(data []byte) ([]byte, error) {
	text := strings.TrimSpace(string(data))
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(text); err == nil {
			if len(decoded) == keyBytes {
				return decoded, nil
			}
			clear(decoded)
		}
	}
	return nil, errors.New("must contain 32 random bytes in base64; generate one with: openssl rand -base64 32")
}

// readKeyFile reads and validates one key file. On Unix-like systems a key file
// readable or writable by group or other is rejected.
func readKeyFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("secrets key file %q: %w", path, errors.Unwrap(err))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secrets key file %q is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets key file %q is accessible to group or other (mode %s); run chmod 400 on it", path, info.Mode().Perm())
	}
	if info.Size() > maxKeyFile {
		return nil, fmt.Errorf("secrets key file %q is too large", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return nil, fmt.Errorf("secrets key file %q: %w", path, err)
	}
	defer clear(data)
	master, err := ParseKey(data)
	if err != nil {
		return nil, fmt.Errorf("secrets key file %q %w", path, err)
	}
	return master, nil
}

// Load reads the active key file and any previous key files. An empty active
// path with no previous paths returns an empty keyring: startup continues and
// features that need a key stay unavailable. Any malformed file is an error.
func Load(activePath string, previousPaths []string) (*Keyring, error) {
	ring := &Keyring{keys: map[string]key{}}
	if activePath == "" {
		if len(previousPaths) > 0 {
			return nil, errors.New("previous secrets keys need an active secrets key")
		}
		return ring, nil
	}
	for i, path := range append([]string{activePath}, previousPaths...) {
		master, err := readKeyFile(path)
		if err != nil {
			return nil, err
		}
		k, err := newKey(master)
		clear(master)
		if err != nil {
			return nil, fmt.Errorf("secrets key file %q: %w", path, err)
		}
		if i == 0 {
			ring.active = k.id
		} else if k.id == ring.active {
			return nil, fmt.Errorf("secrets key file %q repeats the active key; remove it from the previous keys", path)
		}
		ring.keys[k.id] = k
	}
	return ring, nil
}

// FromKeys builds a keyring from raw master keys, first active. It exists for
// tests and tools that hold keys in memory.
func FromKeys(active []byte, previous ...[]byte) (*Keyring, error) {
	ring := &Keyring{keys: map[string]key{}}
	for i, master := range append([][]byte{active}, previous...) {
		if len(master) != keyBytes {
			return nil, errors.New("a secrets key must be 32 bytes")
		}
		k, err := newKey(master)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			ring.active = k.id
		}
		ring.keys[k.id] = k
	}
	return ring, nil
}

// Configured reports whether an active key is loaded.
func (k *Keyring) Configured() bool { return k != nil && k.active != "" }

// ActiveKeyID is the id written with new ciphertexts, or "" with no key.
func (k *Keyring) ActiveKeyID() string {
	if k == nil {
		return ""
	}
	return k.active
}

// Has reports whether a key with this id is loaded.
func (k *Keyring) Has(id string) bool {
	if k == nil {
		return false
	}
	_, ok := k.keys[id]
	return ok
}

// Encrypt seals plaintext under the active key with a fresh random nonce.
func (k *Keyring) Encrypt(aad, plaintext []byte) (keyID string, nonce, ciphertext []byte, err error) {
	if !k.Configured() {
		return "", nil, nil, ErrNoKey
	}
	if len(plaintext) == 0 || len(plaintext) > MaxPlaintext {
		return "", nil, nil, errors.New("credential is empty or too large")
	}
	active := k.keys[k.active]
	nonce = make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, nil, err
	}
	return active.id, nonce, active.aead.Seal(nil, nonce, plaintext, aad), nil
}

// Decrypt opens a ciphertext. The caller must zero the plaintext after use.
func (k *Keyring) Decrypt(keyID string, nonce, ciphertext, aad []byte) ([]byte, error) {
	if k == nil {
		return nil, ErrUnknownKey
	}
	entry, ok := k.keys[keyID]
	if !ok {
		return nil, ErrUnknownKey
	}
	if len(nonce) != nonceBytes {
		return nil, ErrDecrypt
	}
	plaintext, err := entry.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// Binding identifies the row a ciphertext belongs to.
type Binding struct {
	AccountID                        int64
	Provider, TargetKey              string
	RemoteUserID, RemoteCredentialID string
}

// AAD encodes a binding as
// "photodrop/destination-credential/v1|<account id>|<provider>|<target key>|<remote user id>|<remote credential id>".
// A field containing "|" or a control character is refused, so two different
// bindings can never encode to the same bytes.
func AAD(b Binding) ([]byte, error) {
	if b.AccountID < 1 {
		return nil, ErrAAD
	}
	fields := []string{b.Provider, b.TargetKey, b.RemoteUserID, b.RemoteCredentialID}
	for _, field := range fields {
		if field == "" || len(field) > 512 || strings.ContainsAny(field, "|") || strings.IndexFunc(field, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return nil, ErrAAD
		}
	}
	var out bytes.Buffer
	out.WriteString(Version)
	out.WriteByte('|')
	out.WriteString(strconv.FormatInt(b.AccountID, 10))
	for _, field := range fields {
		out.WriteByte('|')
		out.WriteString(field)
	}
	return out.Bytes(), nil
}
