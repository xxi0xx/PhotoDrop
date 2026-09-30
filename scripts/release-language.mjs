// Current product copy only. Dated evidence is explicitly enumerated and labeled.
export const historicalDocs = new Set([
  'docs/gate-3-validation.md', 'docs/gate-4-validation.md', 'docs/gate-5-validation.md',
  'docs/gate-6-validation.md', 'docs/gate-7-validation.md', 'docs/gate-8-validation.md',
  'docs/v11-phase3-validation.md', 'docs/v11-phase4-validation.md', 'docs/v11-phase45-validation.md',
  'docs/v1.0.0-release-history.md',
]);
const stale = /unreleased\s+(?:v?1\.1|phase)|development\s+(?:version|branch)|video support is unreleased|(?:before|until) stable images are published|phase\s+[1-5](?:\.5)?\b|v?1\.1(?:\.0)?-(?:dev|alpha|beta|rc)\b|v1\.0\.0.{0,50}(?:ready for|waiting for).{0,30}(?:tag|release)|direct image PUTs|image bodies are untrusted|never proxies those image bodies|storage secrets, and image bodies|public gallery\/download,\s*video/i;

export function releaseLanguageErrors(file, text) {
  if (historicalDocs.has(file)) return text.includes('> Historical validation record; not current product or release status.') ? [] : ['Historical record needs its explicit status notice'];
  // Older changelog entries are historical. Future Unreleased must remain empty
  // for this readiness snapshot; version notes are checked separately by tests.
  if (file === 'CHANGELOG.md') text = text.split(/^## \[1\.0\.0\]/m)[0];
  return text.split('\n').flatMap((line, i) => stale.test(line) ? [`${i + 1}: stale current-product language`] : []);
}
