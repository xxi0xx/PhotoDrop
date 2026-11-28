// Real Immich (v3.4.2) OAuth behaviour that PhotoDrop's account linking relies
// on, plus the link-flow cases. Runs against the disposable Immich test stack
// (scripts/compose-immich-test.yml) and its test identity provider; never an
// owner's identity provider or Immich.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { photo } from './photo-fixture.mjs';

const immich = 'http://localhost:22830/api';
const oidc = 'http://localhost:18997';
const callback = 'http://localhost:8083/api/admin/destinations/immich/callback';
const admin = { email: 'photodrop-test@example.test', password: 'temporary-immich-test-account-password' };
const composeArgs = ['compose', '-f', 'scripts/compose-immich-test.yml'];
const compose = args => execFileSync('docker', [...composeArgs, ...args], { stdio: 'inherit' });
const b64url = bytes => Buffer.from(bytes).toString('base64url');

async function immichCall(method, path, { body, token, expected } = {}) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await fetch(immich + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await response.text();
  if (expected !== undefined) assert.equal(response.status, expected, `Immich ${method} ${path} returned ${response.status}`);
  return { status: response.status, body: text ? JSON.parse(text) : null };
}
async function identity(faults) {
  const r = await fetch(`${oidc}/__test/oidc`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(faults) });
  assert.ok(r.ok, 'test identity provider control failed');
  return r.json();
}
async function exchanges() { return (await (await fetch(`${oidc}/__test/oidc`)).json()).history ?? []; }

// One Immich OAuth round trip as PhotoDrop performs it: PhotoDrop chooses state
// and the PKCE challenge, the browser visits the identity provider, and PhotoDrop
// submits the callback URL it received.
async function immichOAuth({ submitURL } = {}) {
  const state = b64url(randomBytes(32)), verifier = b64url(randomBytes(32));
  const challenge = b64url(createHash('sha256').update(verifier).digest());
  const { body: authorize } = await immichCall('POST', '/oauth/authorize', { body: { redirectUri: callback, state, codeChallenge: challenge }, expected: 201 });
  const location = new URL(authorize.url);
  assert.equal(location.origin, oidc, 'authorize URL is not the identity provider');
  assert.equal(location.searchParams.get('state'), state, 'Immich did not use our state');
  assert.equal(location.searchParams.get('redirect_uri'), callback, 'Immich changed redirect_uri');
  assert.equal(location.searchParams.get('code_challenge'), challenge, 'Immich did not use our PKCE challenge');
  assert.equal(location.searchParams.get('code_challenge_method'), 'S256');
  const browser = await fetch(location, { redirect: 'manual' });
  assert.equal(browser.status, 303, 'identity provider did not redirect');
  const returned = new URL(browser.headers.get('location'));
  assert.equal(returned.origin + returned.pathname, callback, 'identity provider redirected elsewhere');
  const url = submitURL ? submitURL(returned) : `${callback}?${returned.search.slice(1)}`;
  return immichCall('POST', '/oauth/callback', { body: { url, state, codeVerifier: verifier } });
}

compose(['up', '-d', '--wait', '--wait-timeout', '300', 'immich-server', 'objects']);
// The disposable Immich admin from smoke-immich, created here when missing.
let login = await immichCall('POST', '/auth/login', { body: admin });
if (login.status !== 201) {
  await immichCall('POST', '/auth/admin-sign-up', { body: { ...admin, name: 'PhotoDrop integration tests' }, expected: 201 });
  login = await immichCall('POST', '/auth/login', { body: admin, expected: 201 });
}
const adminToken = login.body.accessToken;
// Auto Register is off, as in production, so the host's Immich user must exist.
const hostEmail = `host-${randomUUID()}@example.test`, hostSubject = `host-${randomUUID()}`;
const created = await immichCall('POST', '/admin/users', { token: adminToken, body: { email: hostEmail, password: `temporary-${randomUUID()}`, name: 'Link test host' }, expected: 201 });
await identity({ Subject: hostSubject, Email: hostEmail, EmailVerified: true, Name: 'Link test host', Groups: ['photodrop-hosts'] });

