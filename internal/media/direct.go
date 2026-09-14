package media

import (
	"context"
	"database/sql"
	"errors"
	"mime"
	"time"

	"photodrop/internal/storage"
)

var ErrStrategy = errors.New("reload the event page to get current upload instructions")
var ErrAsset = errors.New("upload attempt not found for this event and session")
var ErrReady = errors.New("this photo is already complete")
var ErrRequest = errors.New("upload metadata does not match the original attempt")

type Preparation struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	RequestID   string `json:"request_id"`
}
type Prepared struct {
	Asset  Asset              `json:"asset"`
	Upload storage.UploadPlan `json:"upload"`
}
type directAsset struct {
	Asset
	eventID           int64
	key, expectedType string
	backendID         int64
	expectedSize      int64
	deleting, enabled bool
	expiry            *string
}

func (s *Service) checkDirectKey(eventID int64, id, key string, backendID int64) (storage.Direct, error) {
	backend, err := s.backends.Resolve(backendID)
	if err != nil {
		return nil, err
	}
	if backend.Type != "s3" || backend.Direct == nil {
		return nil, storage.ErrBackend
	}
	if !validID(id) || key != backend.Direct.Key(eventID, id) {
		return nil, storage.ErrInvalidKey
	}
	return backend.Direct, nil
}
func (s *Service) loadDirect(ctx context.Context, publicID, sessionID, id string) (directAsset, error) {
	var a directAsset
	if !validID(id) || !validID(sessionID) {
		return a, ErrAsset
	}
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.original_filename,a.mime_type,a.size_bytes,a.status,a.event_id,a.storage_key,a.storage_backend_id,a.expected_mime_type,a.expected_size_bytes,e.deleting,e.enabled,e.expires_at
 FROM assets a JOIN events e ON e.id=a.event_id JOIN upload_sessions u ON u.id=a.upload_session_id AND u.event_id=e.id
 JOIN storage_backends b ON b.id=a.storage_backend_id
 WHERE e.public_id=? AND u.id=? AND a.id=? AND b.type='s3'`, publicID, sessionID, id).Scan(&a.ID, &a.Filename, &a.MIMEType, &a.Size, &a.Status, &a.eventID, &a.key, &a.backendID, &a.expectedType, &a.expectedSize, &a.deleting, &a.enabled, &a.expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAsset
	}
	return a, err
}

func (s *Service) Prepare(ctx context.Context, publicID, sessionID string, p Preparation, limit int64) (Prepared, error) {
	if s.backends.ActiveID == 1 {
		return Prepared{}, ErrStrategy
	}
	active, err := s.backends.Resolve(s.backends.ActiveID)
	if err != nil {
		return Prepared{}, err
	}
	direct := active.Direct
	if err := ValidateFilename(p.Filename); err != nil {
		return Prepared{}, err
	}
	if err := ValidateDeclaredType(p.ContentType); err != nil {
		return Prepared{}, err
	}
	if p.ContentType == "" {
		p.ContentType = "application/octet-stream"
	}
	kind, _, _ := mime.ParseMediaType(p.ContentType)
	p.ContentType = kind
	if p.Size <= 0 {
		return Prepared{}, ErrEmpty
	}
	if p.Size > limit {
		return Prepared{}, storage.ErrTooLarge
	}
	if !validID(p.RequestID) {
		return Prepared{}, ErrRequest
	}
	if !validID(sessionID) {
		return Prepared{}, ErrSession
	}
	e, err := s.openEvent(ctx, publicID)
	if err != nil {
		return Prepared{}, err
	}
	lock := s.lock(e.ID)
	lock.RLock()
	id, err := func() (string, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		if err := checkOpen(ctx, tx, e.ID, publicID); err != nil {
			return "", err
		}
		q, err := checkSession(ctx, tx, e.ID, sessionID)
		if err != nil {
			return "", err
		}
		var id, name, contentType string
		var size int64
		err = tx.QueryRowContext(ctx, "SELECT id,original_filename,expected_size_bytes,expected_mime_type FROM assets WHERE upload_session_id=? AND client_request_id=?", sessionID, p.RequestID).Scan(&id, &name, &size, &contentType)
		if err == nil {
			if name != p.Filename || size != p.Size || contentType != p.ContentType {
				return "", ErrRequest
			}
			return id, tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		id = randomID()
		if err := reserve(ctx, tx, e.ID, sessionID, p.Size, q); err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_provider,storage_target,expected_size_bytes,expected_mime_type,client_request_id,storage_backend_id)
   VALUES(?,?,?,?,?,'pending',?,'s3',?,?,?,?,?)`, id, e.ID, sessionID, p.Filename, direct.Key(e.ID, id), timestamp(), direct.Target(), p.Size, p.ContentType, p.RequestID, active.ID)
		if err != nil {
			return "", err
		}
		return id, tx.Commit()
	}()
	lock.RUnlock()
	if err != nil {
		return Prepared{}, err
	}
	s.logger.Info("direct upload prepared", "asset_id", id, "event_id", e.ID)
	return s.Authorize(ctx, publicID, sessionID, id)
}

