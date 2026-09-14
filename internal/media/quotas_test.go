package media

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"photodrop/internal/config"
	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanupExpiredGrantsAndAttemptsPreservesReadyRecovery(t *testing.T) {
	s, _, e, empty, _, _ := directFixture(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	newGrant := func() Session {
		t.Helper()
		grant, err := s.CreateVerifiedSession(ctx, e.PublicID, true)
		if err != nil {
			t.Fatal(err)
		}
		return grant
	}
	data := testutil.Images()["image/png"]
	pendingGrant, readyGrant := newGrant(), newGrant()
	pending := prepare(t, s, e, pendingGrant, "image/png", data)
	ready := prepare(t, s, e, readyGrant, "image/png", data)
	put(t, ready, data)
	finish(t, s, e, readyGrant, ready)
	exec("UPDATE upload_sessions SET expires_at='2000-01-01T00:00:00Z'")
	exec("UPDATE assets SET created_at='2000-01-01T00:00:00Z',authorized_until='2000-01-01T00:00:00Z' WHERE id=?", pending.Asset.ID)
	for range 2 {
		if err := s.Cleanup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, grant := range []Session{empty, pendingGrant} {
		_, err := s.Prepare(ctx, e.PublicID, grant.ID, Preparation{"new.png", int64(len(data)), "image/png", randomID()}, 1024)
		if !errors.Is(err, ErrSession) {
			t.Fatalf("removed grant: %v", err)
		}
	}
	if _, err := s.Complete(ctx, e.PublicID, pendingGrant.ID, pending.Asset.ID); !errors.Is(err, ErrAsset) {
		t.Fatalf("removed attempt: %v", err)
	}
	// Cleanup must keep expired grants with ready assets for ambiguous-response recovery.
	if _, err := s.Complete(ctx, e.PublicID, readyGrant.ID, ready.Asset.ID); err != nil {
		t.Fatal(err)
	}
	continued := newGrant()
	p := prepare(t, s, e, continued, "image/png", data)
	put(t, p, data)
	finish(t, s, e, continued, p)
	if countAssets(t, s, "ready") != 2 || countAssets(t, s, "pending") != 0 {
		t.Fatal("cleanup or recovery damaged assets")
	}
}

func TestCleanupUnavailableBackendsDoNotStarveLaterReservations(t *testing.T) {
	s, _, e, session, _, remote := directFixture(t)
	backendID := s.backends.ActiveID
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		id := fmt.Sprintf("%032x", i)
		if _, err := tx.Exec("INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_backend_id,expected_size_bytes) VALUES(?,?,?,'pending.png',?,'pending','2000-01-01T00:00:00Z',?,68)", id, e.ID, session.ID, remote.Key(e.ID, id), backendID); err != nil {
			t.Fatal(err)
		}
	}
	id := "ffffffffffffffffffffffffffffffff"
	if _, err := tx.Exec("INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_backend_id,expected_size_bytes) VALUES(?,?,?,'pending.png',?,'pending','2000-01-01T00:00:00Z',1,68)", id, e.ID, session.ID, objectKey(e.ID, id)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	configureDirect(t, s, nil, false)
	for range 2 {
		if err := s.Cleanup(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM assets WHERE storage_backend_id=1").Scan(&count); err != nil || count != 0 {
		t.Fatal("historical failures starved local cleanup", err)
	}
	if countAssets(t, s, "pending") != 1000 {
		t.Fatal("lost missing-backend retry state")
	}
}

func TestAtomicEventAndSessionReservationsAcrossConnections(t *testing.T) {
	for _, scope := range []string{"event files", "event bytes", "session files", "session bytes"} {
		t.Run(scope, func(t *testing.T) {
			s, dir, e, session, _, remote := directFixture(t)
			data := testutil.Images()["image/png"]
			size := int64(len(data))
			switch scope {
			case "event files":
				s.db.Exec("UPDATE events SET max_assets=10 WHERE id=?", e.ID)
			case "event bytes":
				s.db.Exec("UPDATE events SET max_bytes=? WHERE id=?", 10*size, e.ID)
			case "session files":
				s.db.Exec("UPDATE upload_sessions SET max_assets=10 WHERE id=?", session.ID)
			case "session bytes":
				s.db.Exec("UPDATE upload_sessions SET max_bytes=? WHERE id=?", 10*size, session.ID)
			}
			// Mixed local-ready usage and remote pending reservations share one allowance.
			configureDirect(t, s, remote, false)
			for range 9 {
				upload(t, s, e, session, "local.png")
			}
			configureDirect(t, s, remote, true)
			otherDB, err := database.Open(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer otherDB.Close()
			local, err := storage.NewLocal(filepath.Join(dir, "uploads"))
			if err != nil {
				t.Fatal(err)
			}
			other := New(otherDB, local, s.logger)
			other.ConfigureBackends(s.backends)
			var passed, rejected atomic.Int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := range 20 {
				svc := s
				if i%2 == 0 {
					svc = other
				}
				wg.Go(func() {
					<-start
					_, err := svc.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"photo.png", size, "image/png", randomID()}, 1024)
					if err == nil {
						passed.Add(1)
					} else if errors.Is(err, ErrEventQuota) || errors.Is(err, ErrSessionQuota) {
						rejected.Add(1)
					} else {
						t.Errorf("unexpected reservation error: %v", err)
					}
				})
			}
			close(start)
			wg.Wait()
			if passed.Load() != 1 || rejected.Load() != 19 {
				t.Fatal("oversubscribed or lost capacity", passed.Load(), rejected.Load())
			}
			var count, used int64
			if err := s.db.QueryRow("SELECT count(*),sum(CASE WHEN status='ready' THEN size_bytes ELSE expected_size_bytes END) FROM assets WHERE event_id=?", e.ID).Scan(&count, &used); err != nil || count != 10 || used != 10*size {
				t.Fatal(count, used, err)
			}
		})
	}
}

func TestQuotaLowerRaiseReleaseAndExpiry(t *testing.T) {
	s, _, e, session, _, remote := directFixture(t)
	data := testutil.Images()["image/png"]
	size := int64(len(data))
	configureDirect(t, s, remote, false)
	local := upload(t, s, e, session, "local.png")
	configureDirect(t, s, remote, true)
	if _, err := s.db.Exec("UPDATE events SET max_assets=2,max_bytes=? WHERE id=?", size*2, e.ID); err != nil {
		t.Fatal(err)
	}
	pending := prepare(t, s, e, session, "image/png", data)
	if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"next.png", size, "image/png", randomID()}, 1024); !errors.Is(err, ErrEventQuota) {
		t.Fatal("pending reservation ignored", err)
	}
	s.db.Exec("UPDATE assets SET created_at='2000-01-01T00:00:00Z',authorized_until='2000-01-01T00:00:00Z' WHERE id=?", pending.Asset.ID)
	if err := s.Cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	replacement := prepare(t, s, e, session, "image/png", data)
	put(t, replacement, data)
	finish(t, s, e, session, replacement)
	// Lowering preserves ready media; raising/removing the quota restores capacity.
	enabled := true
	one := int64(1)
	if _, err := s.events.Update(t.Context(), e.ID, events.Input{Name: e.Name, Enabled: &enabled, MaxAssets: &one, MaxBytes: &one}); err != nil {
		t.Fatal(err)
	}
	if countAssets(t, s, "ready") != 2 {
		t.Fatal("quota lowering deleted media")
	}
	if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"over.png", size, "image/png", randomID()}, 1024); !errors.Is(err, ErrEventQuota) {
		t.Fatal(err)
	}
	if _, err := s.events.Update(t.Context(), e.ID, events.Input{Name: e.Name, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	inflight := prepare(t, s, e, session, "image/png", data)
	put(t, inflight, data)
	s.db.Exec("UPDATE upload_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE id=?", session.ID)
	if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"expired.png", size, "image/png", randomID()}, 1024); !errors.Is(err, ErrExpired) {
		t.Fatal("expired prepare", err)
	}
	if _, err := s.Authorize(t.Context(), e.PublicID, session.ID, inflight.Asset.ID); !errors.Is(err, ErrExpired) {
		t.Fatal("expired refresh", err)
	}
	finish(t, s, e, session, inflight)
	finish(t, s, e, session, inflight)
	configureDirect(t, s, remote, false)
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "expired.png", "image/png", bytes.NewReader(data), size, 1024); !errors.Is(err, ErrExpired) {
		t.Fatal("expired local upload", err)
	}
	s.ConfigureSecurity(config.Security{SessionTTL: time.Hour, SessionMaxAssets: 1, SessionMaxBytes: size})
	fresh, err := s.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	upload(t, s, e, fresh, "fresh.png")
	if _, err := s.Upload(t.Context(), e.PublicID, fresh.ID, "extra.png", "image/png", bytes.NewReader(data), size, 1024); !errors.Is(err, ErrSessionQuota) {
		t.Fatal("server session limit", err)
	}
	if local.ID == "" {
		t.Fatal("local asset missing")
	}
	if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal("mixed quota deletion", err)
	}
}

func TestLocalUnknownLengthAndExactByteQuota(t *testing.T) {
	s, _, e, session := fixture(t)
	data := testutil.Images()["image/png"]
	size := int64(len(data))
	s.db.Exec("UPDATE events SET max_bytes=? WHERE id=?", size-1, e.ID)
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "a.png", "image/png", bytes.NewReader(data), size, 1024); !errors.Is(err, ErrEventQuota) {
		t.Fatal("one byte over", err)
	}
	s.db.Exec("UPDATE events SET max_bytes=? WHERE id=?", size, e.ID)
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "a.png", "image/png", bytes.NewReader(data), -1, 1024); !errors.Is(err, ErrEventQuota) {
		t.Fatal("unknown length did not reserve limit", err)
	}
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "lie.png", "image/png", bytes.NewReader(data), 1, 1024); err == nil {
		t.Fatal("accepted under-reserved content")
	}
	if countAssets(t, s, "pending") != 0 {
		t.Fatal("failed local reservation not released")
	}
	upload(t, s, e, session, "exact.png")
	stats, err := s.Stats(t.Context())
	if err != nil || stats[e.ID].StorageBytes != size || stats[e.ID].ReservedBytes != 0 {
		t.Fatal(stats, err)
	}
}
