// Dependency-free local Markdown link and production configuration coverage check.
import { readFileSync, readdirSync, existsSync } from 'node:fs';
import { resolve, dirname, extname, relative } from 'node:path';
import assert from 'node:assert/strict';
const root = process.cwd();
const files = [...readdirSync(root).filter(f => f.endsWith('.md')), ...readdirSync('docs').filter(f => f.endsWith('.md')).map(f => `docs/${f}`)];
let links = 0;
function anchors(text) {
  const counts = new Map();
  return new Set([...text.matchAll(/^#{1,6}\s+(.+)$/gm)].map(([, title]) => {
    const slug = title.toLowerCase().replace(/<[^>]*>/g, '').replace(/[^\p{L}\p{N}\s_-]/gu, '').replace(/\s/g, '-');
    const n = counts.get(slug) ?? 0; counts.set(slug, n + 1); return slug + (n ? `-${n}` : '');
  }));
}
for (const file of files) {
  const body = readFileSync(file, 'utf8').replace(/```[^\n]*\n[\s\S]*?```/g, '');
  for (const [, raw] of body.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
    const href = raw.replace(/^<|>$/g, '');
    if (/^[a-z]+:/i.test(href)) continue; // External links need explicit review, never guessed success.
    const [path, fragment] = href.split('#');
    const target = path ? resolve(dirname(file), decodeURIComponent(path)) : resolve(file);
    assert.ok(!relative(root, target).startsWith('..'), `${file}: link escapes repository`);
    assert.ok(existsSync(target), `${file}: missing ${href}`);
    if (fragment && extname(target) === '.md') assert.ok(anchors(readFileSync(target, 'utf8')).has(decodeURIComponent(fragment)), `${file}: missing anchor ${href}`);
    links++;
  }
}
const reference = readFileSync('docs/configuration.md', 'utf8');
const source = readdirSync('internal/config').filter(f => f.endsWith('.go') && !f.endsWith('_test.go')).map(f => readFileSync(`internal/config/${f}`, 'utf8')).join('\n');
for (const name of new Set(source.match(/PHOTODROP_[A-Z_]+/g))) {
  if (name.endsWith('_')) continue; // Dynamic families are checked explicitly below.
  assert.ok(reference.includes(name), `Missing configuration documentation: ${name}`);
}
for (const suffix of ['BUCKET','REGION','ENDPOINT','PREFIX','ACCESS_KEY_ID','SECRET_ACCESS_KEY','SESSION_TOKEN','PATH_STYLE','PRESIGN_TTL']) assert.ok(reference.includes(`PHOTODROP_S3_${suffix}`));
for (const suffix of ['URL','API_KEY']) assert.ok(reference.includes(`PHOTODROP_IMMICH_<KEY>_${suffix}`));
console.log(`Validated ${files.length} Markdown files, ${links} local links/anchors, and production environment coverage. External links require separate review.`);
