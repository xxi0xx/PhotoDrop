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

## Phase 3 — Destination accounts

Baseline: `main` after PR 40 (`b3e7692`). Branch: `multi-host/phase-3-accounts`.

### Changes

- Migration `014_destination_accounts.sql`: `destination_accounts`, link
  transactions, and `immich_event_imports.account_id` with the plan's triggers.
  Config accounts (host 1) are created only for targets that have bindings, as
  revised in the plan; every existing binding is backfilled.
- `immich.Reconcile` applies the config-account rules: a configured key creates
  host 1's account once; a URL-only target is link-only; a removed key leaves the
  row unchanged and unusable; a key where host 1 has linked is ignored with a fixed
  warning.
- Every Immich call resolves its client from an account: the worker from the
  binding's `account_id`; `Test`, provisioning and new jobs from the binding's
  account or else the event owner's live account (invariant 3). No owner account
  is `422 immich_no_account`. The default target never falls back.
- Target lists (`GET /api/admin/immich/targets`, `GET …/events/{id}/immich`) show
  only targets where the caller (or the event owner) has a live account, each with
  `account {id, provider, source, remote_email, status}`. `active_target` appears
  only when usable.
- `internal/destinations`: the `Provider` interface and account store. Immich's
  provider validates credentials (a rejected key is a status, a transport or 5xx
  failure an error); linking reports "not available" until phase 4.
- `rotate-secrets` now always uses the real table; its "no table yet" branch is
  gone.
- Web: the empty Immich section explains that the event's host has no account.
- Docs: export and Immich (whose account an event uses), configuration,
  architecture, security and upgrading.

### Local evidence (2026-11-27, America/Chicago)

- `node scripts/smoke-release.mjs` passed (upgrade and destructive restore from the
  merged migration-012 baseline). It now also asserts the backfill: one active
  config account for host 1 on the bound target, no stored credential, and the
  existing binding's `account_id` pointing at it; every other table is unchanged.
- `node scripts/smoke-immich.mjs` against Immich v3.4.2 passed unchanged ("Real Immich
  Gate 6 integration suite passed."): provisioning, manual and automatic import,
  retries, restart recovery and outage handling now run through the config account.
- Linux (`golang:1.27` container): `go test -race ./...` passed in all 14 packages.
- Windows: CGO-free `go test ./...`, `go vet ./...`, `gofmt -l`, frontend check
  (0 errors, 0 warnings), 46 frontend tests and build, and `check-docs.mjs` passed.
- A deadlock found during development: resolving an account through `s.db` inside
  the binding transaction waits forever on SQLite's single connection. The account
  lookup now uses the caller's transaction, and the Immich package tests (which
  hung before the fix) pass.

### Security tests (plan, Testing section)

| Plan requirement | Test |
| --- | --- |
| Migration: one config account per target with bindings, none without; every `account_id` backfilled; triggers reject a missing account, an account on the wrong target, and an account change once `album_state <> 'new'` | `TestDestinationAccountsUpgrade` |
| Reconcile rules (configured key, link-only, missing key, ignored key) | `TestReconcileConfigAccounts` |
| `immich_no_account`; operations use the event owner's account even when an admin acts; no default-target fallback | `TestImmichUsesTheEventOwnersAccount`, `TestEventProvisioningAPI` |
| Provider validation: `401`/permissions → status, transport/`5xx` → error | `TestProviderValidatesConfigAccounts` |
| Existing worker, retry, auto-import and recovery behaviour through config accounts | the existing `internal/integrations/immich` suite, unchanged apart from fixtures |

Gate: `smoke-release.mjs` (existing bindings and auto-import survive the upgrade)
and `smoke-immich.mjs` pass unchanged.

## Phase 4 — Linking

Baseline: the phase 3 branch (`multi-host/phase-3-accounts`, PR 41). Branch:
`multi-host/phase-4-linking`.

### VERIFY results (before any link code)

Run first against real Immich v3.4.2 with OAuth through the test identity
provider (`node scripts/smoke-immich-link.mjs`, VERIFY section). Both passed on the
first run, so the link flow in the plan stands unchanged:

- **VERIFY 1:** the session from `POST /api/oauth/callback` authenticates
  server-to-server as `Authorization: Bearer` (`GET /users/me` returned the user,
  with `oauthId` equal to the identity subject).
- **VERIFY 2:** the identity provider saw a token exchange whose `redirect_uri`
  equalled the callback URL PhotoDrop submitted, using `client_secret_post`.
  Submitting a different URL changed the `redirect_uri` Immich sent, and the
  exchange was refused.
- Also confirmed: the session can mint and delete a 4-permission key, the key's
  `GET /api-keys/me` returns its id, and after `POST /auth/logout` the session gets
  `401`.

Immich source read for this (v3.4.2): `oauth.repository.ts` (openid-client
`authorizationCodeGrant` with the submitted URL; no `nonce`; PKCE when
advertised), `auth.service.ts` (body `state`/`codeVerifier` override cookies; user
found by `oauthId`, then email; Bearer token accepted), and the OAuth config
defaults (`client_secret_post`).

### Changes

- Owner decisions: a host's email is stored only when `email_verified` is true;
  host 1 stays admin while password sign-in is enabled.
