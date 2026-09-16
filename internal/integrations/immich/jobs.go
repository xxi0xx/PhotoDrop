package immich

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"photodrop/internal/media"
)

var ErrInput = errors.New("choose a configured target, new or retry mode, and an album name of at most 200 characters")
var ErrActive = errors.New("an import job is already queued or running for this event and target")

type Service struct {
	db      *sql.DB
	media   *media.Service
	targets *Targets
	logger  *slog.Logger
	runMu   sync.Mutex
	wake    chan struct{}
}

func New(db *sql.DB, source *media.Service, targets *Targets, logger *slog.Logger) *Service {
	return &Service{db: db, media: source, targets: targets, logger: logger, wake: make(chan struct{}, 1)}
}

type Job struct {
	ID                          int64  `json:"id"`
	Status                      string `json:"status"`
	Error                       string `json:"error"`
	CancelRequested             bool   `json:"cancel_requested"`
	importID, eventID, targetID int64
}
type Status struct {
	ActiveTarget string   `json:"active_target"`
	Targets      []Target `json:"targets"`
	Target       *Target  `json:"target,omitempty"`
	AlbumName    string   `json:"album_name"`
	AlbumID      string   `json:"album_id,omitempty"`
	Total        int64    `json:"total"`
	Imported     int64    `json:"imported"`
	Duplicate    int64    `json:"duplicate"`
	Failed       int64    `json:"failed"`
	Pending      int64    `json:"pending"`
	New          int64    `json:"new"`
	Job          *Job     `json:"job,omitempty"`
}

func (s *Service) Status(ctx context.Context, eventID int64, key string) (Status, error) {
	var name string
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM events WHERE id=?", eventID).Scan(&name); err != nil {
		return Status{}, err
	}
	result := Status{ActiveTarget: s.targets.ActiveKey, Targets: []Target{}, AlbumName: name}
	// Include persisted historical targets so their state remains inspectable
	// even if an operator has temporarily removed their API key.
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM immich_targets ORDER BY key")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		result.Targets = append(result.Targets, s.targets.entries[id])
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	t, err := s.targets.ByKey(key)
	if err != nil {
		if key != "" {
			return result, err
		}
		return result, nil
	}
	result.Target = &t
	var importID int64
	err = s.db.QueryRowContext(ctx, "SELECT id,album_name,COALESCE(immich_album_id,'') FROM immich_event_imports WHERE event_id=? AND target_id=?", eventID, t.ID).Scan(&importID, &result.AlbumName, &result.AlbumID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(i.status='imported'),0),COALESCE(SUM(i.status='duplicate'),0),COALESCE(SUM(i.status='failed'),0),COALESCE(SUM(i.status IN ('pending','importing')),0),COALESCE(SUM(i.asset_id IS NULL),0)
	 FROM assets a LEFT JOIN immich_asset_imports i ON i.asset_id=a.id AND i.event_import_id=? WHERE a.event_id=? AND a.status='ready'`, importID, eventID).Scan(&result.Total, &result.Imported, &result.Duplicate, &result.Failed, &result.Pending, &result.New)
	if err != nil {
		return result, err
	}
	var job Job
	err = s.db.QueryRowContext(ctx, "SELECT id,status,last_error,cancel_requested FROM integration_jobs WHERE event_import_id=? ORDER BY id DESC LIMIT 1", importID).Scan(&job.ID, &job.Status, &job.Error, &job.CancelRequested)
	if err == nil {
		result.Job = &job
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	return result, nil
}

func (s *Service) Test(ctx context.Context, key string) (Version, error) {
	t, err := s.targets.ByKey(key)
	if err != nil {
		return Version{}, err
	}
	c, err := s.targets.client(t.ID)
	if err != nil {
		return Version{}, err
	}
	v, err := c.Validate(ctx)
	if err == nil {
		s.logger.Info("Immich connection validated", "target", t.Key, "version", v.String())
	}
	return v, err
}

func (s *Service) Enqueue(ctx context.Context, eventID int64, key, name, mode string) (int64, error) {
	name = strings.TrimSpace(name)
	if (mode != "new" && mode != "retry") || utf8.RuneCountInString(name) > 200 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") {
		return 0, ErrInput
	}
	t, err := s.targets.ByKey(key)
	if err != nil {
		return 0, err
	}
	if _, err := s.targets.client(t.ID); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var eventName string
	var deleting bool
	if err := tx.QueryRowContext(ctx, "SELECT name,deleting FROM events WHERE id=?", eventID).Scan(&eventName, &deleting); err != nil {
		return 0, err
	}
	if deleting {
		return 0, media.ErrDeleting
	}
	if name == "" {
		name = eventName
	}
	var importID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM immich_event_imports WHERE event_id=? AND target_id=?", eventID, t.ID).Scan(&importID)
	if errors.Is(err, sql.ErrNoRows) {
		var token [16]byte
		rand.Read(token[:])
		result, err := tx.ExecContext(ctx, "INSERT INTO immich_event_imports(event_id,target_id,album_name,album_marker,created_at,updated_at) VALUES(?,?,?,?,?,?)", eventID, t.ID, name, "PhotoDrop import "+hex.EncodeToString(token[:]), now(), now())
		if err != nil {
			return 0, err
		}
		importID, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM integration_jobs WHERE event_import_id=? AND status IN ('queued','running'))", importID).Scan(&active); err != nil {
		return 0, err
	}
	if active {
		return 0, ErrActive
	}
	if _, err := tx.ExecContext(ctx, "UPDATE immich_event_imports SET album_name=?,updated_at=? WHERE id=? AND album_state='new'", name, now(), importID); err != nil {
		return 0, err
	}
	if mode == "new" {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO immich_asset_imports(event_import_id,asset_id,created_at,updated_at) SELECT ?,id,?,? FROM assets WHERE event_id=? AND status='ready'`, importID, now(), now(), eventID)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE immich_asset_imports SET status='pending',last_error='',updated_at=? WHERE event_import_id=? AND status IN ('failed','importing')", now(), importID)
	}
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO integration_jobs(event_import_id,mode,created_at) VALUES(?,?,?)", importID, mode, now())
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.logger.Info("Immich job created", "job_id", id, "event_id", eventID, "target", t.Key)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return id, nil
}

