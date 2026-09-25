import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

export function runtimeManifests(index) {
  assert.equal(index.schemaVersion, 2);
  assert.ok(['application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json'].includes(index.mediaType), 'Expected multi-platform index');
  assert.ok(Array.isArray(index.manifests));
  const attestation = m => m.annotations?.['vnd.docker.reference.type'] === 'attestation-manifest';
  const result = ['amd64', 'arm64'].map(arch => {
    const matches = index.manifests.filter(m => !attestation(m) && m.platform?.os === 'linux' && m.platform.architecture === arch);
    assert.equal(matches.length, 1, `Expected exactly one linux/${arch} runtime manifest`);
    const runtime = matches[0];
    assert.ok(['application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json'].includes(runtime.mediaType));
    assert.match(runtime.digest, /^sha256:[a-f0-9]{64}$/);
    assert.ok(!runtime.platform.variant || (arch === 'arm64' && runtime.platform.variant === 'v8'), 'Unexpected runtime variant');
    const evidence = index.manifests.filter(m => attestation(m) && m.annotations['vnd.docker.reference.digest'] === runtime.digest);
    assert.ok(evidence.length, `Missing ${arch} attestation manifest`);
    for (const record of evidence) assert.match(record.digest, /^sha256:[a-f0-9]{64}$/);
    return {platform:`linux/${arch}`, digest:runtime.digest};
  });
  assert.notEqual(result[0].digest, result[1].digest, 'Runtime platforms must have distinct child digests');
  return result;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  console.log(JSON.stringify(runtimeManifests(JSON.parse(readFileSync(process.argv[2], 'utf8')))));
}
