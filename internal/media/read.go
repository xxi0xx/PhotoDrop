package media

import (
	"context"
	"io"
	"sync"

	"photodrop/internal/events"
	"photodrop/internal/storage"
)

// ReadyAsset exposes portable metadata; routing information stays private.
type ReadyAsset struct {
	Asset
	UploadedAt string
	key        string
	backendID  int64
}

// Snapshot pins an event against in-process deletion until Close. No SQLite
// transaction is held during streaming. A separate CLI process cannot share
// this lock; concurrent deletion there is reported as an incomplete export.
type Snapshot struct {
	Event   events.Event
	Assets  []ReadyAsset
	service *Service
	once    sync.Once
	unlock  func()
}

func (s *Service) Snapshot(ctx context.Context, eventID int64) (*Snapshot, error) {
	if _, err := s.events.Get(ctx, eventID); err != nil {
		return nil, err
	}
	lock := s.lock(eventID)
	lock.RLock()
	e, err := s.events.Get(ctx, eventID)
	if err != nil || e.Deleting {
		lock.RUnlock()
		if err != nil {
			return nil, err
		}
		return nil, ErrDeleting
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, original_filename, mime_type, size_bytes, completed_at, storage_key, storage_backend_id FROM assets WHERE event_id=? AND status='ready' ORDER BY created_at, id`, eventID)
	if err != nil {
		lock.RUnlock()
		return nil, err
	}
	defer rows.Close()
	result := &Snapshot{Event: e, Assets: []ReadyAsset{}, service: s, unlock: lock.RUnlock}
	for rows.Next() {
		var a ReadyAsset
		a.Status = "ready"
		if err := rows.Scan(&a.ID, &a.Filename, &a.MIMEType, &a.Size, &a.UploadedAt, &a.key, &a.backendID); err != nil {
			result.Close()
			return nil, err
		}
		result.Assets = append(result.Assets, a)
	}
	if err := rows.Err(); err != nil {
		result.Close()
		return nil, err
	}
	return result, nil
}

func (s *Snapshot) Close() { s.once.Do(s.unlock) }

// Open returns an owned stream. Callers close every stream before the snapshot.
// The stored backend ID, never the active destination, selects the provider.
func (s *Snapshot) Open(ctx context.Context, a ReadyAsset) (io.ReadCloser, storage.ObjectInfo, error) {
	b, err := s.service.backends.Resolve(a.backendID)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	var reader storage.Reader
	if b.Type == "local" {
		if a.key != objectKey(s.Event.ID, a.ID) {
			return nil, storage.ObjectInfo{}, storage.ErrInvalidKey
		}
		reader, _ = s.service.storage.(storage.Reader)
	} else {
		direct, err := s.service.checkDirectKey(s.Event.ID, a.ID, a.key, a.backendID)
		if err != nil {
			return nil, storage.ObjectInfo{}, err
		}
		reader, _ = direct.(storage.Reader)
	}
	if reader == nil {
		return nil, storage.ObjectInfo{}, storage.ErrBackend
	}
	stream, info, err := reader.Open(ctx, a.key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	if info.Size != a.Size || (info.ContentType != "" && info.ContentType != a.MIMEType) {
		stream.Close()
		return nil, storage.ObjectInfo{}, storage.ErrObjectChanged
	}
	info.ContentType = a.MIMEType
	return stream, info, nil
}