func (s *Service) Cancel(ctx context.Context, eventID int64, key string) error {
	t, err := s.targets.ByKey(key)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE integration_jobs SET cancel_requested=1 WHERE status IN ('queued','running') AND event_import_id IN (SELECT id FROM immich_event_imports WHERE event_id=? AND target_id=?)`, eventID, t.ID)
	return err
}

// Run belongs to the server lifetime, not its short startup context. One job
// runs at a time with three asset transfers; SQLite claims are transactional.
func (s *Service) Run(ctx context.Context) {
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	// Only one PhotoDrop server may own /data. The export CLI starts no worker.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	recovered := false
	for ctx.Err() == nil {
		if !recovered {
			if err := s.recover(ctx); err != nil {
				s.logger.Warn("Immich recovery deferred")
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				continue
			}
			recovered = true
		}
		job, err := s.claim(ctx)
		if err == nil {
			s.execute(ctx, job)
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			s.logger.Warn("Immich job claim deferred")
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Service) recover(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE immich_asset_imports SET status='pending',updated_at=? WHERE status='importing'", now()); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, "UPDATE integration_jobs SET status='queued',last_error='' WHERE status='running'")
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n > 0 {
		s.logger.Info("Immich jobs resumed", "job_count", n)
	}
	return nil
}
func (s *Service) claim(ctx context.Context) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var j Job
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.event_import_id,i.event_id,i.target_id FROM integration_jobs j JOIN immich_event_imports i ON i.id=j.event_import_id WHERE j.status='queued' ORDER BY j.id LIMIT 1`).Scan(&j.ID, &j.importID, &j.eventID, &j.targetID)
	if err != nil {
		return j, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE integration_jobs SET status='running',started_at=? WHERE id=? AND status='queued'", now(), j.ID); err != nil {
		return j, err
	}
	return j, tx.Commit()
}

func (s *Service) execute(parent context.Context, j Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var alreadyCancelled bool
	if err := s.db.QueryRowContext(ctx, "SELECT cancel_requested FROM integration_jobs WHERE id=?", j.ID).Scan(&alreadyCancelled); err != nil {
		return
	}
	if alreadyCancelled {
		cancel()
	}
	// Persisted cancellation works before startup and after restart. Poll only
	// for this active job and stop the monitor before returning/releasing locks.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			var requested bool
			err := s.db.QueryRowContext(ctx, "SELECT cancel_requested FROM integration_jobs WHERE id=?", j.ID).Scan(&requested)
			if requested || errors.Is(err, sql.ErrNoRows) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { cancel(); <-done }()
	s.logger.Info("Immich job started", "job_id", j.ID, "event_id", j.eventID)
	err := s.importJob(ctx, j)
	if parent.Err() != nil {
		return
	} // Leave running/pending state for startup recovery.
	finish, cancelFinish := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFinish()
	status, message := "completed", ""
	if err != nil {
		status, message = "failed", safeError(err)
	}
	var requested bool
	if err := s.db.QueryRowContext(finish, "SELECT cancel_requested FROM integration_jobs WHERE id=?", j.ID).Scan(&requested); err != nil {
		return
	}
	if requested {
		status, message = "cancelled", "Unfinished photos can be sent again"
	}
	if _, err := s.db.ExecContext(finish, "UPDATE integration_jobs SET status=?,finished_at=?,last_error=? WHERE id=?", status, now(), message, j.ID); err != nil {
		s.logger.Error("Immich job progress could not be saved", "job_id", j.ID)
		return
	}
	s.logger.Info("Immich job finished", "job_id", j.ID, "status", status)
}

