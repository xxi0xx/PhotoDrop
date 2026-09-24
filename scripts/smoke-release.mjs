// Disposable Docker upgrade/backup/restore test. Never reads .env or mounts user data.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { photo } from './photo-fixture.mjs';

const baseline = '6b7d046'; // Merged Gate 7, not a mutable tag or remote branch.
const id = `photodrop-release-${randomBytes(5).toString('hex')}`;
const dir = mkdtempSync(join(tmpdir(), id));
const oldImage = `${id}:gate7`, image = `${id}:candidate`, toolsImage = `${id}:tools`;
const objects = `${id}-objects`, app = `${id}-app`;
const volumes = ['data', 'backup', 'restored'].map(x => `${id}-${x}`);
const [data, backup, restored] = volumes;
const base = 'http://localhost:8084', password = randomBytes(24).toString('hex');
let cookie = '', csrf = '', running = false;
const docker = (args, options = {}) => execFileSync('docker', args, { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'], maxBuffer: 20 * 1024 * 1024, ...options });
const cfg = {
  PHOTODROP_LISTEN_ADDR: ':8084', PHOTODROP_BASE_URL: base,
  PHOTODROP_ADMIN_PASSWORD: password, PHOTODROP_DATA_DIR: '/data',
  PHOTODROP_S3_BACKENDS: 'release-s3', PHOTODROP_S3_RELEASE_S3_BUCKET: 'release-photos',
  PHOTODROP_S3_RELEASE_S3_ENDPOINT: 'http://localhost:18094',
  PHOTODROP_S3_RELEASE_S3_PATH_STYLE: 'true', PHOTODROP_S3_RELEASE_S3_PREFIX: 'release/',
  PHOTODROP_S3_RELEASE_S3_ACCESS_KEY_ID: 'test-s3-access',
  PHOTODROP_S3_RELEASE_S3_SECRET_ACCESS_KEY: 'test-s3-secret-for-local-tests-only',
};
const env = Object.entries(cfg).flatMap(([key, value]) => ['-e', `${key}=${value}`]);
async function call(method, path, value, expected = 200, admin = true) {
  const headers = { Origin: base, 'Content-Type': 'application/json' };
  if (admin) { headers.Cookie = cookie; headers['X-CSRF-Token'] = csrf; }
  const response = await fetch(base + path, { method, headers, body: value === undefined ? undefined : JSON.stringify(value), redirect: 'manual' });
  assert.equal(response.status, expected, `${method} ${path}: unexpected status`);
  return { response, body: expected === 204 ? null : await response.json() };
}
async function start(tag, volume, provider = 'local') {
  docker(['run', '-d', '--name', app, '--network', `container:${objects}`, '-v', `${volume}:/data`, ...env,
    '-e', `PHOTODROP_STORAGE_PROVIDER=${provider}`, '-e', `PHOTODROP_STORAGE_BACKEND_KEY=${provider === 's3' ? 'release-s3' : ''}`, tag]);
  running = true;
  for (let n = 0; n < 120; n++) {
    try { if ((await fetch(base + '/healthz')).ok) return; } catch {}
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  throw new Error('Release test app did not become healthy');
}
function stop() {
  if (running) { docker(['stop', '--time', '15', app]); docker(['rm', '-v', app]); running = false; }
}
function state(mode, volume) {
  return JSON.parse(docker(['run', '--rm', '-v', `${volume}:/state`, '--entrypoint', '/tmp/releasestate', toolsImage, mode, '/state']));
}
function copyVolume(source, destination) {
  // Only newly created UUID-prefixed test volumes can reach these mounts.
  assert.ok(volumes.includes(source) && volumes.includes(destination) && source !== destination);
  docker(['run', '--rm', '-v', `${source}:/source:ro`, '-v', `${destination}:/destination`, '--entrypoint', 'sh', image,
    '-ec', 'test -z "$(ls -A /destination)"; cp -a /source/. /destination/']);
}
async function login() {
  const result = await call('POST', '/api/admin/login', { password }, 200, false);
  cookie = result.response.headers.getSetCookie()[0].split(';')[0]; csrf = result.body.csrf_token;
}
async function session(event) {
  return (await call('POST', `/api/public/events/${event.public_id}/upload-sessions`, { contributor_name: 'Release María 王' }, 201, false)).body.upload_session.id;
}
const path = (event, sid) => `/api/public/events/${event.public_id}/upload-sessions/${sid}/assets`;
async function prepare(event, sid, bytes) {
  return (await call('POST', path(event, sid) + '/prepare', { filename: 'remote.png', content_type: 'image/png', size: bytes.length, request_id: randomBytes(16).toString('hex') }, 201, false)).body;
}
const sha = bytes => createHash('sha256').update(bytes).digest('hex');

try {
  execFileSync('git', ['diff', '--exit-code', baseline, '--', 'migrations']);
  const archive = join(dir, 'baseline.tar');
  execFileSync('git', ['archive', '--format=tar', '-o', archive, baseline]);
  console.log('Building exact Gate 7 baseline and release candidate (no publishing).');
  docker(['build', '-t', oldImage, '-'], { input: readFileSync(archive), stdio: ['pipe', 'inherit', 'inherit'] });
  const revision = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  docker(['build', '--build-arg', 'VERSION=1.0.0-rc.test', '--build-arg', `REVISION=${revision}`, '-t', image, '.'], { stdio: 'inherit' });
  docker(['build', '--target', 'backend', '-t', toolsImage, '.'], { stdio: 'inherit' });
  // Helpers are compiled only into a disposable test image, never runtime.
  docker(['run', '--name', `${id}-compile`, toolsImage, 'sh', '-ec', 'go build -o /tmp/s3test ./scripts/s3test && go build -o /tmp/releasestate ./scripts/releasestate']);
  docker(['commit', `${id}-compile`, toolsImage]); docker(['rm', `${id}-compile`]);
  docker(['run', '-d', '--name', objects, '-p', '127.0.0.1:8084:8084', '-p', '127.0.0.1:18094:18094', toolsImage, '/tmp/s3test', '-listen', ':18094', '-origin', base, '-delay', '0s']);
  for (const volume of volumes) docker(['volume', 'create', '--label', `photodrop.release-test=${id}`, volume]);
  await start(oldImage, data); await login();
  const event = (await call('POST', '/api/admin/events', { name: 'Release recovery', enabled: true, max_assets: 12, max_bytes: 1048576 }, 201)).body.event;
  assert.equal(event.id, 1);
  const localBytes = photo(), sid = await session(event);
  const localResponse = await fetch(base + path(event, sid), { method: 'POST', headers: { Origin: base, 'Content-Type': 'image/png', 'Content-Disposition': 'attachment; filename=local.png' }, body: localBytes });
  assert.equal(localResponse.status, 201); const local = (await localResponse.json()).asset;
  stop(); await start(oldImage, data, 's3');
  const remoteBytes = photo(), remoteSession = await session(event), remote = await prepare(event, remoteSession, remoteBytes);
  assert.equal((await fetch(remote.upload.url, { method: 'PUT', headers: remote.upload.headers, body: remoteBytes })).status, 200);
  await call('POST', path(event, remoteSession) + `/${remote.asset.id}/complete`, {}, 200, false);
  await prepare(event, remoteSession, photo()); // Preserve one legitimate pending reservation.
  const publicBefore = (await call('GET', `/api/public/events/${event.public_id}`, undefined, 200, false)).body;
  const detailBefore = (await call('GET', `/api/admin/events/${event.id}`)).body;
  assert.equal(detailBefore.event.media.photo_count, 2);
  stop(); const before = state('seed', data);
  assert.equal(before.assets.filter(a => a.status === 'pending').length, 1);
  assert.equal(before.schema_migrations.length, 9);
  copyVolume(data, backup);
  const backedUpSession = {cookie, csrf};
  for (const [label, volume] of [['upgrade', data], ['restore', restored]]) {
    if (label === 'restore') {
      // Actually destroy the disposable upgraded volume, then restore backup to an empty volume.
      docker(['volume', 'rm', data]); copyVolume(backup, restored);
    }
    await start(image, volume);
    ({cookie, csrf} = backedUpSession);
    // Existing cookie survives; then verify a fresh sign-in too.
    await call('GET', '/api/admin/session'); await login();
    assert.deepEqual((await call('GET', `/api/admin/events/${event.id}`)).body, detailBefore);
    assert.deepEqual((await call('GET', `/api/public/events/${event.public_id}`, undefined, 200, false)).body, publicBefore);
    assert.equal((await fetch(base + `/e/${event.public_id}`)).status, 200);
    assert.equal(docker(['exec', app, 'sha256sum', `/data/uploads/e${event.id}_${local.id}`]).split(' ')[0], sha(localBytes));
    docker(['exec', '-u', '10001', app, 'photodrop', 'export', '--event', String(event.id), '--output', `/data/export-${label}`]);
    const manifest = JSON.parse(docker(['exec', app, 'cat', `/data/export-${label}/photodrop-manifest.json`]));
    assert.equal(manifest.assets.length, 2);
    for (const entry of manifest.assets) {
      const expected = entry.photoDropAssetId === local.id ? localBytes : remoteBytes;
      assert.equal(docker(['exec', app, 'sha256sum', `/data/export-${label}/photos/${entry.exportFilename}`]).split(' ')[0], sha(expected));
    }
    stop(); assert.deepEqual(state('snapshot', volume), before);
    console.log(`${label}: health, old/new login, event/public URL, local/S3 hashes, names, quotas, backend identity, pending/ready, migrations and Immich metadata passed.`);
  }
  console.log('Stopped backup and destructive disposable restore passed; S3 objects remained external and unchanged.');
} finally {
  try { stop(); } catch {}
  for (const name of [app, objects, `${id}-compile`]) { try { docker(['rm', '-fv', name]); } catch {} }
  for (const volume of volumes) { try { docker(['volume', 'rm', volume]); } catch {} }
  for (const tag of [oldImage, image, toolsImage]) { try { docker(['image', 'rm', tag]); } catch {} }
  rmSync(dir, { recursive: true, force: true }); // mkdtemp-owned directory only.
}
