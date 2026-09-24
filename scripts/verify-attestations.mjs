import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const sbom = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const provenance = JSON.parse(readFileSync(process.argv[3], 'utf8'));
for (const platform of ['linux/amd64', 'linux/arm64']) {
  assert.ok(sbom[platform]?.SPDX?.spdxVersion?.startsWith('SPDX-'), `Missing SPDX SBOM: ${platform}`);
  assert.ok(sbom[platform].SPDX.packages?.length, `Empty SBOM: ${platform}`);
  const slsa = provenance[platform]?.SLSA;
  assert.ok(slsa?.buildType || slsa?.buildDefinition?.buildType, `Missing SLSA provenance: ${platform}`);
}
console.log('Both platform SPDX SBOMs and SLSA provenance are present.');
