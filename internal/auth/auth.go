// Package auth manages hosts, the administrator password credential and opaque
// host sessions.
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

// PasswordHostID is the administrator host that password sign-in always uses.
const PasswordHostID int64 = 1

const (
	RoleAdmin = "admin"
	RoleHost  = "host"
)

var ErrUnauthorized = errors.New("unauthorized")

type Manager struct {
	db           *sql.DB
	passwordHash []byte
}

// SessionHost is the public identity carried by a session.
type SessionHost struct {
	ID          int64  `json:"id"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
}

type Session struct {
	CSRFToken string      `json:"csrf_token"`
	ExpiresAt time.Time   `json:"expires_at"`
	Host      SessionHost `json:"host"`
}

// Admin reports whether the session's host has the admin role.
func (s Session) Admin() bool { return s.Host.Role == RoleAdmin }

func New(ctx context.Context, db *sql.DB, password string) (*Manager, error) {
	return initialize(ctx, db, password, PasswordCost)
}

// NewSessionOnly never reads or enables a historical password credential.
func NewSessionOnly(db *sql.DB) *Manager { return &Manager{db: db} }

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
		// A password change revokes only the password host's sessions.
		if _, err := tx.ExecContext(ctx, "DELETE FROM host_sessions WHERE host_id = ?", PasswordHostID); err != nil {
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

// Login always creates a new token for the password host and revokes the
// supplied previous session. Only the token's SHA-256 digest is persisted.
func (m *Manager) Login(ctx context.Context, password, previous string) (string, Session, error) {
	if len(m.passwordHash) == 0 || len(password) > 72 || bcrypt.CompareHashAndPassword(m.passwordHash, []byte(password)) != nil {
		return "", Session{}, ErrUnauthorized
	}
	return m.createSession(ctx, previous, PasswordHostID, true)
}

// CreateSession is for a verified and authorized external login whose host was
// resolved by ResolveHost. Both sign-in methods use the same opaque tokens,
// expiry, rotation and CSRF mechanism.
func (m *Manager) CreateSession(ctx context.Context, previous string, hostID int64) (string, Session, error) {
	return m.createSession(ctx, previous, hostID, false)
}

func (m *Manager) createSession(ctx context.Context, previous string, hostID int64, passwordLogin bool) (string, Session, error) {
	now := time.Now().UTC()
	session := Session{CSRFToken: randomToken(), ExpiresAt: now.Add(SessionLifetime).Truncate(time.Second)}
	token := randomToken()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", Session{}, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM host_sessions WHERE expires_at <= ? OR token_hash = ?", now.Unix(), tokenHash(previous)); err != nil {
		return "", Session{}, fmt.Errorf("remove old sessions: %w", err)
	}
	// A disabled host never gets a session. A password session also requires the
	// credential this process verified to still be the stored one.
	res, err := tx.ExecContext(ctx, `INSERT INTO host_sessions (token_hash, host_id, csrf_token, created_at, expires_at)
	    SELECT ?, h.id, ?, ?, ? FROM hosts h WHERE h.id = ? AND h.disabled = 0
	    AND (?=0 OR EXISTS (SELECT 1 FROM admin_credential WHERE id = 1 AND password_hash = ?))`,
		tokenHash(token), session.CSRFToken, now.Unix(), session.ExpiresAt.Unix(), hostID, passwordLogin, string(m.passwordHash))
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
	if passwordLogin {
		if _, err := tx.ExecContext(ctx, "UPDATE hosts SET last_login_at = ? WHERE id = ?", now.Format(time.RFC3339Nano), hostID); err != nil {
			return "", Session{}, fmt.Errorf("record login: %w", err)
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT id, role, display_name FROM hosts WHERE id = ?", hostID).Scan(&session.Host.ID, &session.Host.Role, &session.Host.DisplayName); err != nil {
		return "", Session{}, fmt.Errorf("read session host: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", Session{}, fmt.Errorf("commit session: %w", err)
	}
	return token, session, nil
}

// Lookup resolves a session and its host's current role. Sessions of disabled
// hosts are rejected even before they are deleted.
func (m *Manager) Lookup(ctx context.Context, token string) (Session, error) {
	if !validToken(token) {
		return Session{}, ErrUnauthorized
	}
	var session Session
	var expires int64
	err := m.db.QueryRowContext(ctx, `SELECT s.csrf_token, s.expires_at, h.id, h.role, h.display_name
	    FROM host_sessions s JOIN hosts h ON h.id = s.host_id
	    WHERE s.token_hash = ? AND s.expires_at > ? AND h.disabled = 0`, tokenHash(token), time.Now().Unix()).Scan(&session.CSRFToken, &expires, &session.Host.ID, &session.Host.Role, &session.Host.DisplayName)
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
	if _, err := m.db.ExecContext(ctx, "DELETE FROM host_sessions WHERE token_hash = ?", tokenHash(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
