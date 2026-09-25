// Manual, main-only completion of an existing artifact. No image builds/deletes,
// immutable-tag writes, remote Git mutations, or release edits are implemented here.
import assert from 'node:assert/strict';
import { appendFileSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { recoveryPlan, releasePlan, writePlanOutputs } from './release.mjs';
import { command } from './verify-published.mjs';

const digestPattern = /^sha256:[a-f0-9]{64}$/;
function remoteTag(tag, run) {
  const rows = run('git', ['ls-remote','--exit-code','origin',`refs/tags/${tag}`]).trim().split('\n');
  assert.equal(rows.length, 1, 'Expected one existing remote tag');
  const [object, ref] = rows[0].split(/\s+/);
  assert.equal(ref, `refs/tags/${tag}`);
  assert.match(object, /^[a-f0-9]{40}$/);
  return object;
}
export function registryDigest(reference, run = command, allowMissing = false) {
  let result;
  try { result = run('docker', ['buildx','imagetools','inspect',reference,'--format','{{json .Manifest}}']); }
  catch (error) {
    const stderr = String(error.stderr);
    const referenceMissing = stderr.split('\n').some(line => line.trim() === `ERROR: ${reference}: not found`);
    if (allowMissing && (referenceMissing || /manifest unknown|manifest[^\n]*not found/i.test(stderr))) return null;
    throw error; // Authentication, transport and rate limits are never absence.
  }
  const digest = JSON.parse(result).digest;
  assert.match(digest, digestPattern);
  return digest;
}
const notes = (plan, changelog) => `Container: \`${plan.image}@${plan.digest}\`\n\n${changelog}`;

export function prepareRecovery(tag, expectedDigest, env = process.env, run = command) {
  const policy = recoveryPlan(tag, env);
  assert.match(expectedDigest, digestPattern, 'Explicit expected index digest required');
  const tagObject = remoteTag(tag, run);
  run('git', ['fetch','origin','refs/heads/main:refs/remotes/origin/main']);
  run('git', ['fetch','origin',`refs/tags/${tag}`]);
  const commit = run('git', ['rev-parse','--verify','FETCH_HEAD^{commit}']).trim();
  assert.match(commit, /^[a-f0-9]{40}$/);
  assert.equal(run('git', ['rev-parse','FETCH_HEAD']).trim(), tagObject, 'Remote tag changed while fetching');
  run('git', ['merge-base','--is-ancestor',commit,'origin/main']);
  assert.ok(readFileSync('LICENSE', 'utf8').trim(), 'Root LICENSE required');
  assert.ok(run('git', ['show',`${commit}:LICENSE`]).trim(), 'Tagged LICENSE required');
  const digest = registryDigest(policy.immutable, run);
  assert.equal(digest, expectedDigest, 'Immutable image differs from the operator-confirmed index');
  const plan = {...policy, tag, repository:env.GITHUB_REPOSITORY, tagObject, commit, digest};
  plan.notes = notes(plan, run('git', ['show',`${commit}:CHANGELOG.md`]));
  return plan;
}

export function aliasAction(current, verified) {
  assert.match(verified, digestPattern);
  if (current === null) return 'create';
  assert.equal(current, verified, 'Conflicting alias; refusing to overwrite or downgrade');
  return 'keep';
}
export function checkExistingRelease(existing, plan) {
  if (existing === null) return;
  assert.equal(existing.tag_name, plan.tag);
  assert.equal(existing.name, `PhotoDrop ${plan.version}`);
  assert.equal(existing.draft, false);
  assert.equal(existing.prerelease, plan.prerelease);
  assert.equal(existing.body.trimEnd(), plan.notes.trimEnd(), 'Conflicting GitHub Release; refusing to overwrite');
}
export function rejectNewerStable(releases, plan) {
  if (plan.prerelease) return;
  const target = plan.version.split('.').map(BigInt);
  for (const release of releases) {
    if (release.draft || release.prerelease) continue;
    let other;
    try { other = releasePlan(release.tag_name, plan.repository); } catch { continue; }
    if (other.prerelease) continue;
    const values = other.version.split('.').map(BigInt);
    const first = values.findIndex((value, i) => value !== target[i]);
    assert.ok(first < 0 || values[first] < target[first], 'Newer stable release exists; refusing alias downgrade');
  }
}
function existingRelease(plan, run) {
  try { return JSON.parse(run('gh', ['api',`repos/${plan.repository}/releases/tags/${plan.tag}`])); }
  catch (error) {
    if (/\(HTTP 404\)/.test(String(error.stderr))) return null;
    throw error;
  }
}

// Invoked only after verify-published succeeds in the workflow. All state checks
// precede writes, then are repeated near writes. Concurrent external publishers
// must be excluded by the operator; registry tags offer no compare-and-swap.
export function finishRecovery(plan, env = process.env, run = command) {
  const policy = recoveryPlan(plan.tag, env);
  for (const key of Object.keys(policy)) assert.deepEqual(plan[key], policy[key]);
  assert.equal(plan.repository, env.GITHUB_REPOSITORY);
  assert.match(plan.commit, /^[a-f0-9]{40}$/);
  assert.match(plan.digest, digestPattern);
  assert.equal(plan.notes, notes(plan, run('git', ['show',`${plan.commit}:CHANGELOG.md`])));
  const identity = () => {
    assert.equal(remoteTag(plan.tag, run), plan.tagObject, 'Remote tag changed');
    assert.equal(registryDigest(plan.immutable, run), plan.digest, 'Immutable image changed');
  };
  const releaseCheck = () => {
    const releases = JSON.parse(run('gh', ['api','--paginate','--slurp',`repos/${plan.repository}/releases?per_page=100`])).flat();
    rejectNewerStable(releases, plan);
    const existing = existingRelease(plan, run);
    checkExistingRelease(existing, plan);
    return existing;
  };
  identity();
  releaseCheck();
  // Preflight every alias before creating even the first missing one.
  for (const alias of plan.aliases) aliasAction(registryDigest(alias, run, true), plan.digest);
  for (const alias of plan.aliases) {
    identity();
    if (aliasAction(registryDigest(alias, run, true), plan.digest) === 'create') {
      run('docker', ['buildx','imagetools','create','--tag',alias,`${plan.image}@${plan.digest}`]);
      assert.equal(registryDigest(alias, run), plan.digest, 'Alias did not retain verified index');
    }
  }
  identity();
  if (releaseCheck() === null) {
    // Tagged changelog only; no source from the current-main application build.
    const dir = mkdtempSync(join(tmpdir(), 'photodrop-release-notes-'));
    try {
      const file = join(dir, 'notes.md');
      writeFileSync(file, plan.notes);
      run('gh', ['release','create',plan.tag,'--repo',plan.repository,'--verify-tag','--title',`PhotoDrop ${plan.version}`,
        '--notes-file',file, ...(plan.prerelease ? ['--prerelease','--latest=false'] : ['--latest'])]);
    } finally { rmSync(dir, {recursive:true, force:true}); }
  }
  console.log('Existing verified artifact publication completed; immutable tag and Git tag preserved.');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [mode, ...args] = process.argv.slice(2);
  if (mode === 'prepare') {
    const [tag, expectedDigest, file] = args;
    const plan = prepareRecovery(tag, expectedDigest);
    writeFileSync(file, JSON.stringify(plan));
    writePlanOutputs(plan);
    for (const key of ['commit','digest']) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${plan[key]}\n`);
  } else if (mode === 'finish') {
    finishRecovery(JSON.parse(readFileSync(args[0], 'utf8')));
  } else throw new Error('Expected prepare or finish');
}