// VERIFY 2 (positive): the code exchange succeeds only with the exact callback URL.
const before = (await exchanges()).length;
const session = await immichOAuth();
assert.equal(session.status, 201, `Immich OAuth callback failed: ${JSON.stringify(session.body)}`);
assert.equal(session.body.userId, created.body.id, 'Immich signed in a different user');
assert.ok(session.body.accessToken, 'no Immich session token');
const exchange = (await exchanges()).slice(before).find(e => e.client_id === 'immich-test');
assert.ok(exchange?.accepted, 'identity provider saw no accepted Immich exchange');
assert.equal(exchange.redirect_uri, callback, 'Immich derived a different redirect_uri');
assert.equal(exchange.auth_method, 'client_secret_post');
console.log('VERIFY 2 (positive): Immich exchanged the code with redirect_uri equal to the submitted callback URL.');

// VERIFY 1: the session token works as Authorization: Bearer, server to server.
const me = await immichCall('GET', '/users/me', { token: session.body.accessToken, expected: 200 });
assert.equal(me.body.id, session.body.userId);
assert.equal(me.body.oauthId, hostSubject, 'Immich did not record the identity subject');
assert.equal(me.body.email, hostEmail);
console.log('VERIFY 1: an Immich OAuth session authenticates server-to-server as Authorization: Bearer.');

// The session can mint and delete a scoped key, then logs out for good.
const permissions = ['album.create', 'album.read', 'asset.upload', 'albumAsset.create'];
const minted = await immichCall('POST', '/api-keys', { token: session.body.accessToken, body: { name: 'PhotoDrop VERIFY probe', permissions }, expected: 201 });
assert.deepEqual([...minted.body.apiKey.permissions].sort(), [...permissions].sort());
const self = await fetch(`${immich}/api-keys/me`, { headers: { 'x-api-key': minted.body.secret } });
assert.equal(self.status, 200); assert.equal((await self.json()).id, minted.body.apiKey.id);
await immichCall('DELETE', `/api-keys/${minted.body.apiKey.id}`, { token: session.body.accessToken, expected: 204 });
await immichCall('POST', '/auth/logout', { token: session.body.accessToken, expected: 200 });
await immichCall('GET', '/users/me', { token: session.body.accessToken, expected: 401 });
console.log('Session minted and deleted a 4-permission key, logged out, and was rejected afterwards.');

// VERIFY 2 (negative): submitting any other URL changes the redirect_uri Immich
// sends, so the identity provider refuses the code.
const mark = (await exchanges()).length;
const wrong = await immichOAuth({ submitURL: returned => `http://localhost:8083/api/admin/destinations/immich/other?${returned.search.slice(1)}` });
assert.ok(wrong.status >= 400, 'Immich accepted a mismatched callback URL');
const rejected = (await exchanges()).slice(mark).find(e => e.client_id === 'immich-test');
assert.ok(rejected && !rejected.accepted, 'no rejected exchange recorded');
assert.equal(rejected.redirect_uri, 'http://localhost:8083/api/admin/destinations/immich/other');
console.log('VERIFY 2 (negative): a different submitted URL became the redirect_uri, and the exchange was refused.');

await immichCall('DELETE', `/admin/users/${created.body.id}`, { token: adminToken, body: { force: true }, expected: 200 });
console.log('Immich OAuth VERIFY checks passed.');

// ---------------------------------------------------------------------------
// Link-flow cases 2-7 through a real PhotoDrop container with a secrets key.
// The test runner plays each host's browser; identities come from the fixture.
// ---------------------------------------------------------------------------
const base = 'http://localhost:8083', target = 'integration-test';
const secretsKey = randomBytes(32).toString('base64');
execFileSync('docker', [...composeArgs, 'up', '-d', '--no-deps', '--force-recreate', '--wait', '--wait-timeout', '120', 'photodrop'], {
  stdio: 'inherit', env: { ...process.env, PHOTODROP_TEST_SECRETS_KEY: secretsKey, PHOTODROP_TEST_IMMICH_KEY: '' },
});

