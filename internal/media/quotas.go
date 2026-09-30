package media

import (
	"context"
	"database/sql"
	"errors"
	"photodrop/internal/config"
	"photodrop/internal/storage"
	"time"
)

var ErrExpired = errors.New("Your upload session expired. Start a new session to continue.")
var ErrEventQuota = errors.New("This event has reached its file or storage limit. Contact the organizer.")
var ErrSessionQuota = errors.New("This upload session has reached its file or storage limit. Start a new session to continue.")

// QuotaError preserves the legacy error family while exposing a safe exact reason.
type QuotaError struct {
	Code, Message string
	File          bool
}

func (e *QuotaError) Error() string { return e.Message }
func (e *QuotaError) Unwrap() error {
	if e.File {
		return storage.ErrTooLarge
	}
	return ErrEventQuota
}
func quota(code, message string) error { return &QuotaError{Code: code, Message: message} }
func fileQuota(class string) error {
	return &QuotaError{Code: class + "_file_too_large", Message: "This " + class + " is larger than this event allows.", File: true}
}

// Class accepts only currently supported MIME values, never filenames.
func Class(kind string) string {
	if !SupportedMIME(kind) {
		return ""
	}
	if kind == "video/mp4" || kind == "video/quicktime" {
		return "video"
	}
	return "photo"
}
func EffectiveFileLimit(server int64, event *int64) int64 {
	if event != nil && *event < server {
		return *event
	}
	return server
}
func fileLimit(ctx context.Context, tx *sql.Tx, eventID int64, class string, server int64) (int64, error) {
	var photo, video *int64
	err := tx.QueryRowContext(ctx, "SELECT max_photo_file_bytes,max_video_file_bytes FROM events WHERE id=?", eventID).Scan(&photo, &video)
	if class == "video" {
		photo = video
	}
	return EffectiveFileLimit(server, photo), err
}

// Called under the same IMMEDIATE transaction as INSERT or legacy classification.
// An unknown legacy pending row isn't in this bucket until this check succeeds.
func reserveClass(ctx context.Context, tx *sql.Tx, eventID int64, class string, size int64) error {
	if class != "photo" && class != "video" {
		return ErrType
	}
	var maxCount, maxBytes *int64
	if err := tx.QueryRowContext(ctx, "SELECT max_"+class+"s,max_"+class+"_storage_bytes FROM events WHERE id=?", eventID).Scan(&maxCount, &maxBytes); err != nil {
		return err
	}
	var count, used int64
	if err := tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(CASE WHEN status='ready' THEN size_bytes ELSE expected_size_bytes END),0) FROM assets WHERE event_id=? AND media_class=?", eventID, class).Scan(&count, &used); err != nil {
		return err
	}
	if maxCount != nil && count >= *maxCount {
		return quota(class+"_count", "This event has reached its "+class+" limit.")
	}
	if maxBytes != nil && (used > *maxBytes || size > *maxBytes-used) {
		return quota(class+"_storage", "This event has reached its "+class+" storage limit.")
	}
	return nil
}

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
func reserve(ctx context.Context, tx *sql.Tx, eventID int64, sessionID string, size int64, q quotaSession, class string) error {
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
			if scope.cause == ErrEventQuota {
				return quota("event_file_count", "This event has reached its overall file limit.")
			}
			return scope.cause
		}
		if scope.maxBytes != nil && (used > *scope.maxBytes || size > *scope.maxBytes-used) {
			if scope.cause == ErrEventQuota {
				return quota("event_storage", "This event has reached its overall storage limit.")
			}
			return scope.cause
		}
	}
	return reserveClass(ctx, tx, eventID, class, size)
}
