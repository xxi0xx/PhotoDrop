package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"photodrop/internal/events"
	"photodrop/internal/storage"
)

type Service struct {
	db         *sql.DB
	events     *events.Store
	storage    storage.Store
	logger     *slog.Logger
	locks      sync.Map // internal event ID -> *sync.RWMutex; only existing events acquire entries
	backends   *storage.Backends
	assetLocks [64]sync.Mutex
}

func New(db *sql.DB, objects storage.Store, logger *slog.Logger) *Service {
	return &Service{db: db, events: events.New(db), storage: objects, logger: logger, backends: storage.LocalBackends()}
}

// ConfigureBackends is called once during startup, before cleanup or requests.
func (s *Service) ConfigureBackends(backends *storage.Backends) { s.backends = backends }
func (s *Service) Strategy() string {
	if s.backends.ActiveID != 1 {
		return "direct"
	}
	return "local"
}

func randomID() string { var b [16]byte; rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && len(id) == 32
}
func objectKey(eventID int64, assetID string) string {
	return "e" + strconv.FormatInt(eventID, 10) + "_" + assetID
}
func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (s *Service) lock(id int64) *sync.RWMutex {
	value, _ := s.locks.LoadOrStore(id, &sync.RWMutex{})
	return value.(*sync.RWMutex)
}

func (s *Service) openEvent(ctx context.Context, publicID string) (events.Event, error) {
	e, err := s.events.GetPublic(ctx, publicID)
	if err != nil {
		return events.Event{}, err
	}
	if e.Deleting || e.Status(time.Now()) != "open" {
		return events.Event{}, ErrClosed
	}
	return e, nil
}

type Session struct {
	ID string `json:"id"`
}

func (s *Service) CreateSession(ctx context.Context, publicID string) (Session, error) {
	e, err := s.openEvent(ctx, publicID)
	if err != nil {
		return Session{}, err
	}
	lock := s.lock(e.ID)
	lock.RLock()
	defer lock.RUnlock()
	// A short transaction rechecks state before creating the association.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if err := checkOpen(ctx, tx, e.ID, publicID); err != nil {
		return Session{}, err
	}
	id, now := randomID(), timestamp()
	if _, err := tx.ExecContext(ctx, "INSERT INTO upload_sessions(id, event_id, created_at, updated_at) VALUES (?, ?, ?, ?)", id, e.ID, now, now); err != nil {
		return Session{}, fmt.Errorf("create upload session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	s.logger.Info("upload session created", "event_id", e.ID)
	return Session{ID: id}, nil
}

func checkOpen(ctx context.Context, tx *sql.Tx, eventID int64, publicID string) error {
	var enabled, deleting bool
	var expiry *string
	if err := tx.QueryRowContext(ctx, "SELECT enabled, deleting, expires_at FROM events WHERE id = ? AND public_id = ?", eventID, publicID).Scan(&enabled, &deleting, &expiry); err != nil {
		return err
	}
	if !enabled || deleting {
		return ErrClosed
	}
	if expiry != nil {
		instant, err := time.Parse(time.RFC3339Nano, *expiry)
		if err != nil {
			return fmt.Errorf("read event expiration: %w", err)
		}
		if !time.Now().Before(instant) {
			return ErrClosed
		}
	}
	return nil
}

type Asset struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Status   string `json:"status"`
}

