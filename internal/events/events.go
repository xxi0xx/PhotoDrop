// Package events owns event validation and explicit SQLite queries.
package events

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const PublicIDLength = 24 // 18 random bytes = 144 bits of entropy.

type Event struct {
	ID                   int64      `json:"id"`
	PublicID             string     `json:"public_id"`
	Name                 string     `json:"name"`
	Description          string     `json:"description"`
	EventDate            *string    `json:"event_date"`
	Enabled              bool       `json:"enabled"`
	ExpiresAt            *time.Time `json:"expires_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	Deleting             bool       `json:"deleting"`
	MaxAssets            *int64     `json:"max_assets"`
	MaxBytes             *int64     `json:"max_bytes"`
	MaxPhotos            *int64     `json:"max_photos"`
	MaxVideos            *int64     `json:"max_videos"`
	MaxPhotoFileBytes    *int64     `json:"max_photo_file_bytes"`
	MaxVideoFileBytes    *int64     `json:"max_video_file_bytes"`
	MaxPhotoStorageBytes *int64     `json:"max_photo_storage_bytes"`
	MaxVideoStorageBytes *int64     `json:"max_video_storage_bytes"`
	Owner                Owner      `json:"owner"`
}

// Owner is the host an event belongs to.
type Owner struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
}

// Input deliberately has no internal/public identifier or audit timestamps.
type Input struct {
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	EventDate            *string `json:"event_date"`
	Enabled              *bool   `json:"enabled"`
	ExpiresAt            *string `json:"expires_at"`
	MaxAssets            *int64  `json:"max_assets"`
	MaxBytes             *int64  `json:"max_bytes"`
	MaxPhotos            *int64  `json:"max_photos"`
	MaxVideos            *int64  `json:"max_videos"`
	MaxPhotoFileBytes    *int64  `json:"max_photo_file_bytes"`
	MaxVideoFileBytes    *int64  `json:"max_video_file_bytes"`
	MaxPhotoStorageBytes *int64  `json:"max_photo_storage_bytes"`
	MaxVideoStorageBytes *int64  `json:"max_video_storage_bytes"`
}

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "Check the event fields" }

func (e Event) Status(now time.Time) string {
	if !e.Enabled || e.Deleting {
		return "disabled"
	}
	if e.ExpiresAt != nil && !now.Before(*e.ExpiresAt) {
		return "expired"
	}
	return "open"
}

func validate(input Input) (Event, error) {
	e := Event{Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), EventDate: input.EventDate}
	fields := map[string]string{}
	e.MaxAssets, e.MaxBytes = input.MaxAssets, input.MaxBytes
	e.MaxPhotos = input.MaxPhotos
	e.MaxVideos = input.MaxVideos
	e.MaxPhotoFileBytes = input.MaxPhotoFileBytes
	e.MaxVideoFileBytes = input.MaxVideoFileBytes
	e.MaxPhotoStorageBytes = input.MaxPhotoStorageBytes
	e.MaxVideoStorageBytes = input.MaxVideoStorageBytes
	if input.MaxPhotos != nil && (*input.MaxPhotos < 1 || *input.MaxPhotos > 1000000) {
		fields["max_photos"] = "Enter a positive whole number up to 1,000,000, or leave empty"
	}
	if input.MaxVideos != nil && (*input.MaxVideos < 1 || *input.MaxVideos > 1000000) {
		fields["max_videos"] = "Enter a positive whole number up to 1,000,000, or leave empty"
	}
	if input.MaxPhotoFileBytes != nil && (*input.MaxPhotoFileBytes < 1 || *input.MaxPhotoFileBytes > 1<<50) {
		fields["max_photo_file_bytes"] = "Enter a positive whole number up to 1 PiB in bytes, or leave empty"
	}
	if input.MaxVideoFileBytes != nil && (*input.MaxVideoFileBytes < 1 || *input.MaxVideoFileBytes > 1<<50) {
		fields["max_video_file_bytes"] = "Enter a positive whole number up to 1 PiB in bytes, or leave empty"
	}
	if input.MaxPhotoStorageBytes != nil && (*input.MaxPhotoStorageBytes < 1 || *input.MaxPhotoStorageBytes > 1<<50) {
		fields["max_photo_storage_bytes"] = "Enter a positive whole number up to 1 PiB in bytes, or leave empty"
	}
	if input.MaxVideoStorageBytes != nil && (*input.MaxVideoStorageBytes < 1 || *input.MaxVideoStorageBytes > 1<<50) {
		fields["max_video_storage_bytes"] = "Enter a positive whole number up to 1 PiB in bytes, or leave empty"
	}
	if input.MaxAssets != nil && (*input.MaxAssets < 1 || *input.MaxAssets > 1000000) {
		fields["max_assets"] = "Enter 1 to 1,000,000 files, or leave empty"
	}
	if input.MaxBytes != nil && (*input.MaxBytes < 1 || *input.MaxBytes > 1<<50) {
		fields["max_bytes"] = "Enter a positive storage limit up to 1 PiB, or leave empty"
	}
	if n := utf8.RuneCountInString(e.Name); n == 0 || n > 200 || strings.ContainsRune(e.Name, '\x00') {
		fields["name"] = "Enter a name of 1 to 200 characters"
	}
	if utf8.RuneCountInString(e.Description) > 4000 || strings.ContainsRune(e.Description, '\x00') {
		fields["description"] = "Use at most 4000 characters"
	}
	if input.Enabled == nil {
		fields["enabled"] = "Enabled must be a boolean"
	} else {
		e.Enabled = *input.Enabled
	}
	if input.EventDate != nil {
		d, err := time.Parse(time.DateOnly, *input.EventDate)
		if err != nil || d.Year() < 1 || d.Format(time.DateOnly) != *input.EventDate {
			fields["event_date"] = "Use a valid date in YYYY-MM-DD format, or null"
		}
	}
	if input.ExpiresAt != nil {
		instant, err := time.Parse(time.RFC3339Nano, *input.ExpiresAt)
		utc := instant.UTC()
		if err != nil || utc.Year() < 1 || utc.Year() > 9999 {
			fields["expires_at"] = "Use an RFC3339 timestamp with a timezone, or null"
		} else {
			e.ExpiresAt = &utc
		}
	}
	if len(fields) != 0 {
		return Event{}, &ValidationError{Fields: fields}
	}
	return e, nil
}

func ValidPublicID(id string) bool {
	if len(id) != PublicIDLength {
		return false
	}
	bytes, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(bytes) == 18
}

func newPublicID() string {
	var bytes [18]byte
	rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

const columns = "id, public_id, name, description, event_date, enabled, expires_at, created_at, updated_at, deleting, max_assets, max_bytes, max_photos, max_videos, max_photo_file_bytes, max_video_file_bytes, max_photo_storage_bytes, max_video_storage_bytes, owner_host_id"

// named selects the event columns plus the owner's display name. RETURNING
// clauses use columns alone and read the name afterwards.
const named = columns + ", (SELECT display_name FROM hosts WHERE hosts.id = events.owner_host_id)"

// Scope limits which events a caller sees. A host sees only its own events;
// an administrator sees all of them.
type Scope struct {
	HostID int64
	All    bool
}

// System is the scope for internal work that is not acting for a signed-in
// host, such as media cleanup, exports and import jobs.
var System = Scope{All: true}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanNamed(row interface{ Scan(...any) error }) (Event, error) {
	var name string
	e, err := scanInto(row, &name)
	e.Owner.DisplayName = name
	return e, err
}

func scan(row interface{ Scan(...any) error }) (Event, error) { return scanInto(row) }

func scanInto(row interface{ Scan(...any) error }, extra ...any) (Event, error) {
	var e Event
	var expiry *string
	var created, updated string
	dest := []any{&e.ID, &e.PublicID, &e.Name, &e.Description, &e.EventDate, &e.Enabled, &expiry, &created, &updated, &e.Deleting, &e.MaxAssets, &e.MaxBytes, &e.MaxPhotos, &e.MaxVideos, &e.MaxPhotoFileBytes, &e.MaxVideoFileBytes, &e.MaxPhotoStorageBytes, &e.MaxVideoStorageBytes, &e.Owner.ID}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Event{}, err
	}
	var err error
	if e.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Event{}, err
	}
	if e.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return Event{}, err
	}
	if expiry != nil {
		t, err := time.Parse(time.RFC3339Nano, *expiry)
		if err != nil {
			return Event{}, err
		}
		e.ExpiresAt = &t
	}
	return e, nil
}

func expiryValue(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// Create makes an event owned by ownerHostID, normally the signed-in host.
func (s *Store) Create(ctx context.Context, ownerHostID int64, input Input) (Event, error) {
	return create(ctx, s.db, ownerHostID, input)
}

// CreateInTx lets the application atomically attach local setup to a new event.
// The caller owns commit/rollback; no external I/O belongs in this transaction.
func CreateInTx(ctx context.Context, tx *sql.Tx, ownerHostID int64, input Input) (Event, error) {
	return create(ctx, tx, ownerHostID, input)
}

func withOwnerName(ctx context.Context, db querier, e Event) (Event, error) {
	if err := db.QueryRowContext(ctx, "SELECT display_name FROM hosts WHERE id = ?", e.Owner.ID).Scan(&e.Owner.DisplayName); err != nil {
		return Event{}, fmt.Errorf("read event owner: %w", err)
	}
	return e, nil
}

func create(ctx context.Context, db querier, ownerHostID int64, input Input) (Event, error) {
	e, err := validate(input)
	if err != nil {
		return Event{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for range 3 {
		row := db.QueryRowContext(ctx, `INSERT INTO events
		    (public_id, name, description, event_date, enabled, expires_at, created_at, updated_at, max_assets, max_bytes, max_photos, max_videos, max_photo_file_bytes, max_video_file_bytes, max_photo_storage_bytes, max_video_storage_bytes, owner_host_id)
		    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(public_id) DO NOTHING RETURNING `+columns,
			newPublicID(), e.Name, e.Description, e.EventDate, e.Enabled, expiryValue(e.ExpiresAt), now, now, e.MaxAssets, e.MaxBytes, e.MaxPhotos, e.MaxVideos, e.MaxPhotoFileBytes, e.MaxVideoFileBytes, e.MaxPhotoStorageBytes, e.MaxVideoStorageBytes, ownerHostID)
		saved, err := scan(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Event{}, fmt.Errorf("create event: %w", err)
		}
		return withOwnerName(ctx, db, saved)
	}
	return Event{}, fmt.Errorf("allocate unique public event identifier")
}

// List returns the events visible in scope, newest first.
func (s *Store) List(ctx context.Context, scope Scope) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+named+" FROM events WHERE ? OR owner_host_id = ? ORDER BY id DESC", scope.All, scope.HostID)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		e, err := scanNamed(rows)
		if err != nil {
			return nil, fmt.Errorf("read event: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return result, nil
}

// Get returns an event in scope. An event outside scope is sql.ErrNoRows, the
// same as one that does not exist.
func (s *Store) Get(ctx context.Context, scope Scope, id int64) (Event, error) {
	e, err := scanNamed(s.db.QueryRowContext(ctx, "SELECT "+named+" FROM events WHERE id = ? AND (? OR owner_host_id = ?)", id, scope.All, scope.HostID))
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	return e, nil
}

// OwnerOf returns an event's owner host id, or sql.ErrNoRows.
func (s *Store) OwnerOf(ctx context.Context, id int64) (int64, error) {
	var owner int64
	if err := s.db.QueryRowContext(ctx, "SELECT owner_host_id FROM events WHERE id = ?", id).Scan(&owner); err != nil {
		return 0, fmt.Errorf("get event owner: %w", err)
	}
	return owner, nil
}

func (s *Store) GetPublic(ctx context.Context, id string) (Event, error) {
	if !ValidPublicID(id) {
		return Event{}, sql.ErrNoRows
	}
	e, err := scanNamed(s.db.QueryRowContext(ctx, "SELECT "+named+" FROM events WHERE public_id = ?", id))
	if err != nil {
		return Event{}, fmt.Errorf("get public event: %w", err)
	}
	return e, nil
}

// Update changes an event in scope; one outside scope is sql.ErrNoRows.
func (s *Store) Update(ctx context.Context, scope Scope, id int64, input Input) (Event, error) {
	e, err := validate(input)
	if err != nil {
		return Event{}, err
	}
	row := s.db.QueryRowContext(ctx, `UPDATE events SET name = ?, description = ?, event_date = ?,
	    enabled = ?, expires_at = ?, updated_at = ?, max_assets = ?, max_bytes = ?, max_photos = ?, max_videos = ?, max_photo_file_bytes = ?, max_video_file_bytes = ?, max_photo_storage_bytes = ?, max_video_storage_bytes = ? WHERE id = ? AND deleting = 0 AND (? OR owner_host_id = ?) RETURNING `+columns,
		e.Name, e.Description, e.EventDate, e.Enabled, expiryValue(e.ExpiresAt), time.Now().UTC().Format(time.RFC3339Nano), e.MaxAssets, e.MaxBytes, e.MaxPhotos, e.MaxVideos, e.MaxPhotoFileBytes, e.MaxVideoFileBytes, e.MaxPhotoStorageBytes, e.MaxVideoStorageBytes, id, scope.All, scope.HostID)
	e, err = scan(row)
	if err != nil {
		return Event{}, fmt.Errorf("update event: %w", err)
	}
	return withOwnerName(ctx, s.db, e)
}

// Delete removes the event row. Media cleanup and the caller's ownership check
// happen before this; see media.Service.DeleteEvent.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

var (
	// ErrOwnerHost means the new owner does not exist or is disabled.
	ErrOwnerHost = errors.New("choose an enabled host as the new owner")
	// ErrOwnerLocked means an Immich album already exists in the current owner's
	// account, so the event can no longer change owner.
	ErrOwnerLocked = errors.New("this event has an Immich album in its owner's account, so its owner can no longer change")
	// ErrOwnerBusy means an Immich job for the event is queued or running.
	ErrOwnerBusy = errors.New("wait for or cancel the active Immich import before changing the owner")
)

// TransferOwner moves an event to another host while no Immich album exists for
// it. Bindings still in state 'new' are deleted with the transfer, because they
// were chosen for the previous owner's account.
func (s *Store) TransferOwner(ctx context.Context, id, hostID int64) (Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, fmt.Errorf("begin owner transfer: %w", err)
	}
	defer tx.Rollback()
	var deleting bool
	if err := tx.QueryRowContext(ctx, "SELECT deleting FROM events WHERE id = ?", id).Scan(&deleting); err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	if deleting {
		return Event{}, sql.ErrNoRows
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT disabled = 0 FROM hosts WHERE id = ?", hostID).Scan(&enabled); err != nil || !enabled {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Event{}, fmt.Errorf("get new owner: %w", err)
		}
		return Event{}, ErrOwnerHost
	}
	var albums, active int
	if err := tx.QueryRowContext(ctx, `SELECT
	    (SELECT count(*) FROM immich_event_imports WHERE event_id = ? AND album_state <> 'new'),
	    (SELECT count(*) FROM integration_jobs j JOIN immich_event_imports b ON b.id = j.event_import_id WHERE b.event_id = ? AND j.status IN ('queued','running'))`, id, id).Scan(&albums, &active); err != nil {
		return Event{}, fmt.Errorf("check event bindings: %w", err)
	}
	if active > 0 {
		return Event{}, ErrOwnerBusy
	}
	if albums > 0 {
		return Event{}, ErrOwnerLocked
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM immich_event_imports WHERE event_id = ? AND album_state = 'new'", id); err != nil {
		return Event{}, fmt.Errorf("remove unprovisioned bindings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE events SET owner_host_id = ?, updated_at = ? WHERE id = ?", hostID, time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
		return Event{}, fmt.Errorf("transfer owner: %w", err)
	}
	e, err := scanNamed(tx.QueryRowContext(ctx, "SELECT "+named+" FROM events WHERE id = ?", id))
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit owner transfer: %w", err)
	}
	return e, nil
}
