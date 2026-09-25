import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync, readFileSync, readdirSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { releasePlan } from './release.mjs';

test('root license matches canonical Apache License 2.0 text', () => {
  // Canonical https://www.apache.org/licenses/LICENSE-2.0.txt; tolerate checkout CRLF only.
  const license = readFileSync('LICENSE', 'utf8').replace(/\r\n/g, '\n');
  assert.equal(createHash('sha256').update(license).digest('hex'), 'cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30');
});

test('OCI verification requires Apache-2.0, rejecting absent or different licenses', () => {
  const dir = mkdtempSync(join(tmpdir(), 'photodrop-oci-license-'));
  mkdirSync(join(dir, 'blobs', 'sha256'), {recursive:true});
  function blob(value) {
    const bytes = Buffer.from(JSON.stringify(value)), hash = createHash('sha256').update(bytes).digest('hex');
    writeFileSync(join(dir, 'blobs', 'sha256', hash), bytes);
    return {digest:`sha256:${hash}`, size:bytes.length};
  }
  try {
    for (const license of [undefined, 'MIT', 'Apache-2.0']) {
      const config = blob({os:'linux', architecture:'amd64', config:{Labels:{
        'org.opencontainers.image.version':'1.0.0-rc.test',
        'org.opencontainers.image.revision':'test',
        'org.opencontainers.image.source':'https://github.com/xxi0xx/PhotoDrop',
        'org.opencontainers.image.licenses':license,
      }}});
      const image = blob({config, layers:[]});
      const subject = [{digest:{sha256:image.digest.slice(7)}}];
      const attestation = blob({layers:[
        blob({subject, predicateType:'https://spdx.dev/Document', predicate:{spdxVersion:'SPDX-2.3', packages:[{name:'photodrop'}]}}),
        blob({subject, predicateType:'https://slsa.dev/provenance/v1', predicate:{}}),
      ]});
      attestation.annotations = {'vnd.docker.reference.type':'attestation-manifest', 'vnd.docker.reference.digest':image.digest};
      writeFileSync(join(dir, 'index.json'), JSON.stringify({manifests:[image, attestation]}));
      const result = spawnSync(process.execPath, ['scripts/verify-oci.mjs', dir, 'amd64', '1.0.0-rc.test', 'test'], {encoding:'utf8'});
      if (license === 'Apache-2.0') assert.equal(result.status, 0, result.stderr);
      else { assert.notEqual(result.status, 0); assert.match(result.stderr, /Apache-2\.0/); }
    }
  } finally { rmSync(dir, {recursive:true, force:true}); }
});

