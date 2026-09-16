package immich

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/media"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
)

type fakeAPI struct {
	mu                                                         sync.Mutex
	uploads                                                    map[string]int
	ids                                                        map[string]string
	inflight, maxInflight, albumCreates, assignments, accepted int
	failUpload, failAssign, lostResponse                       bool
	blockAfter                                                 int
	gate, entered                                              chan struct{}
	marker                                                     string
}

func (f *fakeAPI) Validate(context.Context) (Version, error) { return Version{3, 2, 1}, nil }
func (f *fakeAPI) CreateAlbum(_ context.Context, name, marker string) (Album, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.albumCreates++
	f.marker = marker
	return Album{albumUUID, name, marker}, nil
}
func (f *fakeAPI) Album(context.Context, string) (Album, error) {
	return Album{albumUUID, "Externally renamed", f.marker}, nil
}
func (f *fakeAPI) FindAlbum(_ context.Context, marker string) (Album, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if marker == f.marker {
		return Album{albumUUID, "Renamed", marker}, nil
	}
	return Album{}, ErrAlbumUncertain
}
func (f *fakeAPI) Upload(ctx context.Context, name, _ string, _ int64, r io.Reader) (Upload, error) {
	f.mu.Lock()
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	block := f.blockAfter > 0 && f.accepted >= f.blockAfter
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inflight--; f.mu.Unlock() }()
	if f.gate != nil {
		f.entered <- struct{}{}
		select {
		case <-f.gate:
		case <-ctx.Done():
			return Upload{}, ctx.Err()
		}
	}
	if block {
		<-ctx.Done()
		return Upload{}, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return Upload{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return Upload{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[name]++
	if f.failUpload {
		f.failUpload = false
		return Upload{}, ErrUnavailable
	}
	if id := f.ids[name]; id != "" {
		return Upload{id, "duplicate"}, nil
	}
	id := fmt.Sprintf("%08x-1111-4111-8111-111111111111", len(f.ids)+1)
	f.ids[name] = id
	f.accepted++
	if f.lostResponse {
		f.lostResponse = false
		return Upload{}, ErrUnavailable
	}
	return Upload{id, "created"}, nil
}
func (f *fakeAPI) AddToAlbum(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assignments++
	if f.failAssign {
		f.failAssign = false
		return ErrUnavailable
	}
	return nil
}

func jobsFixture(t *testing.T, count int) (*Service, *fakeAPI, func(string), events.Event) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	objects, err := storage.NewLocal(filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	source := media.New(db, objects, logger)
	on := true
	e, err := events.New(db).Create(t.Context(), events.Input{Name: "Import test", Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	session, err := source.CreateSession(t.Context(), e.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(name string) {
		t.Helper()
		data := testutil.Images()["image/png"]
		if _, err := source.Upload(t.Context(), e.PublicID, session.ID, name, "image/png", bytes.NewReader(data), int64(len(data)), 1024); err != nil {
			t.Fatal(err)
		}
	}
	for i := range count {
		upload(fmt.Sprintf("%02d.png", i))
	}
	fake := &fakeAPI{uploads: map[string]int{}, ids: map[string]string{}}
	if _, err := db.Exec("INSERT INTO immich_targets(id,key,base_url,created_at) VALUES(1,'test','http://immich.test',?)", now()); err != nil {
		t.Fatal(err)
	}
	targets := &Targets{ActiveKey: "test", entries: map[int64]Target{1: {ID: 1, Key: "test", Available: true, client: fake}}}
	return New(db, source, targets, logger), fake, upload, e
}
func runQueued(t *testing.T, s *Service) {
	t.Helper()
	j, err := s.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), j)
}
func enqueue(t *testing.T, s *Service, e events.Event, mode string) {
	t.Helper()
	if _, err := s.Enqueue(t.Context(), e.ID, "", "", mode); err != nil {
		t.Fatal(err)
	}
}
func status(t *testing.T, s *Service, e events.Event) Status {
	t.Helper()
	v, err := s.Status(t.Context(), e.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJobsProgressRetryIncrementalAndDeletion(t *testing.T) {
	s, f, upload, e := jobsFixture(t, 7)
	// A pending source row never enters an import record or progress total.
	if _, err := s.db.Exec(`INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_backend_id) SELECT 'pending',event_id,id,'pending.png','pending','pending',created_at,1 FROM upload_sessions LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	f.failUpload = true
	enqueue(t, s, e, "new")
	if _, err := s.Enqueue(t.Context(), e.ID, "", "other", "new"); !errors.Is(err, ErrActive) {
		t.Fatal("duplicate job accepted", err)
	}
	runQueued(t, s)
	v := status(t, s, e)
	if v.Imported != 6 || v.Failed != 1 || v.Total != 7 || v.Job.Status != "failed" {
		t.Fatalf("progress %+v", v)
	}
	if f.maxInflight < 1 || f.maxInflight > 3 {
		t.Fatal("wrong bounded concurrency", f.maxInflight)
	}
	enqueue(t, s, e, "retry")
	runQueued(t, s)
	v = status(t, s, e)
	if v.Imported != 7 || v.Failed != 0 || v.Job.Status != "completed" {
		t.Fatal("retry did not recover", v)
	}
	totalUploads := 0
	for _, n := range f.uploads {
		totalUploads += n
	}
	if totalUploads != 8 {
		t.Fatal("retry uploaded already-accounted assets", totalUploads)
	}
	upload("new.png")
	if v := status(t, s, e); v.New != 1 {
		t.Fatal("new count", v)
	}
	enqueue(t, s, e, "new")
	runQueued(t, s)
	if v := status(t, s, e); v.Imported != 8 || v.New != 0 {
		t.Fatal(v)
	}
	if f.albumCreates != 1 {
		t.Fatal("album identity recreated")
	}
	// Drop the intentionally invalid pending fixture before ordinary deletion.
	if _, err := s.db.Exec("DELETE FROM assets WHERE id='pending'"); err != nil {
		t.Fatal(err)
	}
	if err := s.media.DeleteEvent(t.Context(), e.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"immich_event_imports", "immich_asset_imports", "integration_jobs"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("integration metadata survived deletion", table, err)
		}
	}
	if len(f.ids) != 8 || f.albumCreates != 1 {
		t.Fatal("copies disappeared")
	}
}

func TestAssignmentFailureAndLostUploadResponse(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint("lost response ", lost), func(t *testing.T) {
			s, f, _, e := jobsFixture(t, 1)
			f.lostResponse = lost
			f.failAssign = !lost
			enqueue(t, s, e, "new")
			runQueued(t, s)
			if v := status(t, s, e); v.Failed != 1 {
				t.Fatal(v)
			}
			enqueue(t, s, e, "retry")
			runQueued(t, s)
			v := status(t, s, e)
			if v.Imported+v.Duplicate != 1 || v.Failed != 0 {
				t.Fatal(v)
			}
			expected := 1
			if lost {
				expected = 2
				if v.Duplicate != 1 {
					t.Fatal("lost-response dedup not accounted")
				}
			}
			if f.uploads["00.png"] != expected || len(f.ids) != 1 {
				t.Fatal("reuploaded known ID or duplicated uncertain upload")
			}
		})
	}
}

func waitStatus(t *testing.T, s *Service, e events.Event, predicate func(Status) bool) Status {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		v := status(t, s, e)
		if predicate(v) {
			return v
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out: %+v", v)
		case <-ticker.C:
		}
	}
}
func TestWorkerRestartAndCancellation(t *testing.T) {
	s, f, _, e := jobsFixture(t, 20)
	f.blockAfter = 8
	enqueue(t, s, e, "new")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	v := waitStatus(t, s, e, func(v Status) bool { return v.Imported >= 8 })
	if err := s.media.DeleteEvent(t.Context(), e.ID); !errors.Is(err, media.ErrBusy) {
		t.Fatal("deletion raced import", err)
	}
	cancel()
	<-done
	var successful []string
	rows, err := s.db.Query("SELECT a.original_filename FROM immich_asset_imports i JOIN assets a ON a.id=i.asset_id WHERE i.status='imported'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		successful = append(successful, name)
	}
	rows.Close()
	if len(successful) < 8 {
		t.Fatal("lost committed progress")
	}
	f.mu.Lock()
	f.blockAfter = 0
	f.mu.Unlock()
	// A new Service represents the restarted process, sharing only persisted DB
	// and independent source media. Recovery retains known remote asset IDs.
	restarted := New(s.db, s.media, s.targets, s.logger)
	ctx2, cancel2 := context.WithCancel(t.Context())
	done2 := make(chan struct{})
	go func() { defer close(done2); restarted.Run(ctx2) }()
	defer func() { cancel2(); <-done2 }()
	waitStatus(t, restarted, e, func(v Status) bool { return v.Job.Status == "completed" })
	f.mu.Lock()
	for _, name := range successful {
		if f.uploads[name] != 1 {
			t.Error("restart reuploaded accounted asset", name)
		}
	}
	if f.maxInflight > 3 {
		t.Error("unbounded concurrency")
	}
	f.mu.Unlock()
	var jobs int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM integration_jobs").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("restart created duplicate job", err)
	}
	if v.Total != 20 {
		t.Fatal("wrong snapshot")
	}
}

func TestQueuedCancellationAndConcurrentClaims(t *testing.T) {
	s, f, _, e := jobsFixture(t, 3)
	enqueue(t, s, e, "new")
	var mu sync.Mutex
	var claimed []Job
	var group sync.WaitGroup
	for range 6 {
		group.Go(func() {
			j, err := s.claim(t.Context())
			if err == nil {
				mu.Lock()
				claimed = append(claimed, j)
				mu.Unlock()
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if len(claimed) != 1 {
		t.Fatal("duplicate claims", len(claimed))
	}
	if err := s.Cancel(t.Context(), e.ID, ""); err != nil {
		t.Fatal(err)
	}
	s.execute(t.Context(), claimed[0])
	if v := status(t, s, e); v.Job.Status != "cancelled" {
		t.Fatal(v)
	}
	if f.accepted != 0 {
		t.Fatal("cancelled job uploaded assets")
	}
	enqueue(t, s, e, "new")
	runQueued(t, s)
	if v := status(t, s, e); v.Imported != 3 {
		t.Fatal("cancelled work did not resume", v)
	}
}

func TestUncertainAlbumCreationReconcilesMarker(t *testing.T) {
	s, f, _, e := jobsFixture(t, 1)
	enqueue(t, s, e, "new")
	var importID int64
	var marker string
	if err := s.db.QueryRow("SELECT id,album_marker FROM immich_event_imports").Scan(&importID, &marker); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE immich_event_imports SET album_state='creating'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.album(t.Context(), f, importID); !errors.Is(err, ErrAlbumUncertain) {
		t.Fatal(err)
	}
	if f.albumCreates != 0 {
		t.Fatal("blindly repeated uncertain album creation")
	}
	f.marker = marker
	runQueued(t, s)
	if v := status(t, s, e); v.Imported != 1 || v.AlbumID != albumUUID {
		t.Fatal(v)
	}
	if f.albumCreates != 0 {
		t.Fatal("created duplicate album")
	}
}

func TestThreeTransfersRunWhileFourthWaits(t *testing.T) {
	s, f, _, e := jobsFixture(t, 4)
	f.gate = make(chan struct{})
	f.entered = make(chan struct{}, 4)
	var once sync.Once
	release := func() { once.Do(func() { close(f.gate) }) }
	defer release()
	enqueue(t, s, e, "new")
	j, err := s.claim(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.execute(ctx, j) }()
	defer func() { release(); cancel(); <-done }()
	for range 3 {
		select {
		case <-f.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("three transfers did not start")
		}
	}
	select {
	case <-f.entered:
		t.Fatal("fourth upload ran before a slot was released")
	case <-time.After(20 * time.Millisecond):
	}
	// Release exactly one transfer; the other two remain blocked. The fourth
	// must enter using only that newly available slot.
	f.gate <- struct{}{}
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fourth upload did not use the released slot")
	}
	release()
	<-done
	if v := status(t, s, e); v.Imported != 4 {
		t.Fatal(v)
	}
	if f.maxInflight != 3 {
		t.Fatal("incorrect concurrency", f.maxInflight)
	}
}