class Browser {
  jar = new Map(); csrf = '';
  async go(url, { method = 'GET', body, expected } = {}) {
    const local = url.startsWith('/') || url.startsWith(base);
    const destination = url.startsWith('/') ? base + url : url;
    const headers = {};
    if (local) {
      headers.Cookie = [...this.jar].map(([k, v]) => `${k}=${v}`).join('; ');
      if (method !== 'GET') Object.assign(headers, { Origin: base, 'Content-Type': 'application/json', 'X-CSRF-Token': this.csrf });
    }
    const response = await fetch(destination, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'manual' });
    if (local) for (const line of response.headers.getSetCookie()) {
      const [pair] = line.split(';'); const [name, value] = pair.split('=');
      if (/Max-Age=0|Max-Age=-1|Expires=Thu, 01 Jan 1970/i.test(line) || value === '') this.jar.delete(name); else this.jar.set(name, value);
    }
    if (expected !== undefined) assert.equal(response.status, expected, `${method} ${url} returned ${response.status}`);
    return response;
  }
  async signIn(who) {
    await identity(who);
    const begin = await this.go('/api/admin/oidc/login', { expected: 303 });
    const authorized = await this.go(begin.headers.get('location'), { expected: 303 });
    const done = await this.go(authorized.headers.get('location'), { expected: 303 });
    assert.equal(done.headers.get('location'), '/admin', 'PhotoDrop sign-in failed');
    const session = await (await this.go('/api/admin/session', { expected: 200 })).json();
    this.csrf = session.csrf_token;
    return session.host;
  }
  // Starts a link (or unlink) and follows the identity provider back to PhotoDrop.
  async link(path = '/api/admin/destinations/immich/link', body = { target }, atImmich) {
    const started = await this.go(path, { method: 'POST', body, expected: 200 });
    const { location } = await started.json();
    if (atImmich) await identity(atImmich);
    const authorized = await this.go(location, { expected: 303 });
    const done = await this.go(authorized.headers.get('location'), { expected: 303 });
    const result = new URL(done.headers.get('location'), base);
    assert.equal(result.pathname, '/admin/account');
    return result.searchParams.get('result');
  }
  async accounts() { return (await (await this.go('/api/admin/destinations', { expected: 200 })).json()).accounts; }
}

// Inspect one Immich user's state with a session of their own.
async function asImmichUser(who, inspect) {
  await identity(who);
  const s = await immichOAuth();
  assert.equal(s.status, 201, 'inspection sign-in failed');
  try { return await inspect(s.body.accessToken); }
  finally { await immichCall('POST', '/auth/logout', { token: s.body.accessToken }); }
}
const photoDropKeys = async token => (await immichCall('GET', '/api-keys', { token, expected: 200 })).body.filter(k => k.name.startsWith('PhotoDrop · localhost:8083'));
const person = (name, extra = {}) => ({ Subject: `${name.replace(' ', '-')}-${randomUUID()}`, Email: `${name.replace(' ', '-').toLowerCase()}-${randomUUID()}@example.test`, EmailVerified: true, Name: name, Groups: ['photodrop-hosts'], ...extra });
async function immichUser(who) {
  return (await immichCall('POST', '/admin/users', { token: adminToken, body: { email: who.Email, password: `temporary-${randomUUID()}`, name: who.Name }, expected: 201 })).body;
}
const cleanup = [];

// Case 2: a host links; their Immich user has exactly one new, named, 4-permission key.
const hostA = person('Host A'); const userA = await immichUser(hostA); cleanup.push(userA.id);
const a = new Browser(); await a.signIn(hostA);
assert.equal(await a.link(), 'linked');
let firstKey;
await asImmichUser(hostA, async token => {
  const keys = await photoDropKeys(token);
  assert.equal(keys.length, 1, 'expected exactly one PhotoDrop key');
  assert.equal(keys[0].name, 'PhotoDrop · localhost:8083 · Host A');
  assert.deepEqual([...keys[0].permissions].sort(), ['album.create', 'album.read', 'albumAsset.create', 'asset.upload']);
  firstKey = keys[0].id;
  // Case 4: the Immich session PhotoDrop used for linking is gone; only ours remains.
  const sessions = (await immichCall('GET', '/sessions', { token, expected: 200 })).body;
  assert.equal(sessions.length, 1, 'the link session was not logged out');
  assert.equal(sessions[0].current, true);
});
const accountsA = await a.accounts();
assert.equal(accountsA.length, 1); assert.equal(accountsA[0].source, 'linked'); assert.equal(accountsA[0].remote_email, hostA.Email);
console.log('Case 2: link created exactly one named 4-permission key. Case 4: the link session was logged out.');