- Test stack: `immich-server` and the test identity provider share the `objects`
  network namespace; Immich OAuth is configured (`autoRegister: false`); PhotoDrop
  runs `password+oidc` with host groups and `PHOTODROP_BASE_URL`; a test-only
  entrypoint writes the per-run secrets key with owner 10001 and mode 400. The
  OIDC fixture gained several clients with exact redirect allowlists,
  `client_secret_post`, optional nonce, a userinfo endpoint and an exchange log.
- Link flow: `GET /api/admin/destinations`, `POST …/immich/link` (link or relink),
  `GET …/immich/callback`, `POST …/{id}/unlink`, `POST …/{id}/forget`. Browser-bound,
  session-bound, five-minute transactions in `destination_link_transactions`.
  Immich's authorize URL must echo state, redirect URI and PKCE challenge. The
  session is logged out on every exit path. Identity by subject or verified email
  (`PHOTODROP_IMMICH_<KEY>_LINK_MATCH`), with the `link_subject_mismatch`
  diagnostic. Exactly four permissions, self-checked; stored with AES-GCM and AAD;
  anything created remotely is deleted on a later failure. `SupportsLink`
  requires 3.4.2 or later.
- Ongoing validation: before each job and connection test, and once at startup in
  the background, a linked key must still be the stored key with the four
  permissions; a rejected or replaced key marks the account `needs_relink`;
  network errors change nothing.
- Web: Account page (`/admin/account`) with Link, Relink, Unlink and Forget and
  one fixed message per result code; the Immich section points hosts there.
- Docs: export and Immich (linking), OIDC (Immich redirect URI, subject mode),
  configuration (`LINK_MATCH`), security, architecture and upgrading.

### Local evidence (2026-11-28, America/Chicago)

- `node scripts/smoke-immich-link.mjs` passed (VERIFY plus cases 2–7): one named
  4-permission key per link; the host's album (owner in `albumUsers`) and imported
  photo are in the host's Immich library, not the admin's; the link session is not
  among the host's Immich sessions afterwards; a different identity at the Immich
  step gets `link_wrong_user` and no key; relink leaves one key and unlink none;
  with no Immich user, `link_failed` and no user, key or account is created; a
  subject-mode mismatch gets `link_subject_mismatch`; PhotoDrop's logs contain no
  key, session or client secret.
- `node scripts/smoke-immich.mjs` passed through the changed test stack.
- `node scripts/smoke-oidc.mjs` passed.
- Browser check (in-app browser against the test stack): a host signed in through
  SSO, opened Account, clicked **Link Immich**, went through Immich and the
  identity provider, and came back to "Your Immich account is linked" (linked as
  the host's email, Relink and Unlink shown). Unlink asked for confirmation and
  ended with "Your Immich account is unlinked, and PhotoDrop deleted its API key in
  Immich".
- Go: CGO-free `go test ./...`, `go vet ./...` and `gofmt -l` passed. The logout
  guarantee was mutation-checked: making `CompleteLink` skip the deferred logout
  fails every link test. Linux (`golang:1.27` container): `go test -race ./...` passed
  in all 14 packages.
- Frontend: check (0 errors, 0 warnings), 48 tests and build passed.
  `check-docs.mjs`, `release.test.mjs` and `git diff --check` passed.

### Security tests (plan, Testing section)

| Plan requirement | Test |
| --- | --- |
| Authorize-URL mismatches (state, redirect_uri, code_challenge); old server | `TestLinkStartValidatesImmichAndTheTarget` |
| Duplicate query parameters; expired, replayed, wrong-browser and wrong-session transactions; provider error | `TestLinkCallbackRejectsReplayedOrForeignTransactions` |
| Wrong user by subject and by email; subject mismatch; email mode | `TestLinkRefusesWrongPeopleAndBadResponses` |
| Account already in use; unique-index conflict at store time (key deleted) | `TestLinkConflictsAndForget` |
| Key permissions mismatch and self-check mismatch (key deleted) | `TestLinkRefusesWrongPeopleAndBadResponses` |
| Logout attempted on every exit path (counted) | every case of `TestLinkRefusesWrongPeopleAndBadResponses`, `TestLinkStoresAnEncryptedScopedKeyAndLogsOut` |
| Oversized responses | `TestLinkRefusesWrongPeopleAndBadResponses` (oversized user) |
| Relink deletes the old key; unlink treats `404` as done | `TestLinkStoresAnEncryptedScopedKeyAndLogsOut`, `TestLinkConflictsAndForget` |
| No secrets in output (logs, responses, database file) | `TestLinkStoresAnEncryptedScopedKeyAndLogsOut`, smoke log check |
| Validation: `401` → `needs_relink`; `5xx` → no state change; relink restores | `TestLinkedAccountValidation` |
| Version gate | `TestSupportsLink` |
| Verified email and host 1 role | `TestOIDCRoleDerivationAdminWinsAndProfileClaims`, `TestHostOneKeepsAdminWhilePasswordSignInIsEnabled` |
| Real-server cases 1–7 | `scripts/smoke-immich-link.mjs` |

Gate: VERIFY 1–2, every link-flow unit test, smoke cases 1–7 and a browser run of
the Account page (met locally); CI pending the PR's checks.
