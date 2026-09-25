import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { releasePlan, recoveryPlan } from './release.mjs';
import { runtimeManifests } from './verify-manifest.mjs';
import { verifyPublished } from './verify-published.mjs';
import { prepareRecovery, finishRecovery, aliasAction, checkExistingRelease, rejectNewerStable, registryDigest } from './release-recover.mjs';

const digest = letter => `sha256:${letter.repeat(64)}`;
const repository = 'xxi0xx/PhotoDrop';
const env = {GITHUB_EVENT_NAME:'workflow_dispatch', GITHUB_REF:'refs/heads/main', GITHUB_REPOSITORY:repository,
  GITHUB_WORKFLOW_REF:`${repository}/.github/workflows/release-recover.yml@refs/heads/main`, GITHUB_SHA:'a'.repeat(40)};
const commit = 'b'.repeat(40), tagObject = 'c'.repeat(40);
function index() {
  return {schemaVersion:2, mediaType:'application/vnd.oci.image.index.v1+json', manifests:[
    ...['amd64','arm64'].map((arch,i)=>({mediaType:'application/vnd.oci.image.manifest.v1+json',digest:digest(i?'b':'a'),platform:{os:'linux',architecture:arch}})),
    ...['a','b'].map((letter,i)=>({digest:digest(i?'d':'c'), platform:{os:'unknown',architecture:'unknown'}, annotations:{'vnd.docker.reference.type':'attestation-manifest','vnd.docker.reference.digest':digest(letter)}})),
  ]};
}
test('index extracts exactly the two runtime child digests, never attestations', () => {
  assert.deepEqual(runtimeManifests(index()), [{platform:'linux/amd64',digest:digest('a')},{platform:'linux/arm64',digest:digest('b')}]);
  for (const arch of ['amd64','arm64']) {
    const missing = index(); missing.manifests = missing.manifests.filter(m=>m.platform.architecture!==arch);
    assert.throws(()=>runtimeManifests(missing), /exactly one/);
    const duplicate = index(); duplicate.manifests.push(duplicate.manifests.find(m=>m.platform.architecture===arch));
    assert.throws(()=>runtimeManifests(duplicate), /exactly one/);
  }
  const disguised = index(); disguised.manifests[0].annotations = {'vnd.docker.reference.type':'attestation-manifest'};
  assert.throws(()=>runtimeManifests(disguised), /exactly one/);
  const absentEvidence = index(); absentEvidence.manifests.pop();
  assert.throws(()=>runtimeManifests(absentEvidence), /attestation/);
});
test('manual recovery shares SemVer policy and refuses non-main workflow execution', () => {
  for (const tag of ['v1.0.0','v1.1.0-rc.1']) assert.deepEqual(recoveryPlan(tag,env),releasePlan(tag,repository));
  for (const tag of ['v01.0.0','v1.0','v1.0.0-01','v1.0.0+build','main','--help','v1.0.0\n']) assert.throws(()=>recoveryPlan(tag,env));
  for (const override of [{GITHUB_EVENT_NAME:'push'},{GITHUB_REF:'refs/heads/feature'},{GITHUB_WORKFLOW_REF:`${repository}/.github/workflows/release-recover.yml@refs/heads/feature`}]) assert.throws(()=>recoveryPlan('v1.0.0',{...env,...override}),/trusted main/);
});
function fixture(tag = 'v1.0.0') {
  const calls = [], aliases = new Map(), policy = releasePlan(tag,repository);
  const state = {existing:null, releases:[], immutable:digest('e'), notes:null};
  const fail = stderr => { const error = new Error(stderr); error.stderr=stderr; throw error; };
  function run(file,args) {
    calls.push([file,...args]);
    if (file==='git') {
      if (args[0]==='ls-remote') return `${tagObject}\trefs/tags/${tag}\n`;
      if (args[0]==='fetch' || args[0]==='merge-base') return '';
      if (args[0]==='rev-parse') return (args.includes('FETCH_HEAD^{commit}') ? commit : tagObject)+'\n';
      if (args[0]==='show') return args[1].endsWith(':LICENSE') ? readFileSync('LICENSE','utf8') : '# Tagged release notes\n';
    }
    if (file==='docker' && args[0]==='buildx' && args[1]==='imagetools') {
      if (args[2]==='inspect') {
        if(args[3]===policy.immutable) return JSON.stringify({digest:state.immutable});
        if(!aliases.has(args[3])) return fail(`ERROR: ${args[3]}: not found`);
        return JSON.stringify({digest:aliases.get(args[3])});
      }
      if (args[2]==='create') { assert.ok(policy.aliases.includes(args[4])); aliases.set(args[4],args[5].split('@')[1]); return ''; }
    }
    if(file==='gh') {
      if(args.includes('--paginate')) return JSON.stringify([state.releases]);
      if(args[0]==='api') return state.existing ? JSON.stringify(state.existing) : fail('gh: Not Found (HTTP 404)');
      if(args[0]==='release' && args[1]==='create') {
        state.notes=readFileSync(args[args.indexOf('--notes-file')+1],'utf8');
        state.existing={tag_name:tag,name:`PhotoDrop ${policy.version}`,draft:false,prerelease:policy.prerelease,body:state.notes};
        return '';
      }
    }
    throw new Error(`Unexpected command ${file} ${args.join(' ')}`);
  }
  return {run,calls,aliases,state,policy};
}
test('recovery resolves remote tag commit and reads tagged license/changelog, not main', () => {
  const f=fixture(), plan=prepareRecovery('v1.0.0',digest('e'),env,f.run);
  assert.equal(plan.commit,commit); assert.notEqual(plan.commit,env.GITHUB_SHA);
  assert.ok(f.calls.some(c=>c.join(' ')==='git merge-base --is-ancestor '+commit+' origin/main'));
  assert.ok(f.calls.some(c=>c.join(' ')==='git show '+commit+':CHANGELOG.md'));
  assert.throws(()=>prepareRecovery('v1.0.0',digest('f'),env,f.run),/operator-confirmed/);
  const missing = (...args)=> {if(args[0]==='git' && args[1][0]==='ls-remote') throw new Error('missing tag'); return f.run(...args);};
  assert.throws(()=>prepareRecovery('v1.0.0',digest('e'),env,missing),/missing tag/);
  const unmerged = (...args)=> {if(args[0]==='git' && args[1][0]==='merge-base') throw new Error('unmerged'); return f.run(...args);};
  assert.throws(()=>prepareRecovery('v1.0.0',digest('e'),env,unmerged),/unmerged/);
});
test('real Git fetch resolves annotated and lightweight remote tags', () => {
  const dir=mkdtempSync(join(tmpdir(),'photodrop-recovery-git-'));
  const origin=join(dir,'origin'), checkout=join(dir,'checkout'); mkdirSync(origin);
  const git=(cwd,...args)=>execFileSync('git',['-c','user.name=Release test','-c','user.email=release@example.test',...args],{cwd,encoding:'utf8',stdio:['ignore','pipe','pipe']});
  try {
    git(origin,'init','-b','main');
    writeFileSync(join(origin,'LICENSE'),readFileSync('LICENSE'));
    writeFileSync(join(origin,'CHANGELOG.md'),'Tagged notes\n');
    git(origin,'add','.'); git(origin,'commit','-m','tagged state');
    const tagged=git(origin,'rev-parse','HEAD').trim();
    git(origin,'tag','v2.0.0'); git(origin,'tag','-a','v2.0.1','-m','annotated fixture');
    writeFileSync(join(origin,'CHANGELOG.md'),'Later main notes\n'); git(origin,'commit','-am','later main');
    git(dir,'clone',origin,checkout);
    for(const tag of ['v2.0.0','v2.0.1']) {
      const run=(file,args)=>file==='git' ? git(checkout,...args) : JSON.stringify({digest:digest('e')});
      const plan=prepareRecovery(tag,digest('e'),env,run);
      assert.equal(plan.commit,tagged);
      assert.ok(plan.notes.endsWith('Tagged notes\n'));
      assert.ok(!plan.notes.includes('Later main'));
    }
  } finally {rmSync(dir,{recursive:true,force:true});}
});
test('published verifier pulls distinct child refs and forwards resolved tag revision', () => {
  const f=fixture(), plan=prepareRecovery('v1.0.0',digest('e'),env,f.run), calls=[];
  const sbom=Object.fromEntries(['amd64','arm64'].map(a=>[`linux/${a}`,{SPDX:{spdxVersion:'SPDX-2.3',packages:[{}]}}]));
  const provenance=Object.fromEntries(['amd64','arm64'].map(a=>[`linux/${a}`,{SLSA:{buildDefinition:{buildType:'buildkit'}}}])) ;
  const run=(file,args)=>{
    calls.push([file,...args]);
    if(args.includes('--raw')) return JSON.stringify(index());
    if(args.includes('{{json .SBOM}}')) return JSON.stringify(sbom);
    if(args.includes('{{json .Provenance}}')) return JSON.stringify(provenance);
    if(args[0]==='pull') assert.notEqual(args.at(-1),`${plan.image}@${plan.digest}`, 'Classic Docker digest collision regression');
    return '';
  };
  verifyPublished(plan.image,plan.digest,plan.version,plan.commit,run);
  assert.deepEqual(calls.filter(c=>c[1]==='pull').map(c=>c.at(-1)),['a','b'].map(d=>`${plan.image}@${digest(d)}`));
  const runtimes=calls.filter(c=>c[1]==='scripts/verify-image.sh');
  assert.equal(runtimes.length,2); assert.ok(runtimes.every(c=>c[4]===commit));
  const failing=(file,args)=> {if(args.includes('{{json .Provenance}}')) return '{}'; return run(file,args);};
  calls.length=0; assert.throws(()=>verifyPublished(plan.image,plan.digest,plan.version,plan.commit,failing),/provenance/);
  assert.equal(calls.filter(c=>c[1]==='pull').length,0);
});
test('recovery creates only missing aliases and tagged release, then reruns without writes', () => {
  const f=fixture(), plan=prepareRecovery('v1.0.0',digest('e'),env,f.run);
  f.aliases.set(plan.aliases[0],plan.digest);
  finishRecovery(plan,env,f.run);
  assert.equal(f.calls.filter(c=>c[3]==='create').length,2);
  assert.equal(f.state.notes,plan.notes);
  const before=f.calls.length; finishRecovery(plan,env,f.run);
  assert.ok(!f.calls.slice(before).some(c=>c.includes('create')));
  assert.ok(!f.calls.some(c=>c.includes('push') || c.includes('build') || c.includes('delete')));
});
test('conflicting aliases/releases and newer stable releases fail before any writes', () => {
  for (const conflict of ['alias','release','newer','immutable']) {
    const f=fixture(), plan=prepareRecovery('v1.0.0',digest('e'),env,f.run);
    if(conflict==='alias') f.aliases.set(plan.aliases[2],digest('f'));
    if(conflict==='release') f.state.existing={tag_name:plan.tag,name:`PhotoDrop ${plan.version}`,draft:false,prerelease:false,body:'unrelated'};
    if(conflict==='newer') f.state.releases=[{tag_name:'v1.1.0',draft:false,prerelease:false}];
    if(conflict==='immutable') f.state.immutable=digest('f');
    assert.throws(()=>finishRecovery(plan,env,f.run));
    assert.ok(!f.calls.some(c=>c.includes('create')));
  }
  assert.equal(aliasAction(null,digest('e')),'create');
  assert.equal(aliasAction(digest('e'),digest('e')),'keep');
  assert.throws(()=>aliasAction(digest('f'),digest('e')));
  assert.throws(()=>checkExistingRelease({tag_name:'v9.0.0'},{tag:'v1.0.0'}));
  assert.doesNotThrow(()=>rejectNewerStable([{tag_name:'v0.9.0'}],{...releasePlan('v1.0.0',repository),repository}));
});
test('prerelease recovery creates no stable aliases and never marks latest', () => {
  const f=fixture('v1.1.0-rc.1'), plan=prepareRecovery('v1.1.0-rc.1',digest('e'),env,f.run);
  finishRecovery(plan,env,f.run);
  assert.equal(f.aliases.size,0);
  const create=f.calls.find(c=>c[0]==='gh' && c[1]==='release');
  assert.ok(create.includes('--prerelease') && create.includes('--latest=false'));
});
test('registry auth, transport and rate-limit errors never masquerade as missing aliases', () => {
  for(const stderr of ['unauthorized','HTTP 429','connection refused','invalid JSON','ERROR: another:1: not found']) {
    assert.throws(()=>registryDigest('example:1',()=>{const e=new Error(stderr);e.stderr=stderr;throw e;},true));
  }
  assert.equal(registryDigest('example:1',()=>{const e=new Error();e.stderr='ERROR: example:1: not found\n';throw e;},true),null);
});
test('workflow boundaries keep recovery manual/main-only and verify before writes', () => {
  const workflow=readFileSync('.github/workflows/release-recover.yml','utf8');
  assert.match(workflow,/workflow_dispatch:/); assert.match(workflow,/github.ref == 'refs\/heads\/main'/);
  assert.match(workflow,/fetch-depth: 0/); assert.match(workflow,/group: photodrop-release/);
  assert.ok(!/build-push-action|docker (?:build|push)|git (?:push|tag)|pull_request:|\n  push:/.test(workflow));
  assert.match(workflow,/TAG_COMMIT: \$\{\{ steps.plan.outputs.commit \}\}/);
  assert.match(workflow,/verify-published.mjs "\$IMAGE" "\$DIGEST" "\$VERSION" "\$TAG_COMMIT"/);
  assert.ok(workflow.indexOf('verify-published.mjs')<workflow.indexOf('release-recover.mjs finish'));
  const normal=readFileSync('.github/workflows/release.yml','utf8');
  assert.match(normal,/verify-published.mjs/); assert.ok(!normal.includes('docker pull --platform'));
  assert.ok(normal.indexOf('verify-published.mjs')<normal.indexOf('Promote verified aliases'));
});