// Upload holds no database transaction while reading the request or writing
// media. Event deletion uses the exclusive event lock; concurrent uploads share
// it. Event edits can happen during transfer and are rechecked before readiness.
func (s *Service) Upload(ctx context.Context, publicID, sessionID, filename, declaredType string, source io.Reader, claimedSize, limit int64) (Asset, error) {
	if s.backends.ActiveID != 1 {
		return Asset{}, ErrStrategy
	}
	if err := ValidateFilename(filename); err != nil {
		return Asset{}, err
	}
	if err := ValidateDeclaredType(declaredType); err != nil {
		return Asset{}, err
	}
	if claimedSize > limit {
		return Asset{}, storage.ErrTooLarge
	}
	if !validID(sessionID) {
		return Asset{}, ErrSession
	}
	e, err := s.openEvent(ctx, publicID)
	if err != nil {
		return Asset{}, err
	}
	lock := s.lock(e.ID)
	lock.RLock()
	defer lock.RUnlock()
	id, now := randomID(), timestamp()
	key := objectKey(e.ID, id)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer tx.Rollback()
	if err := checkOpen(ctx, tx, e.ID, publicID); err != nil {
		return Asset{}, err
	}
	var associated bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM upload_sessions WHERE id = ? AND event_id = ?)", sessionID, e.ID).Scan(&associated); err != nil {
		return Asset{}, err
	}
	if !associated {
		return Asset{}, ErrSession
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO assets(id, event_id, upload_session_id, original_filename, storage_key, status, created_at, storage_backend_id)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, 1)`, id, e.ID, sessionID, filename, key, now); err != nil {
		return Asset{}, fmt.Errorf("create pending asset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Asset{}, err
	}
	s.logger.Info("asset upload started", "asset_id", id, "event_id", e.ID)
	ready := false
	defer func() {
		if !ready {
			// Client cancellation must not prevent cleanup. Retain the pending row
			// whenever cleanup fails, so startup or event deletion can retry it.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.removeAttempt(cleanupCtx, e.ID, id, key); err != nil {
				s.logger.Error("incomplete asset cleanup failed", "asset_id", id, "error", err)
			}
		}
	}()
	bounded := io.LimitReader(source, limit+1)
	var prefix [512]byte
	n, err := io.ReadFull(bounded, prefix[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Asset{}, fmt.Errorf("read upload header: %w", err)
	}
	if int64(n) > limit {
		return Asset{}, storage.ErrTooLarge
	}
	kind, err := Sniff(prefix[:n])
	if err != nil {
		return Asset{}, err
	}
	object, err := s.storage.Put(ctx, key, io.MultiReader(bytes.NewReader(prefix[:n]), bounded), limit)
	if err != nil {
		return Asset{}, err
	}
	if object.Size <= 0 || object.Size > limit || (claimedSize >= 0 && object.Size != claimedSize) {
		return Asset{}, ErrSize
	}
	finish, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Asset{}, err
	}
	defer finish.Rollback()
	if err := checkOpen(ctx, finish, e.ID, publicID); err != nil {
		return Asset{}, err
	}
	now = timestamp()
	result, err := finish.ExecContext(ctx, "UPDATE assets SET mime_type = ?, size_bytes = ?, status = 'ready', completed_at = ? WHERE id = ? AND status = 'pending'", kind, object.Size, now, id)
	if err != nil {
		return Asset{}, fmt.Errorf("complete asset: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return Asset{}, errors.New("pending asset disappeared during upload")
	}
	if _, err := finish.ExecContext(ctx, "UPDATE upload_sessions SET updated_at = ? WHERE id = ?", now, sessionID); err != nil {
		return Asset{}, err
	}
	if err := finish.Commit(); err != nil {
		return Asset{}, fmt.Errorf("commit completed asset: %w", err)
	}
	ready = true
	s.logger.Info("asset upload completed", "asset_id", id, "event_id", e.ID, "size_bytes", object.Size)
	return Asset{ID: id, Filename: filename, MIMEType: kind, Size: object.Size, Status: "ready"}, nil
}

type Stats struct {
	PhotoCount   int64 `json:"photo_count"`
	StorageBytes int64 `json:"storage_bytes"`
}

func (s *Service) Stats(ctx context.Context) (map[int64]Stats, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT event_id, COUNT(*), COALESCE(SUM(size_bytes), 0) FROM assets WHERE status = 'ready' GROUP BY event_id")
	if err != nil {
		return nil, fmt.Errorf("read media statistics: %w", err)
	}
	defer rows.Close()
	result := map[int64]Stats{}
	for rows.Next() {
		var id int64
		var stats Stats
		if err := rows.Scan(&id, &stats.PhotoCount, &stats.StorageBytes); err != nil {
			return nil, err
		}
		result[id] = stats
	}
	return result, rows.Err()
}

func (s *Service) removeAttempt(ctx context.Context, eventID int64, id, key string) error {
	if !validID(id) {
		return storage.ErrInvalidKey
	}
	// A completion commit can report an error with an uncertain outcome. Never
	// remove a file that SQLite already considers ready. Callers serialize this
	// check with uploads/deletion; startup cleanup runs before serving requests.
	var status string
	var backendID int64
	err := s.db.QueryRowContext(ctx, "SELECT status, storage_backend_id FROM assets WHERE id = ? AND event_id = ? AND storage_key = ?", id, eventID, key).Scan(&status, &backendID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return errors.New("refusing to clean up a completed asset")
	}
	backend, err := s.backends.Resolve(backendID)
	if err != nil {
		return err
	}
	if backend.Type == "local" {
		if key != objectKey(eventID, id) {
			return storage.ErrInvalidKey
		}
		if err := s.storage.Delete(ctx, key); err != nil {
			return err
		}
	} else if backend.Type == "s3" {
		direct, err := s.checkDirectKey(eventID, id, key, backendID)
		if err != nil {
			return err
		}
		// Record retirement before DELETE: an old bearer URL may recreate the
		// object after deletion. Retired keys survive event/session cascades.
		if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO s3_cleanup(storage_key, storage_target, event_id, asset_id, checked_at, storage_backend_id) VALUES (?, ?, ?, ?, ?, ?)", key, direct.Target(), eventID, id, timestamp(), backendID); err != nil {
			return err
		}
		if err := direct.Delete(ctx, key); err != nil {
			return err
		}
	} else {
		return storage.ErrBackend
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM assets WHERE id = ? AND event_id = ? AND status = 'pending'", id, eventID)
	return err
}

// Cleanup is bounded and runs only at startup. An hour exceeds the ten-minute
// upload deadline; recent pending rows remain excluded from completed totals.
func (s *Service) Cleanup(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, "SELECT event_id, id, storage_key FROM assets WHERE status = 'pending' AND created_at < ? AND (authorized_until IS NULL OR authorized_until < ?) ORDER BY created_at LIMIT 1000", cutoff, cutoff)
	if err != nil {
		return err
	}
	type pending struct {
		eventID int64
		id, key string
	}
	var attempts []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.eventID, &p.id, &p.key); err != nil {
			rows.Close()
			return err
		}
		attempts = append(attempts, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range attempts {
		if err := s.removeAttempt(ctx, p.eventID, p.id, p.key); err != nil {
			s.logger.Error("stale pending cleanup failed", "asset_id", p.id, "error", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		} else {
			s.logger.Info("stale pending asset removed", "asset_id", p.id)
		}
	}
	return s.cleanupRetired(ctx)
}

// DeleteEvent rejects active transfers rather than holding a request open for
// minutes. A durable deleting flag closes uploads and blocks edits. Each asset
// leaves ready state BEFORE its files are removed, so partial cleanup never
// counts missing files as completed. A retry continues from the remaining rows.
func (s *Service) DeleteEvent(ctx context.Context, eventID int64) error {
	event, err := s.events.Get(ctx, eventID)
	if err != nil {
		return err
	}
	lock := s.lock(eventID)
	if !lock.TryLock() {
		return ErrBusy
	}
	defer lock.Unlock()
	result, err := s.db.ExecContext(ctx, "UPDATE events SET deleting = 1, enabled = 0, updated_at = ? WHERE id = ? AND public_id = ?", timestamp(), eventID, event.PublicID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	for {
		var id, key string
		err := s.db.QueryRowContext(ctx, "SELECT id, storage_key FROM assets WHERE event_id = ? LIMIT 1", eventID).Scan(&id, &key)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE assets SET status = 'pending', completed_at = NULL WHERE id = ?", id); err != nil {
			return err
		}
		if err := s.removeAttempt(ctx, eventID, id, key); err != nil {
			s.logger.Error("event media cleanup failed", "event_id", eventID, "asset_id", id, "error", err)
			return ErrDeleting
		}
	}
	if err := s.events.Delete(ctx, eventID); err != nil {
		return err
	}
	s.logger.Info("event media cleanup completed", "event_id", eventID)
	return nil
}
