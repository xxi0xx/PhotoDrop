package immich

import (
	"context"
	"database/sql"
	"time"
)

const reconcileInterval = 5 * time.Second
const reconcileBindings = 32
const reconcileAssets = 256

// SetAutoImport changes only the policy of an existing binding. It does not
// cancel a selection already owned by a job or contact either storage provider.
func (s *Service) SetAutoImport(ctx context.Context, eventID int64, key string, enabled bool) error {
	target, err := s.targets.ByKey(key)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE immich_event_imports SET auto_import=?,updated_at=? WHERE event_id=? AND target_id=? AND EXISTS(SELECT 1 FROM events WHERE id=? AND deleting=0)`, enabled, now(), eventID, target.ID, eventID)
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
	s.notify()
	return nil
}

// reconcile is a bounded, round-robin local scan, independent of upload wakes.
// It runs at startup, periodically, and after a job ends. Cursor state is merely
// an optimization: losing it cannot lose work. No Immich request occurs here.
func (s *Service) reconcile(ctx context.Context) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM immich_event_imports WHERE auto_import=1 AND album_state='ready' AND id>? ORDER BY id LIMIT ?`, s.reconcileAfter, reconcileBindings)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.reconcileBinding(ctx, id); err != nil {
			return err
		}
		s.reconcileAfter = id
	}
	if len(ids) < reconcileBindings {
		s.reconcileAfter = 0
	}
	return nil
}

func (s *Service) reconcileBinding(ctx context.Context, id int64) error {
	// BEGIN IMMEDIATE serializes this decision with manual Send/Retry, policy
	// changes and upload completion. The existing partial unique index is the
	// final guard against two queued/running jobs owning the same binding.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var eventID int64
	err = tx.QueryRowContext(ctx, `SELECT i.event_id FROM immich_event_imports i JOIN events e ON e.id=i.event_id
 WHERE i.id=? AND i.auto_import=1 AND i.album_state='ready' AND i.immich_album_id IS NOT NULL AND e.deleting=0
 AND NOT EXISTS(SELECT 1 FROM integration_jobs j WHERE j.event_import_id=i.id AND j.status IN ('queued','running'))
 AND NOT EXISTS(SELECT 1 FROM immich_asset_imports a WHERE a.event_import_id=i.id AND a.status IN ('pending','importing'))`, id).Scan(&eventID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	// Never reset a selected/failed row. Pending work left by explicit cancellation
	// belongs to the administrator's next manual Send, not an automatic restart.
	stamp := now()
	result, err := tx.ExecContext(ctx, `INSERT INTO immich_asset_imports(event_import_id,asset_id,created_at,updated_at)
 SELECT ?,a.id,?,? FROM assets a WHERE a.event_id=? AND a.status='ready'
 AND NOT EXISTS(SELECT 1 FROM immich_asset_imports i WHERE i.event_import_id=? AND i.asset_id=a.id)
 ORDER BY a.created_at,a.id LIMIT ?`, id, stamp, stamp, eventID, id, reconcileAssets)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO integration_jobs(event_import_id,mode,created_at) VALUES(?,'new',?)`, id, stamp); err != nil {
		return err
	}
	return tx.Commit()
}
