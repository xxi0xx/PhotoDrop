import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
export function verifyAttestations(sbom, provenance) {
  for (const platform of ['linux/amd64', 'linux/arm64']) {
    assert.ok(sbom[platform]?.SPDX?.spdxVersion?.startsWith('SPDX-'), `Missing SPDX SBOM: ${platform}`);
    assert.ok(sbom[platform].SPDX.packages?.length, `Empty SBOM: ${platform}`);
    const slsa = provenance[platform]?.SLSA;
    assert.ok(slsa?.buildType || slsa?.buildDefinition?.buildType, `Missing SLSA provenance: ${platform}`);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  verifyAttestations(JSON.parse(readFileSync(process.argv[2], 'utf8')), JSON.parse(readFileSync(process.argv[3], 'utf8')));
  console.log('Both platform SPDX SBOMs and SLSA provenance are present.');
}
