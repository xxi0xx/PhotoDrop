import { test } from 'node:test';
import assert from 'node:assert/strict';
import { importView, initialTarget, suggestedAlbum } from '../src/lib/immich.ts';
const base = { target: { key: 'home', available: true }, total: 10, imported: 6, duplicate: 1, failed: 1, pending: 0, new: 2 };
test('new-event defaults and album name follow only before manual editing', () => {
  const targets = [{ key: 'offline', available: false }, { key: 'home', available: true }];
  assert.equal(initialTarget({ active_target: '', targets: [] }), '');
  assert.equal(initialTarget({ active_target: 'offline', targets }), 'offline');
  assert.equal(initialTarget({ active_target: '', targets }), 'home');
  assert.equal(suggestedAlbum('Wedding', null), 'Wedding');
  assert.equal(suggestedAlbum('Wedding renamed', null), 'Wedding renamed');
  assert.equal(suggestedAlbum('Wedding renamed', 'Our album'), 'Our album');
  assert.equal(suggestedAlbum('Wedding renamed', ''), '');
});
test('album-only setup works without media, blocks duplicate work, and allows manual retry', () => {
  const empty = { ...base, total: 0, imported: 0, duplicate: 0, failed: 0, pending: 0, new: 0 };
  assert.equal(importView(empty, false).canProvision, true);
  assert.equal(importView(empty, false).canSend, false);
  for (const state of ['queued', 'running']) assert.equal(importView({ ...empty, job: { status: state } }, false).canProvision, false);
  assert.equal(importView({ ...empty, job: { status: 'failed' } }, false).canProvision, true);
  assert.equal(importView({ ...empty, job: { status: 'failed' } }, false).albumLabel, 'Album setup needs attention');
  assert.equal(importView({ ...empty, target: { available: false } }, false).canProvision, false);
  assert.equal(importView({ ...empty, album_id: 'saved' }, false).canProvision, false);
  const laterUpload = { ...empty, album_id: 'saved', total: 1, new: 1 };
  assert.equal(importView(laterUpload, false).canSend, true);
  assert.equal(importView(laterUpload, false).canRetry, false);
  const selected = { ...empty, pending: 1, job: { status: 'failed' } };
  assert.equal(importView(selected, false).canProvision, false);
  assert.equal(importView(selected, false).canRetry, true);
});
test('incremental import and failed retry counts remain separate', () => {
  const view = importView(base, false);
  assert.equal(view.accounted, 7); assert.equal(view.sendCount, 2);
  assert.equal(view.canSend, true); assert.equal(view.canRetry, true);
});
test('running, cancellation, and unavailable credentials disable duplicate submissions', () => {
  for (const status of ['queued', 'running']) {
    const view = importView({ ...base, job: { status } }, false);
    assert.equal(view.active, true); assert.equal(view.canSend, false); assert.equal(view.canRetry, false);
  }
  assert.equal(importView({ ...base, job: { status: 'running', cancel_requested: true } }, false).label, 'Cancelling import…');
  assert.equal(importView({ ...base, target: { available: false } }, false).canSend, false);
  assert.equal(importView(base, true).canSend, false);
  assert.equal(importView(base, false, true).canRetry, false);
});
test('completed status discovers new ready photos and cancelled work stays retryable', () => {
  const complete = { ...base, imported: 10, duplicate: 0, failed: 0, new: 0, job: { status: 'completed' } };
  assert.equal(importView(complete, false).canSend, false);
  assert.equal(importView(complete, false).label, 'Import completed');
  assert.equal(importView({ ...complete, total: 11, new: 1 }, false).sendCount, 1);
  assert.equal(importView({ ...complete, pending: 3, job: { status: 'cancelled' } }, false).canSend, true);
});
