import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { compileModule } from 'svelte/compiler';
import { proxy } from 'svelte/internal/client';

test('reactive photo rows share one batch grant and frozen attribution', async () => {
  const filename = new URL('../src/lib/upload-batch.svelte.ts', import.meta.url);
  const source = stripTypeScriptTypes(readFileSync(filename, 'utf8'));
  const compiled = compileModule(source, { filename: filename.pathname, generate: 'client' }).js.code;
  const resolved = compiled.replaceAll("'svelte/internal/client'", JSON.stringify(import.meta.resolve('svelte/internal/client')));
  const { UploadBatch } = await import('data:text/javascript;base64,' + Buffer.from(resolved).toString('base64'));
  const batch = new UploadBatch();
  const rows = proxy([{ batch }, { batch }, { batch }]);
  rows[0].batch.name = 'María 李';
  let requests = 0;
  async function grant(row) {
    if (row.batch.grant) return row.batch.grant;
    if (row.batch.creating) return row.batch.creating;
    row.batch.creating = Promise.resolve().then(() => {
      requests++;
      row.batch.grant = { id: 'verified-once', expires_at: '', strategy: 'direct' };
      return row.batch.grant;
    });
    return row.batch.creating;
  }
  const grants = await Promise.all(rows.map(grant));
  assert.equal(requests, 1);
  assert.ok(grants.every(one => one.id === 'verified-once'));
  assert.ok(rows.every(row => row.batch === batch && row.batch.name === 'María 李'));
  rows[0].batch.grant = null;
  assert.equal(rows[1].batch.grant, null, 'authoritative cleanup must invalidate the shared grant');
  const later = new UploadBatch(); later.name = 'María & David';
  assert.equal(batch.name, 'María 李');
});
