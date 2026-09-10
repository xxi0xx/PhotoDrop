package auth

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"photodrop/internal/database"
)

func TestSessionLifecycleAndCredentialRotation(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	const password = "test-password-for-auth"
	m, err := initialize(t.Context(), db, password, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Login(t.Context(), "incorrect password", ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("incorrect password: %v", err)
	}
	var stored string
	if err := db.QueryRow("SELECT password_hash FROM admin_credential").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == password || bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) != nil {
		t.Fatal("password not hashed correctly")
	}
	token, session, err := m.Login(t.Context(), password, "attacker-fixed-session")
	if err != nil {
		t.Fatal(err)
	}
	if !validToken(token) || !validToken(session.CSRFToken) || token == session.CSRFToken || time.Until(session.ExpiresAt) > SessionLifetime {
		t.Fatal("invalid session credentials")
	}
	if err := db.QueryRow("SELECT token_hash FROM admin_sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != tokenHash(token) {
		t.Fatal("bearer token persisted")
	}
	if _, err := m.Lookup(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err = initialize(t.Context(), db, password, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := m.Lookup(t.Context(), token); err != nil || resumed.CSRFToken != session.CSRFToken {
		t.Fatalf("restart lost session: %v", err)
	}
	newToken, _, err := m.Login(t.Context(), password, token)
	if err != nil || token == newToken {
		t.Fatalf("login did not rotate session: %v", err)
	}
	if _, err := m.Lookup(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("previous login still valid")
	}
	if err := m.Logout(t.Context(), newToken); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(t.Context(), newToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("logged-out session still valid")
	}
	token, _, err = m.Login(t.Context(), password, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE admin_sessions SET expires_at = ?", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lookup(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted")
	}
	token, _, err = m.Login(t.Context(), password, "")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := initialize(t.Context(), db, "a different password", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Lookup(t.Context(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("password change did not revoke session")
	}
	if _, _, err := m.Login(t.Context(), password, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("old process could establish a session after credential change")
	}
}
