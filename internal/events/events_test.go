package events

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"photodrop/internal/database"
)

func input() Input {
	enabled := true
	return Input{Name: "  A celebration  ", Description: "Welcome", Enabled: &enabled}
}
func pointer[T any](v T) *T { return &v }

func TestCRUDAndPublicIDs(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	list, err := s.List(t.Context(), System)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty list: %v, %v", list, err)
	}
	seen := map[string]bool{}
	for range 50 {
		e, err := s.Create(t.Context(), 1, input())
		if err != nil {
			t.Fatal(err)
		}
		if !ValidPublicID(e.PublicID) || seen[e.PublicID] || e.PublicID == strconv.FormatInt(e.ID, 10) {
			t.Fatalf("invalid public ID: %q", e.PublicID)
		}
		seen[e.PublicID] = true
	}
	list, err = s.List(t.Context(), System)
	if err != nil || len(list) != 50 {
		t.Fatalf("list: %d, %v", len(list), err)
	}
	e := list[0]
	if e.Name != "A celebration" || e.Status(time.Now()) != "open" {
		t.Fatalf("unexpected event: %+v", e)
	}
	found, err := s.GetPublic(t.Context(), e.PublicID)
	if err != nil || found.ID != e.ID {
		t.Fatalf("public lookup: %v", err)
	}
	if _, err := s.GetPublic(t.Context(), strconv.FormatInt(e.ID, 10)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("internal ID accepted publicly")
	}
	in := input()
	in.Name = "Updated"
	in.Enabled = pointer(false)
	in.EventDate = pointer("2026-09-04")
	in.ExpiresAt = pointer("2026-09-05T01:00:00-05:00")
	updated, err := s.Update(t.Context(), System, e.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PublicID != e.PublicID || !updated.CreatedAt.Equal(e.CreatedAt) || updated.UpdatedAt.Before(e.UpdatedAt) || *updated.EventDate != "2026-09-04" || updated.ExpiresAt.Format(time.RFC3339) != "2026-09-05T06:00:00Z" || updated.Status(time.Now()) != "disabled" {
		t.Fatalf("update semantics: %+v", updated)
	}
	if _, err := db.Exec("UPDATE events SET public_id = ? WHERE id = ?", newPublicID(), e.ID); err == nil {
		t.Fatal("database allowed public ID mutation")
	}
	if err := s.Delete(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), System, e.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted event still present: %v", err)
	}
	if _, err := s.GetPublic(t.Context(), e.PublicID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted public ID still works")
	}
	if err := s.Delete(t.Context(), e.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delete: %v", err)
	}
	if _, err := s.Update(t.Context(), System, e.ID, input()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing update: %v", err)
	}
}

func TestValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Input)
	}{
		{"name", func(i *Input) { i.Name = "   " }},
		{"name length", func(i *Input) { i.Name = strings.Repeat("é", 201) }},
		{"description", func(i *Input) { i.Description = strings.Repeat("x", 4001) }},
		{"enabled", func(i *Input) { i.Enabled = nil }},
		{"date", func(i *Input) { i.EventDate = pointer("2026-02-30") }},
		{"empty date", func(i *Input) { i.EventDate = pointer("") }},
		{"expiration", func(i *Input) { i.ExpiresAt = pointer("2026-09-04T12:30:00") }},
		{"expiration before supported UTC year", func(i *Input) { i.ExpiresAt = pointer("0001-01-01T00:00:00+01:00") }},
		{"expiration after supported UTC year", func(i *Input) { i.ExpiresAt = pointer("9999-12-31T23:00:00-02:00") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			i := input()
			tt.change(&i)
			var validation *ValidationError
			if _, err := validate(i); !errors.As(err, &validation) {
				t.Fatalf("expected validation error: %v", err)
			}
		})
	}
	i := input()
	i.Name = strings.Repeat("é", 200)
	i.Description = "<script>alert(1)</script> ' OR 1=1; --"
	if _, err := validate(i); err != nil {
		t.Fatalf("valid plain text rejected: %v", err)
	}
}

func TestExpiration(t *testing.T) {
	now := time.Now().UTC()
	e := Event{Enabled: true, ExpiresAt: &now}
	if e.Status(now) != "expired" || e.Status(now.Add(-time.Second)) != "open" {
		t.Fatal("expiration boundary is incorrect")
	}
}

func TestScopeLimitsHostsToTheirOwnEvents(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO hosts(id,role,display_name,oidc_issuer,oidc_subject,created_at) VALUES(2,'host','Second host','https://idp.test/','second','old')`); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	mine, err := s.Create(t.Context(), 2, input())
	if err != nil || mine.Owner != (Owner{2, "Second host"}) {
		t.Fatal("create did not record the owner", mine.Owner, err)
	}
	other, err := s.Create(t.Context(), 1, input())
	if err != nil || other.Owner != (Owner{1, "Administrator"}) {
		t.Fatal(other.Owner, err)
	}
	host := Scope{HostID: 2}
	list, err := s.List(t.Context(), host)
	if err != nil || len(list) != 1 || list[0].ID != mine.ID || list[0].Owner.DisplayName != "Second host" {
		t.Fatal("host list", list, err)
	}
	if all, err := s.List(t.Context(), System); err != nil || len(all) != 2 {
		t.Fatal("system list", all, err)
	}
	if _, err := s.Get(t.Context(), host, other.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("host read another host's event", err)
	}
	if _, err := s.Update(t.Context(), host, other.ID, input()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("host updated another host's event", err)
	}
	if updated, err := s.Update(t.Context(), host, mine.ID, input()); err != nil || updated.Owner != mine.Owner {
		t.Fatal("owner lost on update", updated.Owner, err)
	}
	if owner, err := s.OwnerOf(t.Context(), other.ID); err != nil || owner != 1 {
		t.Fatal(owner, err)
	}
	if _, err := s.OwnerOf(t.Context(), 999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := s.Create(t.Context(), 99, input()); err == nil {
		t.Fatal("event created for a host that does not exist")
	}
}