// Case 3: the host's event provisions an album and imports media into the host's own library.
const createdEvent = await a.go('/api/admin/events', { method: 'POST', body: { name: `Linked ${randomUUID()}`, enabled: true, immich: { target, album_name: 'Host A album', auto_import: true } }, expected: 201 });
const event = (await createdEvent.json()).event;
const grant = (await (await a.go(`/api/public/events/${event.public_id}/upload-sessions`, { method: 'POST', body: { contributor_name: 'Linked guest' }, expected: 201 })).json()).upload_session;
const uploaded = await fetch(`${base}/api/public/events/${event.public_id}/upload-sessions/${grant.id}/assets`, { method: 'POST', headers: { Origin: base, 'Content-Type': 'image/png', 'Content-Disposition': 'attachment; filename=linked.png' }, body: photo() });
assert.equal(uploaded.status, 201);
let status;
for (let i = 0; i < 120; i++) {
  status = await (await a.go(`/api/admin/events/${event.id}/immich`, { expected: 200 })).json();
  if (status.album_id && status.imported >= 1) break;
  await new Promise(r => setTimeout(r, 1000));
}
assert.ok(status.album_id && status.imported >= 1, `album/import did not finish: ${JSON.stringify(status)}`);
assert.equal(status.target.account.source, 'linked');
await asImmichUser(hostA, async token => {
  const album = (await immichCall('GET', `/albums/${status.album_id}`, { token, expected: 200 })).body;
  // Immich 3.4.2 lists the owner in albumUsers with role 'owner' (no ownerId field).
  const owner = album.albumUsers.find(u => u.role === 'owner');
  assert.equal(owner?.user?.id, userA.id, "album is not in the host's library");
  assert.ok(album.assetCount >= 1);
});
const adminAlbums = (await immichCall('GET', '/albums', { token: adminToken, expected: 200 })).body;
assert.ok(!adminAlbums.some(al => al.id === status.album_id), 'album landed in the admin library');
console.log("Case 3: the host's album and imported media belong to the host's Immich user.");

// Case 6: relink leaves exactly one PhotoDrop key; unlink leaves none.
assert.equal(await a.link(), 'linked');
await asImmichUser(hostA, async token => {
  const keys = await photoDropKeys(token);
  assert.equal(keys.length, 1, 'relink left more than one key'); assert.notEqual(keys[0].id, firstKey, 'relink did not replace the key');
});
const linkedID = (await a.accounts())[0].id;
assert.equal(await a.link(`/api/admin/destinations/${linkedID}/unlink`, {}), 'unlinked');
await asImmichUser(hostA, async token => assert.equal((await photoDropKeys(token)).length, 0, 'unlink left a key'));
assert.equal((await a.accounts()).length, 0);
console.log('Case 6: relink left exactly one key and unlink left none.');

// Case 5: signed in to the identity provider as someone else at the Immich step.
const hostB = person('Host B'); const userB = await immichUser(hostB); cleanup.push(userB.id);
const intruder = person('Intruder'); const userC = await immichUser(intruder); cleanup.push(userC.id);
const b = new Browser(); await b.signIn(hostB);
assert.equal(await b.link(undefined, undefined, intruder), 'link_wrong_user');
await asImmichUser(intruder, async token => assert.equal((await photoDropKeys(token)).length, 0, 'a key was created for the wrong person'));
assert.equal((await b.accounts()).length, 0);
console.log('Case 5: linking as a different identity was refused and created no key.');

// Case 7: Auto Register is off. No Immich account means a clean failure.
const hostD = person('Host D'); const d = new Browser(); await d.signIn(hostD);
assert.equal(await d.link(), 'link_failed');
const users = (await immichCall('GET', '/admin/users', { token: adminToken, expected: 200 })).body;
assert.ok(!users.some(u => u.email === hostD.Email), 'linking registered an Immich user');
assert.equal((await d.accounts()).length, 0);
// Subject modes differ: the Immich user carries another subject for the same email.
const hostE = person('Host E'); const userE = await immichUser(hostE); cleanup.push(userE.id);
const immichSide = { ...hostE, Subject: `immich-mode-${randomUUID()}` };
await asImmichUser(immichSide, async () => {}); // Immich records oauthId = the other subject.
const e = new Browser(); await e.signIn(hostE);
assert.equal(await e.link(undefined, undefined, immichSide), 'link_subject_mismatch');
await asImmichUser(immichSide, async token => assert.equal((await photoDropKeys(token)).length, 0));
console.log('Case 7: no Immich user fails cleanly without registering one; a subject-mode mismatch is reported.');

const logs = execFileSync('docker', [...composeArgs, 'logs', '--no-color', 'photodrop'], { encoding: 'utf8' });
for (const secret of [secretsKey, 'immich-session', 'disposable-immich-client-secret', 'accessToken']) assert.ok(!logs.includes(secret), 'secret material in PhotoDrop logs');
for (const id of cleanup) await immichCall('DELETE', `/admin/users/${id}`, { token: adminToken, body: { force: true } });
console.log('Real Immich account-link suite passed.');
