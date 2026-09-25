// Registry reads and disposable runtime checks only. Never publishes or retags.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { runtimeManifests } from './verify-manifest.mjs';
import { verifyAttestations } from './verify-attestations.mjs';

export const command = (file, args) => execFileSync(file, args, {encoding:'utf8', stdio:['ignore','pipe','pipe'], maxBuffer:32*1024*1024});

export function verifyPublished(image, digest, version, revision, run = command) {
  assert.match(digest, /^sha256:[a-f0-9]{64}$/);
  const reference = `${image}@${digest}`;
  const inspect = (...args) => run('docker', ['buildx','imagetools','inspect',reference,...args]);
  const platforms = runtimeManifests(JSON.parse(inspect('--raw')));
  verifyAttestations(JSON.parse(inspect('--format','{{json .SBOM}}')), JSON.parse(inspect('--format','{{json .Provenance}}')));
  for (const {platform, digest:child} of platforms) {
    const runtime = `${image}@${child}`;
    run('docker', ['pull','--platform',platform,runtime]);
    // Each local reference is a distinct child digest, never the shared index.
    const shell = process.platform === 'win32' ? 'C:/Program Files/Git/bin/bash.exe' : 'sh';
    const result = run(shell, ['scripts/verify-image.sh',runtime,version,revision,platform]);
    console.log(result.trim());
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  verifyPublished(...process.argv.slice(2));
}