test('stable and prerelease tags', () => {
  assert.deepEqual(releasePlan('v1.0.0', 'xxi0xx/PhotoDrop'), { version: '1.0.0', image: 'ghcr.io/xxi0xx/photodrop', immutable: 'ghcr.io/xxi0xx/photodrop:1.0.0', prerelease: false, aliases: ['ghcr.io/xxi0xx/photodrop:1.0', 'ghcr.io/xxi0xx/photodrop:1', 'ghcr.io/xxi0xx/photodrop:latest'] });
  assert.deepEqual(releasePlan('v1.1.0-rc.1', 'xxi0xx/PhotoDrop').aliases, []);
  for (const tag of ['1.0.0', 'v01.0.0', 'v1.0', 'v1.0.0-01', 'v1.0.0+build', 'v1.0.0\n', 'v1.0.0-']) assert.throws(() => releasePlan(tag, 'xxi0xx/PhotoDrop'), tag);
});
test('PR, dispatch and branch events cannot enter publish path', () => {
  for (const [event, type] of [['pull_request', 'branch'], ['workflow_dispatch', 'tag'], ['push', 'branch']]) {
    assert.notEqual(spawnSync(process.execPath, ['scripts/release.mjs'], { env: { ...process.env, GITHUB_EVENT_NAME: event, GITHUB_REF_TYPE: type } }).status, 0);
  }
});
test('tag ancestry rejects an unmerged commit and accepts merged history', () => {
  const dir = mkdtempSync(join(tmpdir(), 'photodrop-ancestry-'));
  const git = (...args) => execFileSync('git', ['-c', 'user.name=Release test', '-c', 'user.email=release@example.test', ...args], {cwd:dir,encoding:'utf8',stdio:['ignore','pipe','pipe']});
  try {
    git('init', '-b', 'main'); git('commit', '--allow-empty', '-m', 'baseline');
    git('switch', '-c', 'candidate'); git('commit', '--allow-empty', '-m', 'candidate');
    const sha = git('rev-parse','HEAD').trim();
    assert.notEqual(spawnSync('git',['merge-base','--is-ancestor',sha,'main'],{cwd:dir}).status,0);
    git('switch','main'); git('merge','--ff-only','candidate');
    assert.equal(spawnSync('git',['merge-base','--is-ancestor',sha,'main'],{cwd:dir}).status,0);
  } finally { rmSync(dir,{recursive:true,force:true}); }
});
test('workflow credentials and action pins stay constrained', () => {
  for (const file of readdirSync('.github/workflows').filter(f=>f.endsWith('.yml'))) {
    const content=readFileSync(`.github/workflows/${file}`,'utf8');
    assert.ok(!content.includes('pull_request_target'));
    for (const [,action] of content.matchAll(/uses:\s+([^\s#]+)/g)) {
      if (!action.startsWith('./')) assert.match(action, /^[\w.-]+\/[\w./-]+@[a-f0-9]{40}$/);
    }
    if (!['release.yml','release-recover.yml'].includes(file)) assert.ok(!/\bwrite\b/.test(content),`${file} has write permissions`);
  }
});
test('artifact inspection rejects missing architectures, SBOM and provenance', () => {
  const dir=mkdtempSync(join(tmpdir(),'photodrop-attestations-'));
  const manifest={schemaVersion:2, mediaType:'application/vnd.oci.image.index.v1+json', manifests:[]}, sbom={}, provenance={};
  for (const arch of ['amd64','arm64']) {
    const digest=`sha256:${(arch === 'amd64' ? 'a' : 'b').repeat(64)}`;
    manifest.manifests.push({digest,mediaType:'application/vnd.oci.image.manifest.v1+json',platform:{os:'linux',architecture:arch}},{digest:`sha256:${'c'.repeat(64)}`,annotations:{'vnd.docker.reference.type':'attestation-manifest','vnd.docker.reference.digest':digest}});
    sbom[`linux/${arch}`]={SPDX:{spdxVersion:'SPDX-2.3',packages:[{name:'photodrop'}]}};
    provenance[`linux/${arch}`]={SLSA:{buildType:'https://mobyproject.org/buildkit@v1'}};
  }
  const m=join(dir,'manifest.json'),s=join(dir,'sbom.json'),p=join(dir,'provenance.json');
  const check=(script,...args)=>spawnSync(process.execPath,[script,...args]).status;
  try {
    writeFileSync(m,JSON.stringify(manifest)); writeFileSync(s,JSON.stringify(sbom)); writeFileSync(p,JSON.stringify(provenance));
    assert.equal(check('scripts/verify-manifest.mjs',m),0);
    assert.equal(check('scripts/verify-attestations.mjs',s,p),0);
    manifest.manifests.splice(2); writeFileSync(m,JSON.stringify(manifest));
    assert.notEqual(check('scripts/verify-manifest.mjs',m),0);
    const arm = sbom['linux/arm64'];
    delete sbom['linux/arm64']; writeFileSync(s,JSON.stringify(sbom));
    assert.notEqual(check('scripts/verify-attestations.mjs',s,p),0);
    sbom['linux/arm64'] = arm; writeFileSync(s,JSON.stringify(sbom));
    writeFileSync(p,'{}'); assert.notEqual(check('scripts/verify-attestations.mjs',s,p),0);
  } finally { rmSync(dir,{recursive:true,force:true}); }
});
test('actual dev and injected binaries report version without configuration', () => {
  const dir = mkdtempSync(join(tmpdir(), 'photodrop-version-'));
  const binary = join(dir, process.platform === 'win32' ? 'photodrop.exe' : 'photodrop');
  try {
    for (const [flags, expected] of [['', 'PhotoDrop dev'], ['-X photodrop/internal/buildinfo.Version=1.0.0', 'PhotoDrop 1.0.0'], ['-X photodrop/internal/buildinfo.Commit=abcdef', 'PhotoDrop dev (abcdef)'], ['-X photodrop/internal/buildinfo.Version=1.1.0-rc.1 -X photodrop/internal/buildinfo.Commit=abcdef', 'PhotoDrop 1.1.0-rc.1 (abcdef)']]) {
      execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-ldflags', flags, '-o', binary, './cmd/photodrop'], { env: { ...process.env, CGO_ENABLED: '0' } });
      assert.equal(execFileSync(binary, ['version'], { encoding: 'utf8', env: { ...process.env, PHOTODROP_ADMIN_PASSWORD: '' } }).trim(), expected);
    }
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
