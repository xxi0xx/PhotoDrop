# v1.1 Phase 4 validation: native OIDC

> Historical validation record; not current product or release status.

Validated September 27, 2026, from merged Phase 3
`e5a97e49bdadb740389f01cade7a88065eb57f70` on
`codex/v11-oidc-admin-auth`. Implementation checkpoint `e42dc37` was pushed after
the full Linux race suite and repeated targeted auth/migration tests passed.
Subsequent changes add transport regression coverage and this record.

## Authentication and focused review

- Unset `PHOTODROP_ADMIN_AUTH` remains password-only. OIDC-only starts without a
  password and rejects password login even when a historical credential exists.
  Combined mode keeps password sign-in available during provider outages.
- Startup constructs the OIDC service without discovery, JWKS or other provider
  traffic. Discovery happens on login and caches success only. Existing local
  sessions, health, public pages, guest grants and uploads do not call the IdP.
- Both methods use the same session insertion, previous-token revocation, random
  32-byte bearer, SHA-256 storage, 12-hour expiry and synchronizer CSRF mechanism.
  Removing password coupling from lookup preserves Phase 3 sessions. Password
  rotation still deletes all sessions, and the transactional credential-hash guard
  prevents an old password manager from issuing sessions after rotation.
- Independent random state, nonce, browser binding and PKCE verifier; S256 code
  challenge; original verifier at exchange. Callback atomically consumes unexpired
  browser-bound state before exchange. Concurrent/replayed callbacks cannot issue
  another session. Binding cookies are HttpOnly, SameSite=Lax, five-minute, scoped
  to `/api/admin/oidc`, Secure with HTTPS, and cleared after callback.
- Maintained libraries verify discovery issuer, JWT signature/JWKS, audience and
  expiry. PhotoDrop also checks exact token issuer, nonce, nonempty subject and
  authorized party (`azp` when present; required for multiple audiences).
  Exact allowed subject **OR** exact allowed group authorizes. Malformed groups
  fail closed when group authorization is configured, including a mixed array
  containing null/numeric values. At least one allowlist is required. Email,
  username and display name are never authorization boundaries.
- Callback comes only from configured BASE_URL. Host/forwarded headers and
  `return_to` cannot change it. Path-bearing issuers retain exact spelling.
  HTTPS is required except narrow loopback HTTP development. Provider redirects
  cannot forward confidential requests; response reads and time are bounded.
- Failure URLs/logs are generic. Provider access/refresh/ID tokens, codes, claims
  and cookies are not persisted or logged. Config formatting redacts credentials;
  public methods exposes only two booleans. Authenticated mutations retain
  same-origin and CSRF checks. Rate/concurrency limits and transaction admission
  bounds are separate from password and guest limits.
- Fixture signing/fault injection exists only in test packages/executables, with
  no production fake-auth switch. Public/upload/Turnstile handlers were unchanged.
  This is an implementation-focused review, not a claim of a formal external audit.

## Dependencies and migration

Three direct Go module additions, pinned normally in `go.mod`/`go.sum`:

| Module | Version | Purpose |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | `v3.20.0` | Discovery, ID-token validation, JWKS cache/rotation |
| `golang.org/x/oauth2` | `v0.37.0` | Confidential authorization-code exchange and PKCE helpers |
| `github.com/go-jose/go-jose/v4` | `v4.1.4` | OIDC's JOSE dependency; also directly imported by the signed test fixture |

No frontend OIDC SDK, dependency refresh or custom JWT cryptography was added.
`go mod verify` passed. `govulncheck` reported zero reachable vulnerabilities and
zero vulnerable imported packages. It reported the existing unused
`golang.org/x/crypto/openpgp` module advisory GO-2026-5932; PhotoDrop does not import
that package. CGO-free production builds remain supported.

Migration `011_oidc_transactions.sql` adds only:

```sql
CREATE TABLE oidc_transactions (
    state_hash TEXT PRIMARY KEY,
    nonce_hash TEXT NOT NULL,
    pkce_verifier TEXT NOT NULL,
    browser_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX oidc_transactions_expiry ON oidc_transactions(expires_at);
```

Migrations 001–010 and `admin_sessions` schema are unchanged. Transactions expire
after five minutes; initiation deletes expired rows in bounded batches and
replaces the prior browser transaction. A 512-row admission limit prevents
unbounded live transaction growth. Expiry is enforced even before cleanup.

## Commands and results

All commands passed; integration scripts used disposable state, never `.env` or
the deployment's data directory. Windows ran Go 1.27.0 and Node 24.21.0; Linux
Docker supplied the C toolchain for race checks.

