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

## Phase 1 — Hosts and event ownership

Baseline: `main` after PR 38 (`eed1ff3`). Branch: `multi-host/phase-1-hosts`.

### Changes

- Migration `013_hosts.sql`: `hosts` (host 1 = the existing administrator,
  `password_login = 1`), `host_sessions` replacing `admin_sessions` (sessions reset),
  `events.owner_host_id` backfilled to host 1 with NOT NULL enforced by triggers.
  It adds one index beyond the plan's SQL: `host_sessions_host(host_id)`.
- Auth: sessions carry `host {id, role, display_name}`; `OIDC.Complete` returns an
  `Identity` with the role from the four lists (admin wins); `ResolveHost` claims
  host 1 on the first admin sign-in, refreshes known hosts and denies disabled ones.
  Password sign-in always uses host 1; a password change revokes only host 1's
  sessions. New `PHOTODROP_OIDC_HOST_SUBJECTS` / `_HOST_GROUPS`.
- Events: owner set on create; `List` / `Get` / `Update` take a scope; admin-only
  `TransferOwner` with the plan's rules.
- Server: one route table for every `/api/admin/events/{id}*` route, each wrapped in
  an ownership check that answers `404`; `GET /api/admin/hosts`,
  `PUT /api/admin/hosts/{id}`, `PUT /api/admin/events/{id}/owner` (admin-only, `403`
  for hosts); `GET /api/admin/session` includes `host`.
- Web: signed-in name in the navigation, Hosts page for admins, owner label in the
  admin event list, and an owner selector on the event page.
- Docs: configuration, OIDC, security, architecture, upgrading, README,
  `.env.example` and `compose.yml`.

### Local evidence (2026-11-27, America/Chicago)

Windows 11, Docker Engine 29.9.2, Compose 5.6.1, Go 1.27.0, Node 22.23.2.

- `node scripts/smoke-oidc.mjs` passed. New checks: the first admin OIDC sign-in
  claims host 1; a `photodrop-hosts` identity signs in as a host, sees no other
  host's events, gets `404` for the admin's event and `403` for the host list, and
  owns the event it creates; with both groups the admin role wins; after removal
  from every group the next sign-in is refused; the admin sees every host's events.
- `node scripts/smoke-release.mjs` passed (upgrade and destructive restore from the
  merged migration-012 baseline). Updated expectations: the pre-upgrade session is
  rejected (`401`) and a fresh password sign-in is host 1 as admin; existing events
  report `owner {id: 1, display_name: "Administrator"}` and `owner_host_id = 1`;
  every other table, hash, quota and Immich binding is unchanged.
- `node scripts/smoke-immich.mjs` against Immich v3.4.2 passed unchanged ("Real Immich
  Gate 6 integration suite passed."): existing bindings, jobs and auto-import work
  with events owned by host 1.
- Browser check on a local build with the disposable OIDC fixture: admin and host
  sign-ins, "Signed in as" name, host sees only "Your events" and no Hosts link,
  "Event not found" for the admin's event, admin sees "All events" with host
  labels, Hosts page, and moving an event to another host. Two spacing glitches on
  the Hosts page and a stale owner selector after a move were fixed.
- CGO-free `go test ./...`, `go vet ./...`, the production build and `gofmt -l`
  passed; frontend check (0 errors, 0 warnings), 46 tests and build passed;
  `release.test.mjs` (10) and `release-recovery.test.mjs` (10) passed;
  `check-docs.mjs` and `git diff --check` passed. `release-recovery.test.mjs` failed
  its real-Git-fetch test once while Docker builds loaded the machine and passed on
  rerun, as in phase 0. Race tests run in CI.

### Security tests (plan, Testing section)

| Plan requirement | Test |
| --- | --- |
| Migration: host 1, owners backfilled, sessions reset, triggers reject NULL owner | `TestMultiHostUpgradeAssignsEventsToHostOne` |
| Host resolution: claim host 1 once, new host, role updated on login | `TestResolveHostClaimsHostOneOnceThenCreatesHosts` |
| Disabled host denied; disabling deletes sessions; password change revokes only host 1 | `TestHostSessionsDisableAndPasswordRotation` |
| Admin wins; no list match denied; profile claims | `TestOIDCRoleDerivationAdminWinsAndProfileClaims` |
| Every event route: another host gets `404`, owner and admin get access | `TestEventRoutesAreScopedToTheOwnerHost` (mutation-checked: removing the wrapper fails it) |
| Ownership transfer rules | `TestOwnershipTransferRules` |
| Admin-only host routes, self-disable refused, removed user denied at next login | `TestHostAdministrationAndRemovedUsers` |
| Store scoping | `TestScopeLimitsHostsToTheirOwnEvents` |
| Four role lists in configuration | `TestOIDCConfiguration` |

Gate: the authorization route table test, migration test, `smoke-oidc.mjs` and
`smoke-release.mjs` pass (met locally); CI pending the PR's checks.
