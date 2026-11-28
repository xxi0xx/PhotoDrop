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

## Phase 2 — Secrets keyring

Baseline: `main` after PR 39 (`f2929b7`). Branch: `multi-host/phase-2-secrets`. No
migration; nothing user-visible changes.

### Changes

- New `internal/secrets`: key files (32 bytes, standard or URL-safe base64, padded
  or not), key id = first 16 hex characters of SHA-256("photodrop-key-id" ‖ key),
  data key = HKDF-SHA256(key, no salt, `photodrop/destination-credential/v1`),
  AES-256-GCM with a fresh 12-byte nonce, and the plan's AAD layout. `Keyring`
  redacts `String`/`GoString`; `ErrUnknownKey` is the typed "cannot be decrypted"
  error.
- Config: `PHOTODROP_SECRETS_KEY_FILE` and `PHOTODROP_SECRETS_PREVIOUS_KEY_FILES`
  (paths only). Server startup loads the keyring: no key logs that linking is
  unavailable; a malformed, missing or (on Linux) group/other-accessible key file
  stops startup.
- New `internal/destinations` with `Rotate`, and the `photodrop rotate-secrets`
  command. It checks that every stored credential decrypts before changing any,
  re-encrypts one row per statement under the active key, leaves rows that changed
  during the run alone, and prints counts only. With no `destination_accounts`
  table yet (migration 014 is phase 3) it reports zero rows.
- Docs: configuration (new section and Compose secret example), security (stored
  credentials), backup/restore (back the key up separately), architecture,
  upgrading, `.env.example` and `compose.yml`.

### Local evidence (2026-11-27, America/Chicago)

- Known-answer vectors for the key id and a ciphertext were computed independently
  with Node.js `crypto` (`hkdfSync`, `aes-256-gcm`) and pinned in
  `TestKnownAnswer`.
- Startup cases in the real image (`photodrop:phase2-check`, as uid 10001):

  | Case | Result |
  | --- | --- |
  | No key | starts; logs "secrets key not configured; Immich account linking is unavailable" |
  | Valid key, mode 0400 | starts; logs "secrets key loaded" |
  | Key mode 0644 | exit 1: "…is accessible to group or other (mode -rw-r--r--); run chmod 400 on it" |
  | Key mode 0440 | exit 1, same check |
  | Malformed key | exit 1: "…must contain 32 random bytes in base64; generate one with: openssl rand -base64 32" |
  | Previous key without active key | exit 1 at configuration |
  | `rotate-secrets` with a key | "Linked credentials: 0. Re-encrypted: 0. …" |
  | `rotate-secrets` without a key | error "secrets key not configured" |

  Error messages name the file but never its contents, and the key never appears
  under `/data`.
- Linux (`golang:1.27` container): `TestOverPermissiveKeyFileRejected` and
  `TestLoadKeyFiles` passed, and the full `go test -race ./...` passed in all 14
  packages.
- Windows: CGO-free `go test ./...`, `go vet ./...`, the build, `gofmt -l`,
  frontend check and tests, release tests, `check-docs.mjs` and
  `git diff --check` passed. `TestOverPermissiveKeyFileRejected` skips on Windows
  by design and ran on Linux above.

### Security tests (plan, Testing section)

| Plan requirement | Test |
| --- | --- |
| Round trip; wrong AAD, tampered ciphertext or wrong nonce fails | `TestRoundTripAndTampering` |
| A previous key decrypts; encryption uses the active key | `TestPreviousKeysDecryptOnly` |
| Unknown key id gives a typed error | `TestRoundTripAndTampering` (`ErrUnknownKey`) |
| Malformed key files rejected | `TestLoadKeyFiles`, `TestStartupSecretsKeyFile` |
| Over-permissive key files rejected | `TestOverPermissiveKeyFileRejected` (Linux) |
| `String()` redacts | `TestKeyringRedactsItself` |
| Derivation and AAD are stable | `TestKnownAnswer`, `TestAADRejectsAmbiguousFields` |
| Rotation: zero rows, re-encrypts, refuses without changes, concurrent change | `TestRotateWithoutAccountsTableOrRows`, `TestRotateReencryptsUnderTheActiveKey`, `TestRotateRefusesWithoutChangingAnything`, `TestRotateLeavesRowsChangedDuringTheRun`, `TestRotateSecretsCommand` |
| Configuration | `TestSecretsKeyFileConfiguration` |

Gate: the secrets unit tests pass, and startup with no key, a bad key and an
over-permissive key file behaves as specified (met locally); CI pending the PR's
checks.
