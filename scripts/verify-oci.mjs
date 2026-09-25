// Validate a local BuildKit OCI export without registry writes or credentials.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createHash } from 'node:crypto';
import assert from 'node:assert/strict';
const [directory,arch,version,revision]=process.argv.slice(2);
assert.ok(['amd64','arm64'].includes(arch));
function blob(descriptor) {
  assert.match(descriptor.digest,/^sha256:[a-f0-9]{64}$/);
  const content=readFileSync(join(directory,'blobs','sha256',descriptor.digest.slice(7)));
  assert.equal(content.length,descriptor.size);
  assert.equal(createHash('sha256').update(content).digest('hex'),descriptor.digest.slice(7));
  return JSON.parse(content);
}
const images=[], attestations=[];
function visit(index) {
  for(const descriptor of index.manifests) {
    const data=blob(descriptor);
    if(data.manifests) visit(data);
    else if(descriptor.annotations?.['vnd.docker.reference.type']==='attestation-manifest') attestations.push({descriptor,data});
    else images.push({descriptor,data,config:blob(data.config)});
  }
}
visit(JSON.parse(readFileSync(join(directory,'index.json'),'utf8')));
const image=images.find(i=>i.config.os==='linux' && i.config.architecture===arch);
assert.ok(image,`Missing linux/${arch}`);
const labels=image.config.config.Labels;
assert.equal(labels['org.opencontainers.image.version'],version);
assert.equal(labels['org.opencontainers.image.revision'],revision);
assert.equal(labels['org.opencontainers.image.source'],'https://github.com/xxi0xx/PhotoDrop');
assert.equal(labels['org.opencontainers.image.licenses'],'Apache-2.0');
const records=attestations.filter(a=>a.descriptor.annotations['vnd.docker.reference.digest']===image.descriptor.digest).flatMap(a=>a.data.layers.map(blob));
assert.ok(records.some(r=>r.predicateType==='https://spdx.dev/Document' && r.predicate?.spdxVersion?.startsWith('SPDX-') && r.predicate.packages?.length),'Missing SPDX SBOM');
assert.ok(records.some(r=>r.predicateType?.startsWith('https://slsa.dev/provenance/') && r.predicate),'Missing SLSA provenance');
for(const record of records) assert.ok(record.subject.some(s=>s.digest?.sha256===image.descriptor.digest.slice(7)),'Attestation subject mismatch');
console.log(`Local OCI linux/${arch}: version/revision, digest integrity, SPDX SBOM and SLSA provenance verified.`);
