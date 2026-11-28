# Multi-host accounts validation

> Historical validation record; not current product or release status.

Evidence for the multi-host accounts and linked Immich destinations work, one
section per phase. Each section lists the commands run, their results and the
versions involved. GitHub checks on the merged PR remain the authoritative
clean-runner evidence; no pending check is called a pass.

## Phase 0 — Immich 3.4.2 baseline

Baseline: `main` at release 1.2.1 (`5a63599`). Branch:
`multi-host/phase-0-immich-3.4.2`. No Go behaviour changes: the only Go edit is the
version wording in the connection-validation error.

### Changes

- `scripts/compose-immich-test.yml` pins `ghcr.io/immich-app/immich-server:v3.4.2`.
  The Postgres and Valkey images are copied from Immich's own
  `docker/docker-compose.yml` at tag `v3.4.2`: Postgres is unchanged
  (`14-vectorchord0.4.3-pgvectors0.2.0@sha256:bcf63357…`), Valkey moves to
  `valkey:9@sha256:418652cf…`.
- The validation error says "validated with 3.4.2". The importer still accepts any
  3.x version.
- Current docs (README, export and Immich, troubleshooting) name v3.4.2 and link
  v3.4.2 sources. The `/albums/{albumId}` web route and the API-key controller were
  checked to exist at that tag. Historical validation records, past changelog
  entries and the video fixture notes (generated once with the v3.2.1 image) are
  unchanged.
- New contract guard `scripts/immich-contract.mjs` with the snapshot
  `scripts/immich-contract.v3.4.2.json`, run by the Immich workflow before the real
  suite.

### Local evidence (2026-11-27, America/Chicago)

Windows 11, Docker Engine 29.9.2, Compose 5.6.1, Go 1.27.0, Node 22.23.2.
Immich server image `ghcr.io/immich-app/immich-server@sha256:fc880c1bf665b4aab1e74c15e56ab871225989cd546536e6b77e771869af06bd`
(label version `v3.4.2`).

- `node scripts/smoke-immich.mjs` against v3.4.2 passed, ending with "Real Immich
  Gate 6 integration suite passed." This covers early/empty provisioning, outage
  isolation, manual retry, rename, lost album response and restart without duplicate
  albums, automatic S3 PNG/MP4/MOV and local imports with hashes, burst coalescing,
  mixed export hashes, auth with the four permissions, partial failure and response
  loss recovered by Immich deduplication, abrupt restart recovery, health with
  revoked or missing credentials, and independent deletion. The first attempt
  stopped at an upstream registry rate limit while pulling images; the retried pull
  and the full suite then passed.
- `node scripts/immich-contract.mjs --check --tag v3.4.2` downloaded the v3.4.2
  spec and matched the snapshot (13 operations).
- `node scripts/immich-contract.mjs --check --tag v3.2.1 --snapshot
  scripts/immich-contract.v3.4.2.json` reported exactly one difference:
  `GET /server/version` gains `featureLevel` in v3.4.2. This matches the plan's
  contract diff, and shows the guard detects a change.
- Frontend: `npm ci`, `npm run check` (0 errors, 0 warnings), `npm test` (46 passed)
  and `npm run build` passed. `npm ci` reports one existing high-severity audit
  finding; this branch changes no dependencies.
- `gofmt -l` printed nothing. CGO-free `go test ./...`, `go vet ./...` and the
  production build passed. Race tests need Linux and run in CI.
- `node --test scripts/release.test.mjs` passed (10). `node --test
  scripts/release-recovery.test.mjs` passed (10) on rerun; one earlier run failed
  "real Git fetch resolves annotated and lightweight remote tags" while the Docker
  build was loading the machine. The same test passed alone on this branch and on
  `main`; this branch does not touch release recovery.
- `node scripts/check-docs.mjs` and `git diff --check` passed.

Gate: `smoke-immich.mjs` passes against v3.4.2 with no Go behaviour changes (met
locally); the spec guard passes in CI (pending the PR's checks).
