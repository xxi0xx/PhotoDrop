// Local Compose upload integration. Fixtures and credentials never leave localhost.
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { randomUUID, createHash } from 'node:crypto';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';

const runAsync = promisify(execFile);

if (existsSync('.env')) process.loadEnvFile('.env');
const password = process.env.PHOTODROP_ADMIN_PASSWORD;
assert.ok(password, 'Configure PHOTODROP_ADMIN_PASSWORD first');
const base = 'http://localhost:8080';
const origin = new URL(process.env.PHOTODROP_BASE_URL || base).origin;
// Tiny valid PNG; no external fixture downloads.
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a4t8AAAAASUVORK5CYII=', 'base64');
let cookie = '', csrf = '';
const created = new Set();
async function json(method, path, data, expected = 200, admin = false) {
  const headers = { Origin: origin, 'Content-Type': 'application/json' };
  if (admin) { headers.Cookie = cookie; headers['X-CSRF-Token'] = csrf; }
  const response = await fetch(base + path, { method, headers, redirect: 'manual', body: data === undefined ? undefined : JSON.stringify(data) });
  assert.equal(response.status, expected, `${method} ${path}: unexpected HTTP status`);
  return { response, body: response.status === 204 ? null : await response.json() };
}
async function send(event, session, name, data = png, expected = 201) {
  const response = await fetch(`${base}/api/public/events/${event.public_id}/upload-sessions/${session.id}/assets`, {
    method: 'POST', headers: { Origin: origin, 'Content-Type': 'image/png', 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(name)}` }, body: data,
  });
  assert.equal(response.status, expected, 'unexpected upload result');
  const body = await response.json();
  if (expected === 201) {
    assert.equal(body.asset.status, 'ready'); assert.equal(body.asset.filename, name); assert.equal(body.asset.size, data.length);
    assert.match(body.asset.id, /^[a-f0-9]{32}$/);
    assert.equal(body.asset.event_id, undefined); assert.equal(body.asset.storage_key, undefined);
    return body.asset;
  }
  assert.ok(body.error?.code);
}
function stored(event, asset) {
  // Test-only inspection of the documented local layout. Production clients
  // receive no object keys and there is no public media download route.
  return `/data/uploads/e${event.id}_${asset.id}`;
}
function digest(path) { return execFileSync('docker', ['compose', 'exec', '-T', 'photodrop', 'sha256sum', path], { encoding: 'utf8' }).split(' ')[0]; }
function missing(path) {
  execFileSync('docker', ['compose', 'exec', '-T', 'photodrop', 'test', '!', '-e', path]);
}
try {
  const login = await json('POST', '/api/admin/login', { password });
  cookie = login.response.headers.getSetCookie()[0].split(';')[0]; csrf = login.body.csrf_token;
  const events = [];
  for (let i = 0; i < 2; i++) {
    const event = (await json('POST', '/api/admin/events', { name: `Upload smoke ${randomUUID()}`, enabled: true }, 201, true)).body.event;
    created.add(event.id); events.push(event);
  }
  const [first, second] = events;
  const firstSession = (await json('POST', `/api/public/events/${first.public_id}/upload-sessions`, {}, 201)).body.upload_session;
  const secondSession = (await json('POST', `/api/public/events/${second.public_id}/upload-sessions`, {}, 201)).body.upload_session;
  const [one, two, keep] = await Promise.all([send(first, firstSession, '../../etc/passwd'), send(first, firstSession, '../../etc/passwd'), send(second, secondSession, 'keep.png')]);
  assert.notEqual(one.id, two.id);
  const expectedDigest = createHash('sha256').update(png).digest('hex');
  for (const [event, asset] of [[first, one], [first, two], [second, keep]]) assert.equal(digest(stored(event, asset)), expectedDigest);
  await send(first, firstSession, 'fake.png', Buffer.from('not an image'), 415);
  await send(first, firstSession, 'empty.png', Buffer.alloc(0), 422);
  await send(second, firstSession, 'cross-event.png', png, 404);
  let stats = (await json('GET', `/api/admin/events/${first.id}`, undefined, 200, true)).body.event.media;
  assert.deepEqual(stats, { photo_count: 2, storage_bytes: png.length * 2 });
  await json('PUT', `/api/admin/events/${first.id}`, { name: first.name, enabled: false }, 200, true);
  await send(first, firstSession, 'closed.png', png, 409);
  await json('POST', `/api/public/events/${first.public_id}/upload-sessions`, {}, 409);
  await json('PUT', `/api/admin/events/${first.id}`, { name: first.name, enabled: true }, 200, true);
  // Keep fetch socket-close handling active while the test server restarts.
  await runAsync('docker', ['compose', 'restart']);
  await runAsync('docker', ['compose', 'up', '--wait', '--wait-timeout', '120', '-d']);
  assert.equal(digest(stored(first, one)), expectedDigest);
  stats = (await json('GET', `/api/admin/events/${first.id}`, undefined, 200, true)).body.event.media;
  assert.deepEqual(stats, { photo_count: 2, storage_bytes: png.length * 2 });
  await json('DELETE', `/api/admin/events/${first.id}`, undefined, 204, true); created.delete(first.id);
  missing(stored(first, one)); missing(stored(first, two));
  assert.equal(digest(stored(second, keep)), expectedDigest);
  await send(first, firstSession, 'deleted.png', png, 404);
  await json('DELETE', `/api/admin/events/${second.id}`, undefined, 204, true); created.delete(second.id);
  missing(stored(second, keep));
  await json('POST', '/api/admin/logout', undefined, 204, true);
  console.log('Gate 3 smoke checks passed: streamed images, duplicate/path-like filenames, validation, event isolation/state, counts, media/session persistence, and scoped deletion.');
} finally {
  for (const id of created) {
    try { await json('DELETE', `/api/admin/events/${id}`, undefined, 204, true); } catch { /* Keep the original failure. */ }
  }
}
