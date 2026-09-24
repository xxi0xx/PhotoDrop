// Pure release policy shared by CI and publishing. No registry writes here.
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function releasePlan(tag, repository) {
  // SemVer without build metadata: '+' is not a valid Docker tag character.
  const match = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/.exec(tag);
  if (!match || /\s/.test(tag) || tag.length > 128) throw new Error('Expected vMAJOR.MINOR.PATCH[-PRERELEASE], without build metadata');
  const [, major, minor, , pre] = match;
  if (pre?.split('.').some(x => /^\d+$/.test(x) && x.length > 1 && x.startsWith('0'))) throw new Error('Leading zero in numeric prerelease identifier');
  if (!/^[\w-]+\/[\w.-]+$/.test(repository)) throw new Error('Invalid repository');
  const version = tag.slice(1), image = `ghcr.io/${repository.toLowerCase()}`;
  return { version, image, prerelease: Boolean(pre), immutable: `${image}:${version}`, aliases: pre ? [] : [`${image}:${major}.${minor}`, `${image}:${major}`, `${image}:latest`] };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.env.GITHUB_EVENT_NAME !== 'push' || process.env.GITHUB_REF_TYPE !== 'tag') throw new Error('Publishing requires a tag push');
  const plan = releasePlan(process.env.GITHUB_REF_NAME, process.env.GITHUB_REPOSITORY);
  if (!process.env.GITHUB_OUTPUT) throw new Error('Missing Actions output path');
  for (const key of ['version', 'image', 'prerelease', 'immutable']) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${plan[key]}\n`);
  appendFileSync(process.env.GITHUB_OUTPUT, `aliases=${JSON.stringify(plan.aliases)}\n`);
}