func (s *Service) album(ctx context.Context, c API, importID int64) (string, error) {
	var id, name, marker, state string
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(immich_album_id,''),album_name,album_marker,album_state FROM immich_event_imports WHERE id=?", importID).Scan(&id, &name, &marker, &state); err != nil {
		return "", err
	}
	if id != "" {
		_, err := c.Album(ctx, id)
		return id, err
	}
	var album Album
	var err error
	if state == "creating" {
		album, err = c.FindAlbum(ctx, marker)
	} else {
		// Persist intent before the request. A lost create response is resolved
		// by a random description marker, never by a possibly nonunique name.
		if _, err := s.db.ExecContext(ctx, "UPDATE immich_event_imports SET album_state='creating',updated_at=? WHERE id=?", now(), importID); err != nil {
			return "", err
		}
		album, err = c.CreateAlbum(ctx, name, marker)
	}
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE immich_event_imports SET immich_album_id=?,album_state='ready',updated_at=? WHERE id=?", album.ID, now(), importID)
	return album.ID, err
}

func (s *Service) importJob(ctx context.Context, j Job) error {
	client, err := s.targets.client(j.targetID)
	if err != nil {
		return err
	}
	if _, err := client.Validate(ctx); err != nil {
		return err
	}
	snapshot, err := s.media.Snapshot(ctx, j.eventID)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	album, err := s.album(ctx, client, j.importID)
	if err != nil {
		return err
	}
	queue := make(chan media.ReadyAsset)
	var workers sync.WaitGroup
	var failed bool
	var mu sync.Mutex
	for range 3 {
		workers.Go(func() {
			for a := range queue {
				if ctx.Err() != nil {
					continue
				}
				if err := s.importAsset(ctx, j, client, album, snapshot, a); err != nil {
					mu.Lock()
					failed = true
					mu.Unlock()
				}
			}
		})
	}
	for _, a := range snapshot.Assets {
		select {
		case <-ctx.Done():
			break
		case queue <- a:
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(queue)
	workers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failed {
		return errors.New("one or more assets failed")
	}
	return nil
}

func (s *Service) importAsset(ctx context.Context, j Job, c API, album string, snapshot *media.Snapshot, a media.ReadyAsset) (resultErr error) {
	var remote, status string
	var duplicate bool
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(immich_asset_id,''),status,duplicate FROM immich_asset_imports WHERE event_import_id=? AND asset_id=?", j.importID, a.ID).Scan(&remote, &status, &duplicate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE immich_asset_imports SET status='importing',attempt_count=attempt_count+1,updated_at=? WHERE event_import_id=? AND asset_id=?", now(), j.importID, a.ID); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			// Keep a known remote ID even if album assignment fails. A retry then
			// only assigns it. Uncertain upload outcomes rely on Immich deduplication.
			finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			state := "failed"
			if ctx.Err() != nil {
				state = "pending"
			}
			if _, err := s.db.ExecContext(finish, "UPDATE immich_asset_imports SET status=?,last_error=?,updated_at=? WHERE event_import_id=? AND asset_id=?", state, safeError(resultErr), now(), j.importID, a.ID); err != nil {
				s.logger.Error("Immich asset progress could not be saved", "asset_id", a.ID)
			}
			s.logger.Warn("Immich asset failed", "job_id", j.ID, "asset_id", a.ID, "error", safeError(resultErr))
		}
	}()
	if remote == "" {
		stream, _, err := snapshot.Open(ctx, a)
		if err != nil {
			return err
		}
		defer stream.Close()
		upload, err := c.Upload(ctx, a.Filename, a.UploadedAt, a.Size, stream)
		if err != nil {
			return err
		}
		remote, duplicate = upload.ID, upload.Status == "duplicate"
		if _, err := s.db.ExecContext(ctx, "UPDATE immich_asset_imports SET immich_asset_id=?,duplicate=?,updated_at=? WHERE event_import_id=? AND asset_id=?", remote, duplicate, now(), j.importID, a.ID); err != nil {
			return err
		}
	}
	if err := c.AddToAlbum(ctx, album, remote); err != nil {
		return err
	}
	status = "imported"
	if duplicate {
		status = "duplicate"
	}
	_, err = s.db.ExecContext(ctx, "UPDATE immich_asset_imports SET status=?,last_error='',imported_at=?,updated_at=? WHERE event_import_id=? AND asset_id=?", status, now(), now(), j.importID, a.ID)
	if err == nil {
		s.logger.Info("Immich asset accounted", "job_id", j.ID, "asset_id", a.ID, "status", status)
	}
	return err
}
