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
	ID          int64      `json:"id"`
	PublicID    string     `json:"public_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	EventDate   *string    `json:"event_date"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Input deliberately has no internal/public identifier or audit timestamps.
type Input struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	EventDate   *string `json:"event_date"`
	Enabled     *bool   `json:"enabled"`
	ExpiresAt   *string `json:"expires_at"`
}

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "Check the event fields" }

func (e Event) Status(now time.Time) string {
	if !e.Enabled {
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

const columns = "id, public_id, name, description, event_date, enabled, expires_at, created_at, updated_at"

func scan(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var expiry *string
	var created, updated string
	if err := row.Scan(&e.ID, &e.PublicID, &e.Name, &e.Description, &e.EventDate, &e.Enabled, &expiry, &created, &updated); err != nil {
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

func (s *Store) Create(ctx context.Context, input Input) (Event, error) {
	e, err := validate(input)
	if err != nil {
		return Event{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for range 3 {
		row := s.db.QueryRowContext(ctx, `INSERT INTO events
		    (public_id, name, description, event_date, enabled, expires_at, created_at, updated_at)
		    VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(public_id) DO NOTHING RETURNING `+columns,
			newPublicID(), e.Name, e.Description, e.EventDate, e.Enabled, expiryValue(e.ExpiresAt), now, now)
		saved, err := scan(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Event{}, fmt.Errorf("create event: %w", err)
		}
		return saved, nil
	}
	return Event{}, fmt.Errorf("allocate unique public event identifier")
}

func (s *Store) List(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM events ORDER BY id DESC")
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		e, err := scan(rows)
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

func (s *Store) Get(ctx context.Context, id int64) (Event, error) {
	e, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM events WHERE id = ?", id))
	if err != nil {
		return Event{}, fmt.Errorf("get event: %w", err)
	}
	return e, nil
}

func (s *Store) GetPublic(ctx context.Context, id string) (Event, error) {
	if !ValidPublicID(id) {
		return Event{}, sql.ErrNoRows
	}
	e, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM events WHERE public_id = ?", id))
	if err != nil {
		return Event{}, fmt.Errorf("get public event: %w", err)
	}
	return e, nil
}

func (s *Store) Update(ctx context.Context, id int64, input Input) (Event, error) {
	e, err := validate(input)
	if err != nil {
		return Event{}, err
	}
	row := s.db.QueryRowContext(ctx, `UPDATE events SET name = ?, description = ?, event_date = ?,
	    enabled = ?, expires_at = ?, updated_at = ? WHERE id = ? RETURNING `+columns,
		e.Name, e.Description, e.EventDate, e.Enabled, expiryValue(e.ExpiresAt), time.Now().UTC().Format(time.RFC3339Nano), id)
	e, err = scan(row)
	if err != nil {
		return Event{}, fmt.Errorf("update event: %w", err)
	}
	return e, nil
}

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
