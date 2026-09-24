import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
const manifest = JSON.parse(readFileSync(process.argv[2], 'utf8'));
for (const arch of ['amd64', 'arm64']) {
  const platform = manifest.manifests.find(m => m.platform?.os === 'linux' && m.platform.architecture === arch);
  assert.ok(platform, `Missing linux/${arch}`);
  assert.ok(manifest.manifests.some(m => m.annotations?.['vnd.docker.reference.type'] === 'attestation-manifest' && m.annotations?.['vnd.docker.reference.digest'] === platform.digest), `Missing ${arch} attestation manifest`);
}
console.log('Both platforms and associated attestation manifests are present.');
