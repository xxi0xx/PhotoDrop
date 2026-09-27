package immich

import (
	"context"
	"database/sql"
	"errors"
	"photodrop/internal/events"
	"testing"
	"time"
)

func TestCreateEventProvisioningIsAtomicAndOffline(t *testing.T) {
	s, f, _, _ := jobsFixture(t, 0)
	on := true
	input := events.Input{Name: "New event", Enabled: &on}
	f.validateErr = ErrUnavailable
	e, err := s.CreateEvent(t.Context(), input, "test", "Before uploads")
	if err != nil {
		t.Fatal(err)
	}
	v := status(t, s, e)
	if v.Total != 0 || v.AlbumName != "Before uploads" || v.Job.Status != "queued" || f.validations != 0 || f.albumCreates != 0 {
		t.Fatal("creation depended on remote", v)
	}
	runQueued(t, s)
	if v := status(t, s, e); v.Job.Status != "failed" || v.AlbumID != "" {
		t.Fatal(v)
	}
	if _, err := s.claim(t.Context()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("automatic retry loop", err)
	}
	if _, err := s.media.CreateSession(t.Context(), e.PublicID); err != nil {
		t.Fatal("Immich failure blocked guest", err)
	}
	f.validateErr = nil
	if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	if v := status(t, s, e); v.AlbumID != albumUUID || v.AlbumState != "ready" || v.Job.Status != "completed" || v.AlbumName != "Before uploads" {
		t.Fatal(v)
	}
	// Failure to persist the job must roll back both event and binding.
	var before int
	s.db.QueryRow("SELECT count(*) FROM events").Scan(&before)
	if _, err := s.db.Exec("CREATE TRIGGER fail_queue BEFORE INSERT ON integration_jobs BEGIN SELECT RAISE(ABORT,'queue failed'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateEvent(t.Context(), input, "test", ""); err == nil {
		t.Fatal("queue failure accepted")
	}
	var after, bindings int
	s.db.QueryRow("SELECT count(*) FROM events").Scan(&after)
	s.db.QueryRow("SELECT count(*) FROM immich_event_imports").Scan(&bindings)
	if after != before || bindings != 1 {
		t.Fatal("partial invisible state", before, after, bindings)
	}
}

func TestProvisionSelectsNoMediaAndKeepsAlbumIdentity(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			s, f, upload, e := jobsFixture(t, count)
			if _, err := s.Provision(t.Context(), e.ID, "test", "Early album"); err != nil {
				t.Fatal(err)
			}
			upload("arrived-after-queue.png")
			runQueued(t, s)
			if v := status(t, s, e); v.AlbumID != albumUUID || v.Imported != 0 || v.New != int64(count+1) || f.accepted != 0 {
				t.Fatal("album provisioning imported media", v)
			}
			if _, err := s.claim(t.Context()); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("upload queued automatic import", err)
			}
			enqueue(t, s, e, "new")
			runQueued(t, s)
			if f.albumCreates != 1 || status(t, s, e).Imported != int64(count+1) {
				t.Fatal("manual import lost album identity")
			}
			if err := s.media.DeleteEvent(t.Context(), e.ID); err != nil {
				t.Fatal(err)
			}
			if f.albumCreates != 1 || f.accepted != count+1 {
				t.Fatal("remote copies changed on deletion")
			}
		})
	}
}

func TestZeroMediaLostAlbumResponseAndRestart(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "restart"}[restart], func(t *testing.T) {
			s, f, _, e := jobsFixture(t, 0)
			if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
				t.Fatal(err)
			}
			if restart {
				f.albumEntered = make(chan struct{})
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); s.Run(ctx) }()
				select {
				case <-f.albumEntered:
				case <-ctx.Done():
					t.Fatal("album creation did not start")
				}
				cancel()
				<-done
				f.albumEntered = nil
				s = New(s.db, s.media, s.targets, s.logger)
				if err := s.recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				f.loseAlbum = true
				runQueued(t, s)
				if v := status(t, s, e); v.Job.Status != "failed" || v.AlbumState != "creating" {
					t.Fatal(v)
				}
				if _, err := s.Provision(t.Context(), e.ID, "test", "ignored rename"); err != nil {
					t.Fatal(err)
				}
			}
			runQueued(t, s)
			if v := status(t, s, e); v.AlbumID != albumUUID || v.Job.Status != "completed" || v.Total != 0 || f.albumCreates != 1 {
				t.Fatal("duplicated/lost album", v)
			}
		})
	}
}

func TestUnavailableTargetStillCreatesUsableEvent(t *testing.T) {
	s, _, _, _ := jobsFixture(t, 0)
	s.targets.entries[1] = Target{ID: 1, Key: "test"}
	on := true
	e, err := s.CreateEvent(t.Context(), events.Input{Name: "Credentials missing", Enabled: &on}, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	if v := status(t, s, e); v.Target.Available || v.Job.Status != "failed" {
		t.Fatal(v)
	}
	if _, err := s.media.CreateSession(t.Context(), e.PublicID); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionMultipleTargetsAndBrowserLinks(t *testing.T) {
	s, f, _, e := jobsFixture(t, 0)
	r, err := s.db.Exec("INSERT INTO immich_targets(key,base_url,created_at) VALUES('second','https://internal.example',?)", now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	second := &fakeAPI{}
	s.targets.entries[id] = Target{ID: id, Key: "second", Available: true, PublicURL: "https://public.example", client: second}
	if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Provision(t.Context(), e.ID, "second", ""); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	runQueued(t, s)
	first, err := s.Status(t.Context(), e.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Status(t.Context(), e.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.AlbumURL != "" || other.Target.Key != "second" || other.AlbumURL != "https://public.example/albums/"+albumUUID || f.albumCreates != 1 || second.albumCreates != 1 {
		t.Fatal(first, other)
	}
	// Browser origin is runtime-only and changing it cannot recreate a binding.
	entry := s.targets.entries[id]
	entry.PublicURL = "https://changed.example"
	s.targets.entries[id] = entry
	other, err = s.Status(t.Context(), e.ID, "second")
	if err != nil || other.AlbumURL != "https://changed.example/albums/"+albumUUID {
		t.Fatal(other, err)
	}
	entry.PublicURL = ""
	s.targets.entries[id] = entry
	other, err = s.Status(t.Context(), e.ID, "second")
	if err != nil || other.AlbumURL != "" || other.AlbumID != albumUUID {
		t.Fatal(other, err)
	}
}
