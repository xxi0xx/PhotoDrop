package immich

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"photodrop/internal/events"
	"photodrop/internal/testutil"
	"sync"
	"testing"
	"time"
)

func uploadAutoPhoto(t *testing.T, s *Service, e events.Event, sessionID, name string) {
	t.Helper()
	data := testutil.Images()["image/png"]
	if _, err := s.media.Upload(t.Context(), e.PublicID, sessionID, name, "image/png", bytes.NewReader(data), int64(len(data)), 1024); err != nil {
		t.Fatal(err)
	}
}

func autoReady(t *testing.T, s *Service, e events.Event) {
	t.Helper()
	if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	if status(t, s, e).AutoImport {
		t.Fatal("auto-import defaults on")
	}
	if err := s.SetAutoImport(t.Context(), e.ID, "test", true); err != nil {
		t.Fatal(err)
	}
}
func reconcileNow(t *testing.T, s *Service) {
	t.Helper()
	if err := s.reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func jobCount(t *testing.T, s *Service) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM integration_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func noQueued(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.claim(t.Context()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unexpected queued job", err)
	}
}

func TestAutoBurstAndConcurrentReconciliation(t *testing.T) {
	for _, count := range []int{1, 10, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, f, upload, e := jobsFixture(t, 0)
			autoReady(t, s, e)
			for i := range count {
				upload(fmt.Sprintf("burst-%d.png", i))
			}
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					if err := s.reconcile(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if jobCount(t, s) != 2 || status(t, s, e).Pending != int64(count) {
				t.Fatal("burst did not coalesce", status(t, s, e))
			}
			extraSession, err := s.media.CreateSession(t.Context(), e.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			uploadAutoPhoto(t, s, e, extraSession.ID, "queued-arrival.png")
			reconcileNow(t, s)
			if jobCount(t, s) != 2 || status(t, s, e).New != 1 {
				t.Fatal("queued job selection changed")
			}
			// Hold the selected work in the remote API while three new uploads finish.
			f.gate = make(chan struct{})
			f.entered = make(chan struct{}, count)
			j, err := s.claim(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			// Bound entry into the gate, not the full 100-asset race-instrumented
			// import. Slow runners must not turn successful work into cancellation.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); s.execute(ctx, j) }()
			defer func() { cancel(); <-done }()
			select {
			case <-f.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("worker did not enter upload")
			}
			for i := range 3 {
				uploadAutoPhoto(t, s, e, extraSession.ID, fmt.Sprintf("running-%d.png", i))
			}
			reconcileNow(t, s)
			if jobCount(t, s) != 2 || status(t, s, e).Pending != int64(count) || status(t, s, e).New != 4 {
				t.Fatal("running job consumed new selection")
			}
			close(f.gate)
			<-done
			f.gate = nil
			if got := status(t, s, e); got.Imported != int64(count) {
				t.Fatal("selected job did not import exactly its original assets", got)
			}
			reconcileNow(t, s)
			reconcileNow(t, s)
			if jobCount(t, s) != 3 || status(t, s, e).Pending != 4 {
				t.Fatal("follow-up did not coalesce", status(t, s, e))
			}
			runQueued(t, s)
			reconcileNow(t, s)
			noQueued(t, s)
			if status(t, s, e).Imported != int64(count+4) {
				t.Fatal("stranded uploads")
			}
		})
	}
}

func TestAutoLostWakeAtCompletionAndRestart(t *testing.T) {
	s, _, upload, e := jobsFixture(t, 1)
	autoReady(t, s, e)
	reconcileNow(t, s)
	j, err := s.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Both sides cross the completion boundary concurrently. Neither sends an
	// upload wake. A later durable scan must find the new asset in either order.
	start := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); <-start; s.execute(t.Context(), j) }()
	close(start)
	upload("completion-boundary.png")
	<-done
	restarted := New(s.db, s.media, s.targets, s.logger)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() { defer close(stopped); restarted.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	waitStatus(t, restarted, e, func(v Status) bool { return v.Imported == 2 && v.Job.Status == "completed" })
	// Arrives after the restarted worker's first reconciliation and all queued
	// work. Only the periodic backstop can find this upload; there is no wake.
	upload("periodic-no-wake.png")
	waitStatus(t, restarted, e, func(v Status) bool { return v.Imported == 3 && v.Job.Status == "completed" }, 2*reconcileInterval+time.Second)
}

func TestAutoFailureIsolationAndManualRetry(t *testing.T) {
	for _, failure := range []string{"validation", "upload", "assignment"} {
		t.Run(failure, func(t *testing.T) {
			s, f, upload, e := jobsFixture(t, 2)
			autoReady(t, s, e)
			switch failure {
			case "validation":
				f.validateErr = ErrUnavailable
			case "upload":
				f.failUpload = true
			case "assignment":
				f.failAssign = true
			}
			reconcileNow(t, s)
			runQueued(t, s)
			first := status(t, s, e)
			if first.Job.Status != "failed" || first.Failed == 0 || first.Pending != 0 {
				t.Fatal(first)
			}
			for range 5 {
				reconcileNow(t, s)
			}
			noQueued(t, s)
			if jobCount(t, s) != 2 {
				t.Fatal("failed work auto-retried")
			}
			f.validateErr = nil
			upload("independent-new.png")
			reconcileNow(t, s)
			runQueued(t, s)
			if got := status(t, s, e); got.Failed != first.Failed || got.Imported+got.Duplicate != first.Imported+first.Duplicate+1 {
				t.Fatal("new work retried old failures", got)
			}
			enqueue(t, s, e, "retry")
			runQueued(t, s)
			if got := status(t, s, e); got.Failed != 0 || got.Imported+got.Duplicate != 3 {
				t.Fatal("manual retry failed", got)
			}
		})
	}
}

func TestAutoOffDisableAndCancellation(t *testing.T) {
	s, f, upload, e := jobsFixture(t, 2)
	if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	reconcileNow(t, s)
	noQueued(t, s)
	if err := s.SetAutoImport(t.Context(), e.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	reconcileNow(t, s)
	if err := s.SetAutoImport(t.Context(), e.ID, "test", false); err != nil {
		t.Fatal(err)
	}
	upload("after-disable.png")
	runQueued(t, s)
	reconcileNow(t, s)
	noQueued(t, s)
	if got := status(t, s, e); got.AutoImport || got.Imported != 2 || got.New != 1 || f.accepted != 2 {
		t.Fatal(got)
	}
	enqueue(t, s, e, "new")
	runQueued(t, s)
	if status(t, s, e).Imported != 3 {
		t.Fatal("manual-only send broken")
	}
	if err := s.SetAutoImport(t.Context(), e.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	upload("cancel.png")
	reconcileNow(t, s)
	if err := s.Cancel(t.Context(), e.ID, "test"); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	upload("after-cancel.png")
	reconcileNow(t, s)
	noQueued(t, s)
	enqueue(t, s, e, "new")
	runQueued(t, s)
	if status(t, s, e).Imported != 5 {
		t.Fatal("cancelled selection lost")
	}
	if err := s.media.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	if f.accepted != 5 || f.albumCreates != 1 {
		t.Fatal("deletion changed remote copies")
	}
}

func TestAutoProvisioningAndFailedProvisionRetry(t *testing.T) {
	s, f, _, _ := jobsFixture(t, 0)
	on := true
	e, err := s.CreateEvent(t.Context(), events.Input{Name: "Automatic", Enabled: &on}, "test", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// Prepare guest media on the new event without scheduling integration work.
	session, err := s.media.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	uploadAutoPhoto(t, s, e, session.ID, "during-provision.png")
	reconcileNow(t, s)
	if got := status(t, s, e); !got.AutoImport || got.Pending != 0 || got.New != 1 {
		t.Fatal(got)
	}
	f.validateErr = ErrUnavailable
	runQueued(t, s)
	for range 3 {
		reconcileNow(t, s)
	}
	noQueued(t, s)
	if jobCount(t, s) != 1 {
		t.Fatal("failed provisioning auto-retried")
	}
	f.validateErr = nil
	if _, err := s.Provision(t.Context(), e.ID, "test", ""); err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	if got := status(t, s, e); got.Imported != 0 || got.New != 1 {
		t.Fatal("provisioning imported media", got)
	}
	reconcileNow(t, s)
	if jobCount(t, s) != 3 {
		t.Fatal("no follow-up after provisioning")
	}
	runQueued(t, s)
	if status(t, s, e).Imported != 1 {
		t.Fatal("waiting media stranded")
	}
}

func TestAutoZeroMediaNeedsOnlyProvisioning(t *testing.T) {
	s, _, _, _ := jobsFixture(t, 0)
	on := true
	e, err := s.CreateEvent(t.Context(), events.Input{Name: "Empty automatic event", Enabled: &on}, "test", "", true)
	if err != nil {
		t.Fatal(err)
	}
	runQueued(t, s)
	for range 3 {
		reconcileNow(t, s)
	}
	noQueued(t, s)
	if v := status(t, s, e); jobCount(t, s) != 1 || !v.AutoImport || v.AlbumID != albumUUID || v.Total != 0 {
		t.Fatal(v)
	}
}

func TestAutoSelectionAndJobCommitAtomically(t *testing.T) {
	s, _, _, e := jobsFixture(t, 2)
	autoReady(t, s, e)
	if _, err := s.db.Exec("CREATE TRIGGER fail_auto_job BEFORE INSERT ON integration_jobs BEGIN SELECT RAISE(ABORT,'queue failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcile(t.Context()); err == nil {
		t.Fatal("queue failure accepted")
	}
	if got := status(t, s, e); got.Pending != 0 || got.New != 2 {
		t.Fatal("selection stranded without job", got)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_auto_job"); err != nil {
		t.Fatal(err)
	}
	reconcileNow(t, s)
	runQueued(t, s)
}

func TestAutoBoundedAndIndependentTargets(t *testing.T) {
	s, _, _, e := jobsFixture(t, 0)
	autoReady(t, s, e)
	// More eligible bindings than one page prove cursor progress and wraparound.
	for i := 2; i <= reconcileBindings+2; i++ {
		if _, err := s.db.Exec("INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(?,?,?,?)", i, fmt.Sprint("t-", i), "http://example.test", now()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,album_state,immich_album_id,auto_import,created_at,updated_at) VALUES(?,?,'Album',?,'ready',?,1,?,?)", e.ID, i, fmt.Sprint("marker-", i), albumUUID, now(), now()); err != nil {
			t.Fatal(err)
		}
	}
	// Insert real ready sources efficiently through the fixture's upload path.
	session, err := s.media.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range reconcileAssets + 1 {
		if i%100 == 0 {
			session, err = s.media.CreateSession(t.Context(), e.PublicID)
			if err != nil {
				t.Fatal(err)
			}
		}
		uploadAutoPhoto(t, s, e, session.ID, fmt.Sprint(i, ".png"))
	}
	reconcileNow(t, s)
	if jobCount(t, s) != 1+reconcileBindings {
		t.Fatal("unbounded binding page")
	}
	first, err := s.Status(t.Context(), e.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Pending != reconcileAssets {
		t.Fatal("unbounded asset selection")
	}
	reconcileNow(t, s)
	if jobCount(t, s) != 1+reconcileBindings+2 {
		t.Fatal("binding cursor stranded later targets")
	}
	if err := s.SetAutoImport(t.Context(), e.ID, "test", false); err != nil {
		t.Fatal(err)
	}
	var other bool
	if err := s.db.QueryRow("SELECT auto_import FROM immich_event_imports WHERE target_id=2").Scan(&other); err != nil || !other {
		t.Fatal("policy leaked across targets", err)
	}
}
