// Exact tagged-version notes. The marker preserves legacy recovery body identity.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function changelogNotes(version, changelog) {
  if (!changelog.includes('<!-- release-notes: version-section -->')) return changelog;
  const sections = changelog.replaceAll('\r\n', '\n').split(/(?=^## \[)/m);
  const matches = sections.filter(section => section.startsWith(`## [${version}]`));
  assert.equal(matches.length, 1, `Expected exactly one changelog section for ${version}`);
  return matches[0].trimEnd() + '\n';
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  process.stdout.write(changelogNotes(process.argv[2], readFileSync(process.argv[3], 'utf8')));
}
