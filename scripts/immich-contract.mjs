// Dependency-free guard for the Immich API operations PhotoDrop relies on.
//
//   node scripts/immich-contract.mjs --check --tag v3.4.2
//       Download that tag's OpenAPI spec, extract the operation view below and
//       fail on any difference from scripts/immich-contract.v3.4.2.json.
//   node scripts/immich-contract.mjs --check --tag vX --snapshot scripts/immich-contract.v3.4.2.json
//       Show how a candidate tag differs from the current baseline before a bump.
//   node scripts/immich-contract.mjs --write --tag vX
//       Write scripts/immich-contract.vX.json for review in the bump PR.
//
// --spec <file> reads a local copy of the spec instead of downloading it.
// The view per operation: method, path, x-immich-permission, parameter names,
// request body property names and required list, and 2xx response property names
// and required list, with $refs resolved one level.
import { readFileSync, writeFileSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';
import { pathToFileURL } from 'node:url';

export const operations = [
  ['post', '/albums'],
  ['get', '/albums'],
  ['get', '/albums/{id}'],
  ['put', '/albums/{id}/assets'],
  ['post', '/assets'],
  ['get', '/api-keys/me'],
  ['get', '/server/version'],
  ['post', '/oauth/authorize'],
  ['post', '/oauth/callback'],
  ['get', '/users/me'],
  ['post', '/api-keys'],
  ['delete', '/api-keys/{id}'],
  ['post', '/auth/logout'],
];

const maxSpecBytes = 16 << 20;
const sorted = values => [...(values ?? [])].sort();

function component(spec, ref) {
  const prefix = '#/components/schemas/';
  if (typeof ref !== 'string' || !ref.startsWith(prefix)) throw new Error(`unsupported $ref ${ref}`);
  const schema = spec.components?.schemas?.[ref.slice(prefix.length)];
  if (!schema) throw new Error(`missing schema ${ref}`);
  return schema;
}

// One level: a $ref (or an array of $ref items) becomes the referenced object's
// property names and required list. Nested schemas are not followed.
function shape(spec, schema) {
  if (!schema) return null;
  let array = false;
  if (schema.type === 'array') {
    array = true;
    schema = schema.items ?? {};
  }
  if (schema.$ref) schema = component(spec, schema.$ref);
  const view = { properties: sorted(Object.keys(schema.properties ?? {})), required: sorted(schema.required) };
  if (array) view.array = true;
  if (!schema.properties && schema.type) view.type = schema.type;
  return view;
}

function body(spec, content) {
  if (!content) return null;
  const types = Object.keys(content).sort();
  if (types.length !== 1) throw new Error(`expected one content type, got ${types.join(', ')}`);
  return { contentType: types[0], ...shape(spec, content[types[0]].schema) };
}

export function extract(spec) {
  return operations.map(([method, path]) => {
    const op = spec.paths?.[path]?.[method];
    if (!op) return { method, path, missing: true };
    const responses = {};
    for (const code of Object.keys(op.responses ?? {}).sort()) {
      if (!/^2\d\d$/.test(code)) continue;
      responses[code] = body(spec, op.responses[code].content);
    }
    return {
      method,
      path,
      permission: op['x-immich-permission'] ?? null,
      parameters: (op.parameters ?? []).map(p => `${p.in}:${p.name}${p.required ? '!' : ''}`).sort(),
      request: body(spec, op.requestBody?.content),
      responses,
    };
  });
}

export function differences(expected, actual) {
  const out = [];
  const key = op => `${op.method.toUpperCase()} ${op.path}`;
  const want = new Map(expected.map(op => [key(op), op]));
  const got = new Map(actual.map(op => [key(op), op]));
  for (const name of new Set([...want.keys(), ...got.keys()])) {
    const a = want.get(name), b = got.get(name);
    if (!a || !b) { out.push(`${name}: ${a ? 'missing from tag' : 'not in snapshot'}`); continue; }
    for (const field of new Set([...Object.keys(a), ...Object.keys(b)])) {
      if (!isDeepStrictEqual(a[field], b[field])) out.push(`${name} ${field}: ${JSON.stringify(a[field])} -> ${JSON.stringify(b[field])}`);
    }
  }
  return out;
}

async function download(tag) {
  const url = `https://raw.githubusercontent.com/immich-app/immich/${tag}/open-api/immich-openapi-specs.json`;
  for (let attempt = 1; ; attempt++) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(60_000) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const text = await res.text();
      if (text.length > maxSpecBytes) throw new Error('spec too large');
      return JSON.parse(text);
    } catch (err) {
      if (attempt >= 3) throw new Error(`download ${url}: ${err.message}`);
      await new Promise(r => setTimeout(r, attempt * 5000));
    }
  }
}

function args(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--check' || a === '--write') opts.mode = a.slice(2);
    else if (a === '--tag' || a === '--spec' || a === '--snapshot') opts[a.slice(2)] = argv[++i];
    else throw new Error(`unknown argument ${a}`);
  }
  if (!opts.mode || !/^v\d+\.\d+\.\d+$/.test(opts.tag ?? '')) {
    throw new Error('usage: immich-contract.mjs --check|--write --tag vX.Y.Z [--spec file] [--snapshot file]');
  }
  return opts;
}

async function main() {
  const opts = args(process.argv.slice(2));
  const spec = opts.spec ? JSON.parse(readFileSync(opts.spec, 'utf8')) : await download(opts.tag);
  const view = extract(spec);
  const snapshot = opts.snapshot ?? new URL(`./immich-contract.${opts.tag}.json`, import.meta.url);
  if (opts.mode === 'write') {
    const missing = view.filter(op => op.missing);
    if (missing.length) throw new Error(`operations missing from ${opts.tag}: ${missing.map(op => `${op.method} ${op.path}`).join(', ')}`);
    writeFileSync(snapshot, JSON.stringify({ tag: opts.tag, operations: view }, null, 2) + '\n');
    console.log(`Wrote ${view.length} operations for ${opts.tag}.`);
    return;
  }
  const expected = JSON.parse(readFileSync(snapshot, 'utf8'));
  const diff = differences(expected.operations, view);
  if (diff.length) {
    console.error(`Immich ${opts.tag} differs from the ${expected.tag} contract snapshot:`);
    for (const line of diff) console.error(`  ${line}`);
    process.exit(1);
  }
  console.log(`Immich ${opts.tag} matches the ${expected.tag} contract snapshot (${view.length} operations).`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(err => { console.error(err.message); process.exit(1); });
}
