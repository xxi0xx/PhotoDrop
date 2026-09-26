import { readFileSync } from 'node:fs';
import { randomBytes } from 'node:crypto';

// Original tiny fixtures shared with Go tests; no runtime encoder dependency.
export function video(format = 'mp4', unique = false) {
  if (!['mp4', 'mov'].includes(format)) throw new Error('Unsupported test fixture');
  const bytes = readFileSync(new URL(`../internal/testutil/testdata/tiny.${format}`, import.meta.url));
  // A valid trailing free box makes a rerun independent of Immich deduplication
  // from earlier disposable events, without changing the encoded video frame.
  return unique ? Buffer.concat([bytes, Buffer.from([0,0,0,24,102,114,101,101]), randomBytes(16)]) : bytes;
}
