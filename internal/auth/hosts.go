package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Identity is a verified, authorized OIDC sign-in. Role is derived from
// configuration at every login and never stored as a grant on its own.
type Identity struct {
	Issuer, Subject, Email, Name string
	Groups                       []string
	Role                         string
}

var (
	ErrHostDisabled = errors.New("host is disabled")
	ErrSelf         = errors.New("you cannot disable your own host")
)

// Host is the admin-visible view of a host. It never includes the OIDC issuer
// or subject.
type Host struct {
	ID          int64      `json:"id"`
	Role        string     `json:"role"`
	DisplayName string     `json:"display_name"`
	Email       *string    `json:"email"`
	OIDC        bool       `json:"oidc"`
	Password    bool       `json:"password"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func cleanText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if utf8.RuneCountInString(value) > limit {
		value = string([]rune(value)[:limit])
	}
	return strings.TrimSpace(value)
}

// DisplayName follows name, then preferred_username, then email, then "Host".
func DisplayName(name, preferred, email string) string {
	for _, candidate := range []string{name, preferred, email} {
		if value := cleanText(candidate, 200); value != "" {
			return value
		}
	}
	return "Host"
}

func cleanEmail(email string) any {
	email = cleanText(email, 400)
	if email == "" || len(email) > 320 {
		return nil
	}
	return email
}

// ResolveHost maps a verified identity onto a host row in one transaction:
// an existing identity is refreshed (or denied when disabled), the first admin
// sign-in claims host 1, and anyone else becomes a new host.
func (m *Manager) ResolveHost(ctx context.Context, id Identity) (int64, error) {
	if id.Issuer == "" || id.Subject == "" || (id.Role != RoleAdmin && id.Role != RoleHost) {
		return 0, ErrUnauthorized
	}
	name, email := cleanText(id.Name, 200), cleanEmail(id.Email)
	if name == "" {
		name = "Host"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin host resolution: %w", err)
	}
	defer tx.Rollback()
	var hostID int64
	var disabled bool
	err = tx.QueryRowContext(ctx, "SELECT id, disabled FROM hosts WHERE oidc_issuer = ? AND oidc_subject = ?", id.Issuer, id.Subject).Scan(&hostID, &disabled)
	switch {
	case err == nil:
		if disabled {
			return 0, ErrHostDisabled
		}
		if _, err := tx.ExecContext(ctx, "UPDATE hosts SET role = ?, email = ?, display_name = ?, last_login_at = ? WHERE id = ?", id.Role, email, name, now, hostID); err != nil {
			return 0, fmt.Errorf("refresh host: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		claimed := false
		if id.Role == RoleAdmin {
			err = tx.QueryRowContext(ctx, `UPDATE hosts SET oidc_issuer = ?, oidc_subject = ?, email = ?, display_name = ?, role = ?, last_login_at = ?
			    WHERE id = ? AND oidc_issuer IS NULL RETURNING id, disabled`, id.Issuer, id.Subject, email, name, RoleAdmin, now, PasswordHostID).Scan(&hostID, &disabled)
			if err == nil {
				claimed = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return 0, fmt.Errorf("claim administrator host: %w", err)
			}
		}
		if claimed && disabled {
			return 0, ErrHostDisabled
		}
		if !claimed {
			if err := tx.QueryRowContext(ctx, `INSERT INTO hosts (role, display_name, email, oidc_issuer, oidc_subject, created_at, last_login_at)
			    VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`, id.Role, name, email, id.Issuer, id.Subject, now, now).Scan(&hostID); err != nil {
				return 0, fmt.Errorf("create host: %w", err)
			}
		}
	default:
		return 0, fmt.Errorf("find host: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit host resolution: %w", err)
	}
	return hostID, nil
}

// Hosts lists every host for administrators, oldest first.
func (m *Manager) Hosts(ctx context.Context) ([]Host, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, role, display_name, email, oidc_subject IS NOT NULL, password_login, disabled, created_at, last_login_at FROM hosts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	defer rows.Close()
	result := []Host{}
	for rows.Next() {
		var h Host
		var created string
		var last *string
		if err := rows.Scan(&h.ID, &h.Role, &h.DisplayName, &h.Email, &h.OIDC, &h.Password, &h.Disabled, &created, &last); err != nil {
			return nil, fmt.Errorf("read host: %w", err)
		}
		if h.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("read host: %w", err)
		}
		if last != nil {
			t, err := time.Parse(time.RFC3339Nano, *last)
			if err != nil {
				return nil, fmt.Errorf("read host: %w", err)
			}
			h.LastLoginAt = &t
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list hosts: %w", err)
	}
	return result, nil
}

// SetDisabled enables or disables a host. Disabling deletes that host's sessions
// in the same transaction. Hosts are never deleted, and nobody disables their
// own host, so at least one administrator always remains able to sign in.
func (m *Manager) SetDisabled(ctx context.Context, actor, hostID int64, disabled bool) error {
	if disabled && actor == hostID {
		return ErrSelf
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin host update: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE hosts SET disabled = ? WHERE id = ?", disabled, hostID)
	if err != nil {
		return fmt.Errorf("update host: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return fmt.Errorf("update host: %w", err)
		}
		return sql.ErrNoRows
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM host_sessions WHERE host_id = ?", hostID); err != nil {
			return fmt.Errorf("revoke host sessions: %w", err)
		}
	}
	return tx.Commit()
}
