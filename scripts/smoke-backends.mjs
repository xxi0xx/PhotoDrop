// Isolated Compose integration: never loads .env or touches production data.
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const composeArgs = ['compose', '-f', 'scripts/compose-backends-test.yml'];
const base = 'http://localhost:8081';
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a4t8AAAAASUVORK5CYII=', 'base64');
let cookie = '', csrf = '';
function compose(args, active = 'local-default', configured = 's3-a,s3-b') {
  return execFileSync('docker', [...composeArgs, ...args], { stdio: 'inherit', env: {
    ...process.env, PHOTODROP_TEST_PROVIDER: active === 'local-default' ? 'local' : 's3',
    PHOTODROP_TEST_BACKEND_KEY: active, PHOTODROP_TEST_BACKENDS: configured,
  } });
}
function restart(active, configured) { compose(['up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', 'photodrop'], active, configured); }
async function json(method, path, data, status = 200, admin = false) {
  const headers = { Origin: base, 'Content-Type': 'application/json' };
  if (admin) { headers.Cookie = cookie; headers['X-CSRF-Token'] = csrf; }
  const res = await fetch(base + path, { method, headers, body: data === undefined ? undefined : JSON.stringify(data), redirect: 'manual' });
  assert.equal(res.status, status, `${method} ${path}: unexpected status`);
  return { res, body: status === 204 ? null : await res.json() };
}
async function session(event) { return (await json('POST', `/api/public/events/${event.public_id}/upload-sessions`, {}, 201)).body.upload_session.id; }
function assetBase(event, session) { return `/api/public/events/${event.public_id}/upload-sessions/${session}/assets`; }
async function prepare(event, session) {
  return (await json('POST', assetBase(event, session) + '/prepare', { filename: 'same.png', size: png.length, content_type: 'image/png', request_id: randomBytes(16).toString('hex') }, 201)).body;
}
async function put(prepared) {
  const res = await fetch(prepared.upload.url, { method: prepared.upload.method, headers: prepared.upload.headers, body: png });
  assert.equal(res.status, 200, 'direct PUT failed');
}
async function complete(event, session, prepared) { return (await json('POST', assetBase(event, session) + `/${prepared.asset.id}/complete`, {})).body.asset; }
async function observations(port) { return (await fetch(`http://localhost:${port}/__test/requests`)).json(); }

compose(['up', '--build', '-d', '--wait', '--wait-timeout', '180']);
const login = await json('POST', '/api/admin/login', { password: 'temporary-backends-browser-password' });
cookie = login.res.headers.getSetCookie()[0].split(';')[0]; csrf = login.body.csrf_token;
try {
  for (const missing of [false, true]) {
    restart('local-default');
    const event = (await json('POST', '/api/admin/events', { name: `Backend smoke ${randomUUID()}`, enabled: true }, 201, true)).body.event;
    const sid = await session(event), path = assetBase(event, sid);
    const local = await fetch(base + path, { method: 'POST', headers: { Origin: base, 'Content-Type': 'image/png', 'Content-Disposition': 'attachment; filename=local.png' }, body: png });
    assert.equal(local.status, 201); const l = (await local.json()).asset;
    const localPath = `/data/uploads/e${event.id}_${l.id}`;
    compose(['exec', '-T', 'photodrop', 'test', '-s', localPath]);
    restart('s3-a');
    const a = await prepare(event, sid); assert.equal(new URL(a.upload.url).port, '18090'); await put(a);
    restart('s3-b');
    const refreshed = (await json('POST', path + `/${a.asset.id}/authorize`, {})).body;
    assert.equal(new URL(refreshed.upload.url).host, new URL(a.upload.url).host);
    assert.equal((await complete(event, sid, a)).status, 'ready');
    assert.equal((await complete(event, sid, a)).id, a.asset.id);
    const b = await prepare(event, sid); assert.equal(new URL(b.upload.url).port, '18091'); await put(b); await complete(event, sid, b);
    restart('local-default', missing ? 's3-b' : 's3-a,s3-b');
    assert.deepEqual((await json('GET', `/api/admin/events/${event.id}`, undefined, 200, true)).body.event.media, { photo_count: 3, storage_bytes: png.length * 3 });
    if (missing) {
      await json('DELETE', `/api/admin/events/${event.id}`, undefined, 500, true);
      assert.equal((await json('GET', '/healthz')).body.status, 'ok');
      const attempts = (await observations(18090)).filter(r => r.Path === new URL(a.upload.url).pathname);
      assert.ok(!attempts.some(r => r.Method === 'DELETE'), 'missing credentials silently deleted historical object');
      restart('local-default');
    }
    await json('DELETE', `/api/admin/events/${event.id}`, undefined, 204, true);
    compose(['exec', '-T', 'photodrop', 'test', '!', '-e', localPath]);
    for (const [port, asset] of [[18090, a], [18091, b]]) {
      const requests = (await observations(port)).filter(r => r.Path === new URL(asset.upload.url).pathname);
      assert.ok(requests.some(r => r.Method === 'HEAD'));
      assert.ok(requests.some(r => r.Method === 'GET' && r.Range === 'bytes=0-511'));
      assert.equal(requests.at(-1).Method, 'DELETE');
    }
    await json('GET', `/api/admin/events/${event.id}`, undefined, 404, true);
  }
  console.log('Backend Compose checks passed: local → S3-A → S3-B → local, historical refresh/finalization, mixed deletion, missing credentials and retry.');
} finally {
  restart('local-default'); // Leave the isolated app available for browser checks.
}
