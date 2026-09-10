// Package auth manages the single administrator credential and opaque sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const SessionLifetime = 12 * time.Hour
const PasswordCost = 12

var ErrUnauthorized = errors.New("unauthorized")

type Manager struct {
	db           *sql.DB
	passwordHash []byte
}

type Session struct {
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func New(ctx context.Context, db *sql.DB, password string) (*Manager, error) {
	return initialize(ctx, db, password, PasswordCost)
}

func initialize(ctx context.Context, db *sql.DB, password string, cost int) (*Manager, error) {
	if len(password) < 12 || len(password) > 72 {
		return nil, fmt.Errorf("administrator password must contain 12 to 72 bytes")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize administrator credential: %w", err)
	}
	defer tx.Rollback()
	var hash []byte
	err = tx.QueryRowContext(ctx, "SELECT password_hash FROM admin_credential WHERE id = 1").Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read administrator credential: %w", err)
	}
	if err == nil {
		if _, err := bcrypt.Cost(hash); err != nil {
			return nil, fmt.Errorf("stored administrator credential is invalid")
		}
	}
	if len(hash) == 0 || bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil {
		hash, err = bcrypt.GenerateFromPassword([]byte(password), cost)
		if err != nil {
			return nil, fmt.Errorf("hash administrator credential: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions"); err != nil {
			return nil, fmt.Errorf("revoke administrator sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_credential (id, password_hash) VALUES (1, ?)
		    ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash`, string(hash)); err != nil {
			return nil, fmt.Errorf("save administrator credential: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit administrator credential: %w", err)
	}
	return &Manager{db: db, passwordHash: hash}, nil
}

func randomToken() string {
	var bytes [32]byte
	rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func validToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	value, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(value) == 32
}

// Login always creates a new token and revokes the supplied previous session.
// Only the token's SHA-256 digest is persisted; the bearer token is never stored.
func (m *Manager) Login(ctx context.Context, password, previous string) (string, Session, error) {
	if len(password) > 72 || bcrypt.CompareHashAndPassword(m.passwordHash, []byte(password)) != nil {
		return "", Session{}, ErrUnauthorized
	}
	now := time.Now().UTC()
	session := Session{CSRFToken: randomToken(), ExpiresAt: now.Add(SessionLifetime).Truncate(time.Second)}
	token := randomToken()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", Session{}, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM admin_sessions WHERE expires_at <= ? OR token_hash = ?", now.Unix(), tokenHash(previous)); err != nil {
		return "", Session{}, fmt.Errorf("remove old sessions: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions (token_hash, csrf_token, created_at, expires_at)
	    SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM admin_credential WHERE id = 1 AND password_hash = ?)`,
		tokenHash(token), session.CSRFToken, now.Unix(), session.ExpiresAt.Unix(), string(m.passwordHash))
	if err != nil {
		return "", Session{}, fmt.Errorf("create session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", Session{}, fmt.Errorf("create session: %w", err)
	}
	if n != 1 {
		return "", Session{}, ErrUnauthorized
	}
	if err := tx.Commit(); err != nil {
		return "", Session{}, fmt.Errorf("commit session: %w", err)
	}
	return token, session, nil
}

func (m *Manager) Lookup(ctx context.Context, token string) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrUnauthorized
	}
	var session Session
	var expires int64
	err := m.db.QueryRowContext(ctx, `SELECT csrf_token, expires_at FROM admin_sessions
	    WHERE token_hash = ? AND expires_at > ?
	    AND EXISTS (SELECT 1 FROM admin_credential WHERE id = 1 AND password_hash = ?)`, tokenHash(token), time.Now().Unix(), string(m.passwordHash)).Scan(&session.CSRFToken, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, fmt.Errorf("validate session: %w", err)
	}
	session.ExpiresAt = time.Unix(expires, 0).UTC()
	return session, nil
}

func (m *Manager) Logout(ctx context.Context, token string) error {
	if _, err := m.db.ExecContext(ctx, "DELETE FROM admin_sessions WHERE token_hash = ?", tokenHash(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