| Validation | Result |
| --- | --- |
| `npm ci --prefix web` | Locked install passed |
| `npm run check --prefix web` | Svelte/TypeScript check passed, warnings treated as failures |
| `npm test --prefix web` | 39 tests passed |
| `npm run build --prefix web` | Embedded production frontend passed |
| `gofmt -l cmd internal migrations scripts web/embed.go` | No formatting changes required |
| `CGO_ENABLED=0 go test ./...` | Full suite passed |
| `go test -race ./...` in Linux Docker | Full suite passed after the test-only timing correction below |
| `go vet ./...`, `go mod verify` | Passed |
| `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop` | Passed |
| Repeated `go test -count=1 ./internal/auth ./internal/config ./internal/server ./internal/database` | Passed |
| Provider redirect/oversized-response test, also under race | Passed |
| `node --test scripts/release.test.mjs scripts/release-recovery.test.mjs` | 18 passed; no workflow dispatch |
| `node scripts/check-docs.mjs` | Local links/anchors and environment reference coverage passed |
| Actionlint, `docker compose --env-file .env.example config --quiet`, `git diff --check` | Passed |
| `node scripts/smoke-fresh.mjs --compose-smoke` | Standard Compose smoke in a disposable checkout: persistence, non-root and SIGTERM passed |
| `node scripts/smoke-fresh.mjs` | Fresh local and S3 mixed-media uploads, contributor names, event URLs, export hashes passed |
| `node scripts/smoke-backends.mjs` | local → S3-A → S3-B → local, historical refresh/finalization, mixed deletion, missing credentials/retry passed |
| `node scripts/smoke-release.mjs` | Exact populated Phase 3 upgrade and stopped backup/destructive restore passed |
| `node scripts/smoke-oidc.mjs --browser` | Signed protocol, browser binding/replay, restart, provider-outage guest upload, password fallback and logout passed |
| Build `scripts/compose-immich-test.yml`; `node scripts/smoke-immich.mjs` | Real disposable Immich v3.2.1 provisioning, manual and automatic import suite passed |
| `scripts/verify-image.sh` for local amd64 and arm64 builds | Version/labels, health, UID 10001, private `/data`, minimal runtime and SIGTERM passed |
| Local OCI export with `--sbom=true --provenance=mode=max`; `scripts/verify-oci.mjs` for both architectures | Digest integrity, SPDX SBOM and SLSA provenance passed; nothing published |

The first Linux race run exposed an existing test's ten-second context covering
the complete 100-file Immich import. The correction keeps a ten-second wait for
entry into the explicit API gate, then lets the synchronized import finish under
the test lifetime. Asset counts, frozen selection, late arrivals and one coalesced
follow-up remain asserted; production Immich code is unchanged. The full rerun
passed without a reported data race.

## Integration evidence

The signed OIDC fixture exercises real RSA signatures, discovery, code exchange,
PKCE, JWKS rotation and invalid issuer/audience/signature/expiry/nonce/subject/group
rejection. Tests cover unavailable discovery recovery, restart during a login,
short-lived state, browser binding, concurrent replay and token non-persistence.
HTTP tests preserve cookie/CSRF/origin/session-fixation protections and prove
provider-outage isolation for guest and administrator operations.

Browser validation used the disposable fixture at loopback port 8085: password-only
form, combined SSO/password layout, OIDC-only without a password field, successful
SSO through the signed provider, generic error after an invalid nonce, local logout,
and event administration while the provider was unavailable. QR and contributor
summary remained visible. The test browser and disposable services were cleaned up.

Upgrade/restore retained event/public URL, local and external S3 byte hashes,
contributor names, quotas, backend identities, pending/ready counts, completed
Immich metadata and original migration checksums/timestamps. Existing local
session works after exact Phase 3 upgrade, then combined and password-free OIDC
restarts; password endpoint behavior follows each mode. Export hashes match before
and after restore. S3 objects remain external to the PhotoDrop backup.

Real Immich validation includes early/empty album provisioning, outage isolation,
manual retry/import, rename, lost-response and restart deduplication, automatic
S3 PNG/MP4/MOV import, hashes, disable/enable backlog, burst coalescing, restart
recovery and deletion retaining all 27 remote copies. The automated reference
remains v3.2.1; no owner Immich or Authentik installation was contacted.

## Limits and release boundary

Authentik setup is based on its official documentation and a signed generic OIDC
fixture, not a live Authentik-version certification. Owners should verify their
per-provider issuer, strict redirect, scope mappings and ID-token groups in staging.
Authorization is evaluated at login; group/policy changes do not revoke existing
12-hour local sessions. Logout is local-only. One PhotoDrop process/data directory
and the existing single administrator role remain the supported model.

No release/tag, GHCR publication, registry alias movement, recovery-workflow
invocation or production deployment was performed. No multiple-admin/user system
was added. The Phase 4 PR is intentionally left unmerged. Branch and PR workflow
links/status are recorded in the PR rather than treating local checks as CI proof.
