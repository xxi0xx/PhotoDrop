import { test } from 'node:test';
import assert from 'node:assert/strict';
import { importView } from '../src/lib/immich.ts';
const base = { target: { key: 'home', available: true }, total: 10, imported: 6, duplicate: 1, failed: 1, pending: 0, new: 2 };
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
