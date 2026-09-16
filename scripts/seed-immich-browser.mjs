// Add disposable photos to the fixture left by smoke-immich.mjs --browser.
// Uses the real guest upload boundary; this never adds test routes to PhotoDrop.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { photo } from './photo-fixture.mjs';
const fixture = JSON.parse(readFileSync('.tmp/gate6-browser.json', 'utf8'));
const base = 'http://localhost:8083';
assert.ok(fixture.adminURL.startsWith(base + '/admin/events/'));
const count = Number(process.argv[2] ?? '1'); assert.ok(Number.isInteger(count) && count > 0 && count <= 30);
const session = await fetch(`${base}/api/public/events/${fixture.publicID}/upload-sessions`, { method: 'POST', headers: { Origin: base, 'Content-Type': 'application/json' }, body: '{}' });
assert.equal(session.status, 201); const sid = (await session.json()).upload_session.id;
for (let i = 0; i < count; i++) {
  const response = await fetch(`${base}/api/public/events/${fixture.publicID}/upload-sessions/${sid}/assets`, {
    method: 'POST', headers: { Origin: base, 'Content-Type': 'image/png', 'Content-Disposition': `attachment; filename="browser-${randomUUID()}.png"` }, body: photo(),
  });
  assert.equal(response.status, 201, 'fixture requires local upload mode');
}
console.log(`Added ${count} ready photos to browser fixture event ${fixture.eventID}.`);
