// Real Immich + historical storage/export lifecycle. All accounts and media
// belong to the isolated test Compose project; never reads the production .env.
import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { photo } from './photo-fixture.mjs';
import { video } from './video-fixture.mjs';

const runAsync = promisify(execFile);
const base = 'http://localhost:8083', immich = 'http://localhost:22830/api';
const composeArgs = ['compose', '-f', 'scripts/compose-immich-test.yml'];
const account = { email: 'photodrop-test@example.test', password: 'temporary-immich-test-account-password' };
let apiKey = '', accessToken = '', cookie = '', csrf = '', active = 'local-default';
function compose(args, capture = false, key = apiKey, run = execFileSync) {
  return run('docker', [...composeArgs, ...args], {
    stdio: capture ? ['ignore', 'pipe', 'pipe'] : 'inherit', encoding: capture ? 'utf8' : undefined,
    env: { ...process.env, PHOTODROP_TEST_PROVIDER: active === 'local-default' ? 'local' : 's3', PHOTODROP_TEST_BACKEND_KEY: active, PHOTODROP_TEST_IMMICH_KEY: key },
  });
}
function restart(backend = active, key = apiKey) {
  // Let fetch retire closed sockets while Docker recreates the test service.
  active = backend; return compose(['up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', 'photodrop'], false, key, runAsync);
}
async function remote(method, path, data, expected = 200, useSession = false) {
  const headers = { 'Content-Type': 'application/json' };
  if (useSession) headers.Authorization = `Bearer ${accessToken}`; else if (apiKey) headers['x-api-key'] = apiKey;
  const response = await fetch(immich + path, { method, headers, body: data === undefined ? undefined : JSON.stringify(data) });
  assert.equal(response.status, expected, `Immich ${method} ${path}: unexpected status`);
  return expected === 204 ? null : response.json();
}
async function json(method, path, data, expected = 200, admin = true) {
  const headers = { Origin: base, 'Content-Type': 'application/json' };
  if (admin) { headers.Cookie = cookie; headers['X-CSRF-Token'] = csrf; }
  const response = await fetch(base + path, { method, headers, body: data === undefined ? undefined : JSON.stringify(data), redirect: 'manual' });
  const body = response.status === 204 ? null : await response.json();
  assert.equal(response.status, expected, `PhotoDrop ${method} ${path}: ${body?.error?.message ?? 'unexpected status'}`);
  assert.ok(!JSON.stringify(body).includes(apiKey), 'API key leaked into browser response');
  return { response, body };
}
async function login() {
  const result = await json('POST', '/api/admin/login', { password: 'temporary-immich-test-admin-password' }, 200, false);
  cookie = result.response.headers.getSetCookie()[0].split(';')[0]; csrf = result.body.csrf_token;
}
async function createEvent(label) { return (await json('POST', '/api/admin/events', { name: `${label} ${randomUUID()}`, enabled: true }, 201)).body.event; }
async function upload(event, bytes = photo(), name = 'photo.png', kind = 'image/png') {
  const session = (await json('POST', `/api/public/events/${event.public_id}/upload-sessions`, {}, 201, false)).body.upload_session.id;
  const path = `/api/public/events/${event.public_id}/upload-sessions/${session}/assets`;
  if (active === 'local-default') {
    const response = await fetch(base + path, { method: 'POST', headers: { Origin: base, 'Content-Type': kind, 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(name)}` }, body: bytes });
    assert.equal(response.status, 201, 'local upload failed'); return { ...(await response.json()).asset, bytes };
  }
  const prepared = (await json('POST', path + '/prepare', { filename: name, content_type: kind, size: bytes.length, request_id: randomBytes(16).toString('hex') }, 201, false)).body;
  const response = await fetch(prepared.upload.url, { method: prepared.upload.method, headers: prepared.upload.headers, body: bytes }); assert.equal(response.status, 200);
  const asset = (await json('POST', path + `/${prepared.asset.id}/complete`, {}, 200, false)).body.asset;
  return { ...asset, bytes, objectPath: new URL(prepared.upload.url).pathname, port: new URL(prepared.upload.url).port };
}
const pathFor = event => `/api/admin/events/${event.id}/immich`;
async function status(event) { return (await json('GET', pathFor(event))).body; }
async function control(data) {
  const response = await fetch('http://localhost:2284/__test/control', { method: data ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json' }, body: data ? JSON.stringify(data) : undefined });
  assert.equal(response.status, 200); return response.json();
}
async function waitFor(event, predicate, timeout = 90000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const s = await status(event); if (predicate(s)) return s; await new Promise(resolve => setTimeout(resolve, 200)); }
  throw new Error('Timed out waiting for persistent import progress');
}
async function send(event, mode = 'new') {
  const start = Date.now(); await json('POST', pathFor(event) + '/jobs', { mode, album_name: 'PhotoDrop integration validation' }, 202);
  assert.ok(Date.now() - start < 2000, 'import blocked the HTTP request');
  return waitFor(event, s => !['queued', 'running'].includes(s.job.status));
}
const sha = bytes => createHash('sha256').update(bytes).digest('hex');

compose(['up', '-d', '--wait', '--wait-timeout', '300', 'immich-server', 'objects']);
// A rerun reuses only the disposable account, never real user credentials.
const probe = await fetch(immich + '/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(account) });
if (probe.ok) accessToken = (await probe.json()).accessToken;
else {
  await remote('POST', '/auth/admin-sign-up', { ...account, name: 'PhotoDrop integration tests' }, 201);
  accessToken = (await remote('POST', '/auth/login', account, 201)).accessToken;
}
const key = await remote('POST', '/api-keys', { name: `PhotoDrop test ${randomUUID()}`, permissions: ['album.create', 'album.read', 'asset.upload', 'albumAsset.create'] }, 201, true);
apiKey = key.secret; assert.ok(apiKey, 'test API key missing');
await restart('local-default'); await login(); await control({});
// Provisioning selects no assets and uses the same durable marker recovery as
// manual imports. Fault injection is confined to the disposable proxy.
const catalog = (await json('GET', '/api/admin/immich/targets')).body;
assert.equal(catalog.active_target, 'integration-test');
assert.ok(!JSON.stringify(catalog).includes('localhost:2284'), 'internal API origin leaked');
async function provisionedEvent(label, autoImport = false) {
  const start = Date.now();
  const event = (await json('POST', '/api/admin/events', {
    name: `${label} ${randomUUID()}`, enabled: true,
    immich: {target: 'integration-test', album_name: label, auto_import: autoImport},
  }, 201)).body.event;
  assert.ok(Date.now() - start < 2000, 'creation waited on Immich');
  return event;
}
async function retryAlbum(event) {
  await json('POST', pathFor(event) + '/album', {target:'integration-test'}, 202);
  return waitFor(event, s => s.job.status === 'completed');
}
await control({fail_validate:true});
const early = await provisionedEvent('Early album');
await waitFor(early, s => s.job.status === 'failed');
await upload(early);
const earlyFailedJob = (await status(early)).job.id;
await control({});
await new Promise(resolve => setTimeout(resolve, 1200));
assert.equal((await status(early)).job.id, earlyFailedJob);
assert.equal((await status(early)).job.status, 'failed', 'failed setup auto-retried');
const beforeEarly = await control();
const earlyReady = await retryAlbum(early);
assert.equal(earlyReady.album_name, 'Early album');
assert.equal(earlyReady.album_url, `http://localhost:22830/albums/${earlyReady.album_id}`);
assert.equal(earlyReady.new,1); assert.equal(earlyReady.imported,0);
assert.equal((await control()).uploads,beforeEarly.uploads);
assert.equal((await remote('GET', `/albums/${earlyReady.album_id}`)).assetCount,0);
await remote('PATCH', `/albums/${earlyReady.album_id}`, {albumName:'Renamed early album'},200,true);
assert.equal((await send(early)).album_id,earlyReady.album_id);
await upload(early);
assert.equal((await status(early)).new,1);
assert.equal((await remote('GET', `/albums/${earlyReady.album_id}`)).assetCount,1);
const existingEmpty = await createEvent('Existing empty');
assert.equal((await retryAlbum(existingEmpty)).total,0);
const beforeLostAlbum = await control();
await control({lose_album:true});
const lostAlbum = await provisionedEvent('Lost album response');
assert.equal((await waitFor(lostAlbum,s=>s.job.status==='failed')).album_state,'creating');
await retryAlbum(lostAlbum);
assert.equal((await control()).album_creates-beforeLostAlbum.album_creates,1);
const beforeRestartAlbum = await control();
await control({hold_albums:true});
const restartAlbum = await provisionedEvent('Interrupted album creation');
const deadlineAlbum=Date.now()+30000;
while ((await control()).album_creates===beforeRestartAlbum.album_creates) {
  assert.ok(Date.now()<deadlineAlbum,'remote album was not created');
  await new Promise(resolve=>setTimeout(resolve,100));
}
compose(['kill','-s','SIGKILL','photodrop']);
await control({}); await restart();
assert.equal((await waitFor(restartAlbum,s=>s.job.status==='completed')).total,0);
assert.equal((await control()).album_creates-beforeRestartAlbum.album_creates,1);
console.log('Early/empty provisioning, outage isolation, manual retry/import, rename, lost album response and restart without duplicate albums passed.');

// Durable automatic import. Gates and observed job states synchronize races;
// upload notifications are deliberately unnecessary for periodic discovery.
async function automatic(event, enabled) {
  await json('PUT',pathFor(event)+'/auto-import',{target:'integration-test',auto_import:enabled},204);
}
async function waitControl(predicate) {
  const deadline=Date.now()+30000;
  for (;;) {const value=await control();if(predicate(value))return value;assert.ok(Date.now()<deadline,'test gate was not reached');await new Promise(resolve=>setTimeout(resolve,100));}
}
await restart('s3-a');
const beforeAutomatic=await control();
await control({hold_albums:true});
const automaticEvent=await provisionedEvent('Automatic mixed media',true);
await waitControl(value=>value.album_creates===beforeAutomatic.album_creates+1);
const autoSources=[
  await upload(automaticEvent,photo(),'auto.png'),
  await upload(automaticEvent,video('mp4',true),'auto.mp4','video/mp4'),
  await upload(automaticEvent,video('mov',true),'auto.mov','video/quicktime'),
];
const provisioning=await status(automaticEvent);
assert.equal(provisioning.auto_import,true);assert.equal(provisioning.pending,0);assert.equal(provisioning.new,3);
assert.equal((await control()).uploads,beforeAutomatic.uploads,'provisioning sent media');
await control({hold_uploads:true});
let autoState=await waitFor(automaticEvent,s=>s.job.status==='running'&&s.pending===3);
const autoAlbum=autoState.album_id, firstAutoJob=autoState.job.id;
assert.equal(firstAutoJob,provisioning.job.id+1);
await waitControl(value=>value.uploads>beforeAutomatic.uploads);
for(let i=0;i<10;i++)await upload(automaticEvent,photo(),`burst-${i}.png`);
autoState=await status(automaticEvent);
assert.equal(autoState.job.id,firstAutoJob);assert.equal(autoState.pending,3);assert.equal(autoState.new,10);
await automatic(automaticEvent,false);await control({});
autoState=await waitFor(automaticEvent,s=>s.job.status==='completed');
assert.equal(autoState.imported,3);assert.equal(autoState.new,10);assert.equal(autoState.auto_import,false);
await control({hold_uploads:true});await automatic(automaticEvent,true);
autoState=await waitFor(automaticEvent,s=>s.job.status==='running'&&s.pending===10);
assert.equal(autoState.job.id,firstAutoJob+1);
await upload(automaticEvent);await upload(automaticEvent);
const held=await status(automaticEvent);assert.equal(held.new,2);assert.equal(held.job.id,autoState.job.id);
await control({});
autoState=await waitFor(automaticEvent,s=>s.imported===15&&s.job.status==='completed');
assert.equal(autoState.job.id,firstAutoJob+2,'burst created more than one follow-up job');
assert.equal((await remote('GET',`/albums/${autoAlbum}`)).assetCount,15);
const autoRemote=(await remote('POST','/search/metadata',{albumIds:[autoAlbum]},200,true)).assets.items;
assert.equal(autoRemote.filter(a=>a.type==='VIDEO').length,2);
for(const source of autoSources) {
 const match=autoRemote.find(a=>a.originalFileName===source.filename);assert.ok(match);
 const result=await fetch(immich+`/assets/${match.id}/original`,{headers:{Authorization:`Bearer ${accessToken}`}});
 assert.equal(result.status,200);assert.equal(sha(Buffer.from(await result.arrayBuffer())),sha(source.bytes));
}
console.log('Real automatic S3 PNG/MP4/MOV import, hashes, provisioning isolation, disable/enable backlog and 10+2 burst coalescing passed.');

await restart('local-default');await control({hold_uploads:true});
await upload(automaticEvent);
autoState=await waitFor(automaticEvent,s=>s.job.status==='running'&&s.pending===1);
await upload(automaticEvent);await upload(automaticEvent);
assert.equal((await status(automaticEvent)).new,2);
const interruptedAutoJob=autoState.job.id;
compose(['kill','-s','SIGKILL','photodrop']);await control({});await restart();
autoState=await waitFor(automaticEvent,s=>s.imported===18&&s.job.status==='completed');
assert.equal(autoState.job.id,interruptedAutoJob+1,'restart stranded or split the untracked backlog');

await automatic(automaticEvent,false);
await upload(automaticEvent);await upload(automaticEvent);
await control({fail_next:true});await automatic(automaticEvent,true);
autoState=await waitFor(automaticEvent,s=>s.job.status==='failed');
assert.equal(autoState.failed,1);assert.equal(autoState.imported,19);
const failureJob=autoState.job.id;
await upload(automaticEvent);
autoState=await waitFor(automaticEvent,s=>s.job.status==='completed'&&s.imported===20);
assert.equal(autoState.job.id,failureJob+1);assert.equal(autoState.failed,1,'new work silently retried a failure');
autoState=await send(automaticEvent,'retry');assert.equal(autoState.imported,21);assert.equal(autoState.failed,0);
await control({fail_validate:true});
const guestStarted=Date.now();await upload(automaticEvent);
assert.ok(Date.now()-guestStarted<2000,'guest completion waited for Immich');await json('GET','/healthz');
// Status reads progress and the latest job separately; a completion between
// those reads can briefly pair old counts with a terminal job. Wait for both
// invariants rather than accepting the first terminal-job snapshot.
autoState=await waitFor(automaticEvent,s=>s.job.status==='failed'&&s.failed===1&&s.pending===0);
assert.equal(autoState.failed,1);assert.equal(autoState.pending,0);
await control({});autoState=await send(automaticEvent,'retry');assert.equal(autoState.imported,22);
await json('DELETE',`/api/admin/events/${automaticEvent.id}`,undefined,204);
assert.equal((await remote('GET',`/albums/${autoAlbum}`)).assetCount,22);
console.log('Real automatic local uploads, restart reconciliation, partial failure with manual-only retry, outage isolation and independent deletion passed.');

const event = await createEvent('Gate 6 lifecycle');
const local = await upload(event);
await restart('s3-a'); const a = await upload(event, video('mp4', true), 'clip.mp4', 'video/mp4');
await restart('s3-b'); const b = await upload(event, video('mov', true), 'clip.mov', 'video/quicktime');
const assets = [local, a, b];
const exportPath = `/export/event-${event.id}-${randomUUID()}`;
// Test-only volume ownership; production optional bind mounts stay operator-owned.
compose(['exec', '-T', '-u', '0', 'photodrop', 'chown', '10001:10001', '/export']);
compose(['exec', '-T', '-u', '10001', 'photodrop', 'photodrop', 'export', '--event', String(event.id), '--output', exportPath]);
const manifest = JSON.parse(compose(['exec', '-T', 'photodrop', 'cat', exportPath + '/photodrop-manifest.json'], true));
assert.equal(manifest.formatVersion, 1); assert.equal(manifest.assets.length, 3); assert.equal(manifest.event.id, event.id);
assert.equal(new Set(manifest.assets.map(a => a.exportFilename.toLowerCase())).size, 3);
for (const item of manifest.assets) {
  const source = assets.find(a => a.id === item.photoDropAssetId); assert.ok(source);
  assert.equal(item.originalFilename, source.filename); assert.equal(item.sizeBytes, source.bytes.length); assert.equal(item.mimeType, source.mime_type);
  const digest = compose(['exec', '-T', 'photodrop', 'sha256sum', `${exportPath}/photos/${item.exportFilename}`], true).split(' ')[0];
  assert.equal(digest, sha(source.bytes));
}
assert.ok(!/storage_backend|storage_key|secret|endpoint|presigned/i.test(JSON.stringify(manifest)));
console.log('Mixed local + historical S3-A + S3-B export: hashes and manifest passed.');

await json('POST', pathFor(event) + '/test', {});
let current = await send(event);
assert.equal(current.job.status, 'completed'); assert.equal(current.imported, 3); assert.equal(current.duplicate, 0);
const albumID = current.album_id;
const album = await remote('GET', `/albums/${albumID}`);
assert.equal(album.assetCount, 3);
// Use the test owner's session for readback; PhotoDrop's four-permission API
// key deliberately has no search/download permission. Album info has no assets.
const importedAssets = (await remote('POST', '/search/metadata', {albumIds:[albumID]}, 200, true)).assets.items;
assert.equal(importedAssets.filter(asset => asset.type === 'VIDEO').length, 2);
for (const source of [a,b]) {
  const target = importedAssets.find(asset => asset.originalFileName === source.filename); assert.ok(target);
  const original = await fetch(immich + `/assets/${target.id}/original`, {headers:{Authorization:`Bearer ${accessToken}`}});
  assert.equal(original.status,200);assert.equal(sha(Buffer.from(await original.arrayBuffer())),sha(source.bytes));
}
console.log('Real Immich MP4/MOV import, VIDEO classification, and original-byte download hashes passed.');
const albums = await remote('GET', '/albums'); assert.equal(albums.filter(x => x.id === albumID).length, 1);
await remote('PATCH', `/albums/${albumID}`, { albumName: 'Renamed externally during validation' }, 200, true);
const beforeIncrement = await control(); await upload(event); current = await send(event);
assert.equal(current.imported, 4); assert.equal(current.album_id, albumID);
assert.equal((await control()).uploads - beforeIncrement.uploads, 1);
assert.equal((await remote('GET', `/albums/${albumID}`)).assetCount, 4);
console.log('Real auth with four permissions, album creation/assignment, external rename, and incremental import passed.');

await upload(event); await upload(event); await control({ fail_next: true });
current = await send(event); assert.equal(current.failed, 1); assert.equal(current.imported, 5);
const beforeRetry = await control(); current = await send(event, 'retry');
assert.equal(current.failed, 0); assert.equal(current.imported, 6); assert.equal((await control()).uploads - beforeRetry.uploads, 1);
await upload(event); await control({ lose_next: true }); current = await send(event); assert.equal(current.failed, 1);
const beforeLostRetry = await control(); current = await send(event, 'retry');
assert.equal(current.failed, 0); assert.equal(current.duplicate, 1); assert.equal(current.imported, 6);
assert.equal((await control()).duplicates - beforeLostRetry.duplicates, 1); assert.equal((await remote('GET', `/albums/${albumID}`)).assetCount, 7);
console.log('Real partial failure/retry and response loss after acceptance recovered through Immich deduplication.');

await restart('local-default');
for (let i = 0; i < 20; i++) await upload(event);
await control({ delay_ms: 700 });
await json('POST', pathFor(event) + '/jobs', { mode: 'new' }, 202);
current = await waitFor(event, s => s.imported + s.duplicate >= 15 && s.job.status === 'running');
const saved = current.imported + current.duplicate, jobID = current.job.id;
compose(['kill', '-s', 'SIGKILL', 'photodrop']);
await control({}); await restart();
current = await waitFor(event, s => s.job.status === 'completed');
assert.equal(current.job.id, jobID); assert.equal(current.imported + current.duplicate, 27); assert.ok(saved >= 15);
assert.equal((await remote('GET', `/albums/${albumID}`)).assetCount, 27);
console.log(`Abrupt process restart recovered job ${jobID} with ${saved} assets already accounted for.`);

// Optional integration failures never become process-health failures.
await restart(active, 'deliberately-invalid-test-key');
await json('POST', pathFor(event) + '/test', {}, 503); await json('GET', '/healthz');
await restart(active, ''); await json('POST', pathFor(event) + '/test', {}, 503); await json('GET', '/healthz');
await restart(); compose(['stop', 'immich-server']);
await json('POST', pathFor(event) + '/test', {}, 503); await json('GET', '/healthz');
compose(['up', '-d', '--wait', '--wait-timeout', '180', 'immich-server']);
await json('DELETE', `/api/admin/events/${event.id}`, undefined, 204);
compose(['exec', '-T', 'photodrop', 'test', '!', '-e', `/data/uploads/e${event.id}_${local.id}`]);
for (const source of [a, b]) {
  const requests = await (await fetch(`http://localhost:${source.port}/__test/requests`)).json();
  assert.ok(requests.some(r => r.Path === source.objectPath && r.Method === 'DELETE'));
}
assert.equal((await remote('GET', `/albums/${albumID}`)).assetCount, 27);
console.log('Health during revoked/missing credentials and outage passed; event deletion preserved all 27 real Immich copies.');

if (process.argv.includes('--browser')) {
  const browserEvent = await createEvent('Gate 6 browser'); await upload(browserEvent);
  mkdirSync('.tmp', { recursive: true });
  writeFileSync('.tmp/gate6-browser.json', JSON.stringify({ eventID: browserEvent.id, publicID: browserEvent.public_id, adminURL: `${base}/admin/events/${browserEvent.id}` }, null, 2));
  writeFileSync('.tmp/gate6-browser-new.png', photo());
  await control({ delay_ms: 3000 });
  console.log(`Browser fixture ready: ${base}/admin/events/${browserEvent.id}`);
}
console.log('Real Immich Gate 6 integration suite passed.');
