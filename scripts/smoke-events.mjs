// API smoke test against the local Compose service. Creates and removes only
// its own uniquely named events, and verifies its session across a restart.
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

if (existsSync('.env')) process.loadEnvFile('.env');
const password = process.env.PHOTODROP_ADMIN_PASSWORD;
assert.ok(password, 'Set PHOTODROP_ADMIN_PASSWORD or configure .env before running smoke tests');
const base = 'http://localhost:8080';
const origin = new URL(process.env.PHOTODROP_BASE_URL || base).origin;
let cookie = '';
let csrf = '';
const created = new Set();
async function call(method, path, data, expected = 200, authenticated = true, csrfOverride = csrf) {
  const headers = { Origin: origin };
  if (authenticated && cookie) headers.Cookie = cookie;
  if (authenticated && csrfOverride) headers['X-CSRF-Token'] = csrfOverride;
  if (data !== undefined) headers['Content-Type'] = 'application/json';
  const response = await fetch(base + path, { method, headers, redirect: 'manual', body: data === undefined ? undefined : JSON.stringify(data) });
  assert.equal(response.status, expected, `${method} ${path}: unexpected HTTP status`);
  const body = response.status === 204 ? null : await response.text();
  return { response, body, json: body && response.headers.get('content-type')?.includes('application/json') ? JSON.parse(body) : null };
}
const draft = { name: `Smoke event ${randomUUID()}`, description: '<b>This is plain text</b>', event_date: '2026-09-04', enabled: true, expires_at: null };
try {
  await call('GET', '/admin', undefined, 303, false);
  await call('GET', '/api/admin/events', undefined, 401, false);
  await call('POST', '/api/admin/login', { password: 'incorrect-password' }, 401, false);
  const login = await call('POST', '/api/admin/login', { password });
  const setCookie = login.response.headers.getSetCookie()[0];
  assert.match(setCookie, /HttpOnly/i); assert.match(setCookie, /SameSite=Lax/i);
  cookie = setCookie.split(';')[0]; csrf = login.json.csrf_token;
  const expires = login.json.expires_at;
  await call('POST', '/api/admin/events', draft, 403, true, '');
  const first = (await call('POST', '/api/admin/events', draft, 201)).json.event;
  created.add(first.id);
  const second = (await call('POST', '/api/admin/events', { ...draft, name: `Independent ${randomUUID()}` }, 201)).json.event;
  created.add(second.id);
  assert.notEqual(first.public_id, second.public_id);
  assert.match(first.public_id, /^[A-Za-z0-9_-]{24}$/);
  const path = `/api/admin/events/${first.id}`;
  const guest = `/api/public/events/${first.public_id}`;
  await call('GET', `/e/${first.public_id}`, undefined, 200, false);
  await call('GET', `/e/${first.id}`, undefined, 404, false);
  const opened = (await call('GET', guest, undefined, 200, false)).json.event;
  assert.equal(opened.status, 'open'); assert.equal(opened.description, draft.description);
  assert.equal(opened.id, undefined); assert.equal(opened.enabled, undefined);
  await call('PUT', path, { ...draft, enabled: false });
  assert.equal((await call('GET', guest, undefined, 200, false)).json.event.status, 'closed');
  assert.equal((await call('GET', `/api/public/events/${second.public_id}`, undefined, 200, false)).json.event.status, 'open');
  await call('PUT', path, { ...draft, expires_at: '2000-01-01T00:00:00Z' });
  assert.equal((await call('GET', guest, undefined, 200, false)).json.event.status, 'closed');
  const edited = (await call('PUT', path, { ...draft, name: `Edited ${draft.name}` })).json.event;
  assert.equal(edited.public_id, first.public_id);
  assert.equal(edited.created_at, first.created_at);
  execFileSync('docker', ['compose', 'restart'], { stdio: 'inherit' });
  execFileSync('docker', ['compose', 'up', '--wait', '--wait-timeout', '120', '-d'], { stdio: 'inherit' });
  const resumed = (await call('GET', '/api/admin/session')).json;
  assert.equal(resumed.csrf_token, csrf); assert.equal(resumed.expires_at, expires);
  assert.deepEqual((await call('GET', path)).json.event, edited);
  await call('GET', `/e/${first.public_id}`, undefined, 200, false);
  for (const id of [...created]) { await call('DELETE', `/api/admin/events/${id}`, undefined, 204); created.delete(id); }
  await call('GET', `/e/${first.public_id}`, undefined, 404, false);
  await call('GET', guest, undefined, 404, false);
  await call('POST', '/api/admin/logout', undefined, 204);
  await call('GET', '/api/admin/events', undefined, 401);
  await call('GET', '/admin', undefined, 303);
  console.log('Gate 2 smoke checks passed: login, CSRF, independent events, editing, closed/expired pages, restart persistence, deletion, and logout.');
} finally {
  for (const id of created) {
    try { await call('DELETE', `/api/admin/events/${id}`, undefined, 204); } catch { /* Preserve the original failure. */ }
  }
}
