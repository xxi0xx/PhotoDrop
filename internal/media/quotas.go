package media

import (
	"context"
	"database/sql"
	"errors"
	"photodrop/internal/config"
	"time"
)

var ErrExpired = errors.New("Your upload session expired. Start a new session to continue.")
var ErrEventQuota = errors.New("This event has reached its file or storage limit. Contact the organizer.")
var ErrSessionQuota = errors.New("This upload session has reached its file or storage limit. Start a new session to continue.")

func (s *Service) ConfigureSecurity(c config.Security) { s.security = c.Defaults() }

type quotaSession struct {
	maxAssets, maxBytes int64
	contributor         *string
}

func checkSession(ctx context.Context, tx *sql.Tx, eventID int64, sessionID string) (quotaSession, error) {
	var q quotaSession
	var expires string
	err := tx.QueryRowContext(ctx, "SELECT expires_at,max_assets,max_bytes,contributor_name FROM upload_sessions WHERE id=? AND event_id=?", sessionID, eventID).Scan(&expires, &q.maxAssets, &q.maxBytes, &q.contributor)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrSession
	}
	if err != nil {
		return q, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return q, err
	}
	if !time.Now().Before(expiry) {
		return q, ErrExpired
	}
	return q, nil
}

// Caller holds an IMMEDIATE SQLite write transaction from check through INSERT.
// The existing pending row is the reservation; completion replaces expected bytes
// with actual ready bytes, so capacity is never counted twice.
func reserve(ctx context.Context, tx *sql.Tx, eventID int64, sessionID string, size int64, q quotaSession) error {
	var eventMaxAssets, eventMaxBytes *int64
	if err := tx.QueryRowContext(ctx, "SELECT max_assets,max_bytes FROM events WHERE id=?", eventID).Scan(&eventMaxAssets, &eventMaxBytes); err != nil {
		return err
	}
	for _, scope := range []struct {
		where               string
		id                  any
		maxAssets, maxBytes *int64
		cause               error
	}{
		{"event_id", eventID, eventMaxAssets, eventMaxBytes, ErrEventQuota},
		{"upload_session_id", sessionID, &q.maxAssets, &q.maxBytes, ErrSessionQuota},
	} {
		var count, used int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(CASE WHEN status='ready' THEN size_bytes ELSE expected_size_bytes END),0) FROM assets WHERE "+scope.where+"=?", scope.id).Scan(&count, &used); err != nil {
			return err
		}
		if scope.maxAssets != nil && count >= *scope.maxAssets {
			return scope.cause
		}
		if scope.maxBytes != nil && (used > *scope.maxBytes || size > *scope.maxBytes-used) {
			return scope.cause
		}
	}
	return nil
}