// Lock order is event then asset stripe. Network verification never holds SQL
// write transactions. The bounded stripes serialize complete/refresh per asset.
func (s *Service) directLock(ctx context.Context, publicID, id string) (func(), error) {
	if !validID(id) {
		return nil, ErrAsset
	}
	e, err := s.events.GetPublic(ctx, publicID)
	if err != nil {
		return nil, err
	}
	eventLock := s.lock(e.ID)
	eventLock.RLock()
	assetLock := &s.assetLocks[(int(id[0])+int(id[1]))%len(s.assetLocks)]
	assetLock.Lock()
	return func() { assetLock.Unlock(); eventLock.RUnlock() }, nil
}
func (s *Service) Authorize(ctx context.Context, publicID, sessionID, id string) (Prepared, error) {
	unlock, err := s.directLock(ctx, publicID, id)
	if err != nil {
		return Prepared{}, err
	}
	defer unlock()
	a, err := s.loadDirect(ctx, publicID, sessionID, id)
	if err != nil {
		return Prepared{}, err
	}
	if a.Status == "ready" {
		return Prepared{}, ErrReady
	}
	if _, err := s.openEvent(ctx, publicID); err != nil {
		return Prepared{}, err
	}
	direct, err := s.checkDirectKey(a.eventID, id, a.key, a.backendID)
	if err != nil {
		return Prepared{}, err
	}
	plan, err := direct.Authorize(ctx, a.key, a.expectedType)
	if err != nil {
		return Prepared{}, err
	}
	// Save the latest expiry before exposing the URL. Startup cleanup must not
	// delete a long-lived pending row that was recently reauthorized.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Prepared{}, err
	}
	defer tx.Rollback()
	if err := checkOpen(ctx, tx, a.eventID, publicID); err != nil {
		return Prepared{}, err
	}
	if _, err := checkSession(ctx, tx, a.eventID, sessionID); err != nil {
		return Prepared{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE assets SET authorized_until=? WHERE id=? AND status='pending'", plan.ExpiresAt.UTC().Format(time.RFC3339Nano), id); err != nil {
		return Prepared{}, err
	}
	if err := tx.Commit(); err != nil {
		return Prepared{}, err
	}
	s.logger.Info("direct upload authorized", "asset_id", id)
	return Prepared{a.Asset, plan}, nil
}

func (s *Service) Complete(ctx context.Context, publicID, sessionID, id string) (Asset, error) {
	unlock, err := s.directLock(ctx, publicID, id)
	if err != nil {
		return Asset{}, err
	}
	defer unlock()
	a, err := s.loadDirect(ctx, publicID, sessionID, id)
	if err != nil {
		return Asset{}, err
	}
	if a.deleting {
		return Asset{}, ErrClosed
	}
	if a.Status == "ready" {
		return a.Asset, nil
	}
	direct, err := s.checkDirectKey(a.eventID, id, a.key, a.backendID)
	if err != nil {
		return Asset{}, err
	}
	s.logger.Info("direct upload verification started", "asset_id", id)
	info, err := direct.Head(ctx, a.key)
	if err != nil {
		return Asset{}, err
	}
	reject := func(cause error) (Asset, error) {
		if err := direct.Delete(ctx, a.key); err != nil {
			s.logger.Error("invalid object cleanup failed", "asset_id", id, "error", err)
			return Asset{}, err
		}
		s.logger.Info("direct upload validation rejected", "asset_id", id, "reason", cause)
		// Keep the pending attempt and its immutable metadata for same-key retry.
		return Asset{}, cause
	}
	if info.Size != a.expectedSize || info.Size <= 0 {
		return reject(ErrSize)
	}
	if info.ContentType != "" {
		kind, _, err := mime.ParseMediaType(info.ContentType)
		if err != nil || kind != a.expectedType {
			return reject(ErrType)
		}
	}
	prefix, err := direct.ReadPrefix(ctx, a.key, info)
	if err != nil {
		return Asset{}, err
	}
	kind, err := Sniff(prefix)
	if err != nil {
		return reject(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	// Previously authorized valid uploads may finish after disable/expiration.
	// A deleting event cannot gain ready assets. Its exclusive lock also guards it.
	var deleting bool
	if err := tx.QueryRowContext(ctx, "SELECT deleting FROM events WHERE id=? AND public_id=?", a.eventID, publicID).Scan(&deleting); err != nil {
		return Asset{}, err
	}
	if deleting {
		return Asset{}, ErrClosed
	}
	now := timestamp()
	result, err := tx.ExecContext(ctx, "UPDATE assets SET status='ready',size_bytes=?,mime_type=?,completed_at=? WHERE id=? AND status='pending'", info.Size, kind, now, id)
	if err != nil {
		return Asset{}, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return Asset{}, ErrAsset
	}
	if _, err := tx.ExecContext(ctx, "UPDATE upload_sessions SET updated_at=? WHERE id=?", now, sessionID); err != nil {
		return Asset{}, err
	}
	if err := tx.Commit(); err != nil {
		return Asset{}, err
	}
	a.Status = "ready"
	a.Size = info.Size
	a.MIMEType = kind
	s.logger.Info("direct upload finalized", "asset_id", id, "size_bytes", info.Size)
	return a.Asset, nil
}

func (s *Service) cleanupRetired(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT storage_key,storage_backend_id,event_id,asset_id FROM s3_cleanup WHERE checked_at < ? AND storage_key > ? ORDER BY storage_key LIMIT 1000", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), s.retiredAfter)
	if err != nil {
		return err
	}
	type retired struct {
		key, id   string
		backendID int64
		eventID   int64
	}
	var items []retired
	for rows.Next() {
		var r retired
		if err := rows.Scan(&r.key, &r.backendID, &r.eventID, &r.id); err != nil {
			rows.Close()
			return err
		}
		items = append(items, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range items {
		s.retiredAfter = r.key
		direct, err := s.checkDirectKey(r.eventID, r.id, r.key, r.backendID)
		if err == nil {
			err = direct.Delete(ctx, r.key)
		}
		if err != nil {
			s.logger.Error("retired object cleanup failed", "asset_id", r.id, "error", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE s3_cleanup SET checked_at=? WHERE storage_key=?", timestamp(), r.key); err != nil {
			return err
		}
		s.logger.Info("retired object reconciled", "asset_id", r.id)
	}
	if len(items) < 1000 {
		s.retiredAfter = ""
	}
	return nil
}
