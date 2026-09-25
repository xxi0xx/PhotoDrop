# Gate 8 release preparation validation

Baseline: merged Gate 7 `6b7d046f16d8093246996f1f6587daa281089ceb` (PR #8). Branch:
`codex/gate-8-community-release`. First intended stable version: `v1.0.0`.
No stable tag, GitHub Release or production image is published by this task.

## Release implementation

Git SemVer tags are the authority. CLI metadata defaults to dev and accepts version/
commit through Go link flags. Release tag validation checks main ancestry and a
selected LICENSE, reuses CI/Immich, builds amd64/arm64, adds OCI labels and BuildKit
SBOM/provenance, verifies artifacts before stable aliases/GitHub Release. Prereleases
never move latest/major/minor. CI builds/runs both architectures without publishing.
Publish permissions are confined to contents-write/packages-write in the tag job.
See [release policy](releases.md).

## Local evidence (2026-09-23, America/Chicago)

Commands ran against the Gate 8 working tree. GitHub checks on the committed PR
remain the authoritative clean-runner evidence; no pending check is called a pass.

- Frontend: npm ci (0 audit vulnerabilities), check (0 errors/warnings), all 34
  tests and production build passed. Removed the private npm package's stale
  application-like version so Git tags remain the single release authority.
- CGO_ENABLED=0 go test ./..., go vet ./..., CGO-free production build and gofmt
  passed. Full Linux `go test -race -timeout 180s ./...` and `go vet ./...` passed
  in an ephemeral golang:1.26 container with the source mounted read-only.
- Six release tests passed: stable/prerelease tags, invalid tags, non-tag publish
  rejection, actual Git ancestry, constrained workflow credentials/action pins,
  missing-platform/attestation rejection and actual injected binary versions.
- Both linux/amd64 and linux/arm64 release-style images built and ran successfully
  (arm64 under Docker emulation). CLI reports `1.0.0-rc.test` and injected revision.
  OCI source/revision/version/title match. Each platform passed health, UID 10001,
  mode 0700 /data, absence of Go/Node/npm/gcc/source/test directories, and graceful
  SIGTERM/zero exit. The root entrypoint performs scoped directory preparation;
  the application remains non-root PID 1. No claim of native ARM hardware testing.
- Named multi-architecture OCI export passed digest/size checks, both platform
  metadata checks, SPDX SBOM and SLSA provenance/subject verification. The same
  artifact was pushed only to an ephemeral loopback registry, where the exact
  release manifest/SBOM/provenance inspection helpers passed. No GHCR publishing
  occurred. An unnamed initial export had empty subjects; the named dry-build
  path fixes this and retains strict subject validation. Provenance supports the
  current SLSA v1 as well as older BuildKit predicate representation.
- `node scripts/smoke-fresh.mjs`: PASS. Copies public files, fills an example env
  with an ephemeral password, and starts fresh Compose local and signed-fixture
  S3 deployments. Both passed health/login/event URL/contributor upload/export
  hash checks. No developer .env or previous data is used. Browser QR behavior is
  independently covered by the focused browser run and actual QR decoding tests.
- `node scripts/smoke-release.mjs`: PASS. Builds exact merged Gate 7 and the Gate 8
  candidate; creates local and S3 photos, one pending reservation, contributor names,
  quotas and backend records, and seeds completed Immich job/import metadata.
  Stops, copies the complete data volume, upgrades, verifies, destroys the disposable
  upgraded volume, restores into an empty volume, and verifies again. Both phases
  preserved old-session access/new login, events/public URLs, local/S3 export hashes,
  attribution, quotas, backend identity, ready/pending state, all nine migration
  checksum/history records and Immich metadata. Remote S3 objects remained outside
  the backup and unchanged. A harness correction restores the backed-up cookie,
  rather than a newer cookie created after backup. No runtime fix was necessary.
- `node scripts/smoke-fresh.mjs --compose-smoke` runs the unchanged
  `scripts/smoke-compose.sh` in a disposable public-file copy: PASS, including
  event/auth/local-upload regression, restart hash, non-root/minimal image, SIGTERM.
- `node scripts/smoke-backends.mjs`: PASS, local → S3-A → S3-B → local, historical
  refresh/finalization, missing credentials, mixed deletion and retry.
- Real Immich v3.2.1: Compose build and `node scripts/smoke-immich.mjs --browser`
  passed. Mixed local/S3-A/S3-B export hashes/manifest passed. Four-permission auth,
  album create/rename, incremental work, failed retry, lost-response deduplication,
  restart recovery (16 assets already accounted for), revoked credentials/outage
  health and independent deletion passed; all 27 Immich copies survived PhotoDrop
  deletion. Other Immich versions are not independently verified.
- Gate 7 browser regression passed on the unchanged production frontend using the
  existing test-only fixture: 320px admin/guest without overflow, square 208px QR,
  all six admin sections, contributor attribution, partial success, failed-only
  retry, add-more with another name, and direct S3. Three local selections used one
  grant; forced failure produced two ready photos, then retry sent exactly one
  additional body. Add-more produced one photo under a second label. Two direct
  photos used two S3 PUTs and no additional PhotoDrop media bodies. Final attribution:
  3 / 1 / 2 photos under the three supplied labels. URL remained unchanged.
  Browser console contained the intended injected 503 and the existing single-admin
  password-form username suggestion. Browser and fixture closed successfully.
- Local Markdown/configuration coverage check passed (26 documents, 60 local links
  and anchors). Actionlint v1.7.12 passed. New Cloudflare/Docker guidance was checked
  against official documentation. External links are not claimed as exhaustively crawled.
- Migration files 001–009 have no diff against baseline; the upgrade/restore test
  also compares persisted checksum/history rows byte-for-byte. No migration 010.

Docker Desktop was unavailable initially due an inaccessible runtime socket. It
subsequently recovered; Docker Engine 29.7.2 and Compose configuration now pass.
No operator data was deleted and no factory reset was performed. The initially
blocked container checks subsequently completed successfully as recorded above.

## Commands and release review

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts web/embed.go
CGO_ENABLED=0 go test ./...
go test -race -timeout 180s ./...  # Linux container
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop
node --test scripts/release.test.mjs
node scripts/check-docs.mjs
actionlint -shellcheck= -pyflakes=
docker compose --env-file .env.example config --quiet # ephemeral password via env
node scripts/smoke-fresh.mjs
node scripts/smoke-release.mjs
node scripts/smoke-fresh.mjs --compose-smoke
node scripts/smoke-backends.mjs
docker compose -f scripts/compose-immich-test.yml build
node scripts/smoke-immich.mjs --browser
git diff --exit-code 6b7d046 -- migrations
git diff --check
```

Release dry builds used VERSION=1.0.0-rc.test and the baseline revision as explicit
test inputs; they are not stable release identities. CI injects the exact commit
under test. Local OCI and registry helpers are `verify-oci.mjs`, `verify-manifest.mjs`,
`verify-attestations.mjs`; `verify-image.sh` runs both runtime platforms.

Focused review preserves strict Turnstile hostname/action checks, existing upload
recovery and private DTO/logging boundaries. No production debug route/verifier,
new media feature or integration was introduced. Frontend output has no source maps
or test-verifier markers. Runtime copies only the binary/entrypoint into Alpine;
no source, .git, .env, fixture server, compiler or credentials are copied. The build
context excludes private env variants, data, generated output and browser artifacts.
Data-directory preparation now enforces private mode 0700. Test credentials appear
only in intentional isolated fixtures, never production example values. Actions are
SHA-pinned, PR jobs are read-only, release writes are confined to the validated tag
publish job, and there is no pull_request_target. No automatic dependency merging.
The final credential-pattern scan found no matching private keys, GitHub/AWS tokens
or signed S3 URLs in release source/documentation. This is a bounded check, not proof
that every possible secret format is detectable. Disposable test containers/volumes,
local registry, OCI archives, browser session/data/screenshots, logs, temporary tools
and generated binary were removed; unrelated operator state was preserved.

## Gate 8 implementation-time owner decisions and blockers (2026-09-23)

The following was the state during implementation, before the owner completed
production validation. It is retained as history and superseded by the next section.

- No LICENSE exists. The owner must select a license; none is invented here.
- GitHub private vulnerability reporting is disabled (API checked 2026-09-23).
  Owner must enable/verify it or publish an intentional private contact route.
- **UNVERIFIED: live Cloudflare R2 interoperability**: no dedicated credentials/bucket.
- **UNVERIFIED: live production-key Turnstile flow**: no production keys/public staging hostname.
  Exact owner-run steps are in [live validation](live-validation.md); fixtures are not live proof.
- Confirm public GHCR package access when first publishing. Stable publication is
  permitted only after this PR is merged and readiness blockers are resolved.

This is focused release engineering/security review, not a formal exhaustive audit.
There are no post-v1 product features or database migrations in this gate.

## Post-merge owner validation — 2026-09-24 America/Chicago

Gate 8 PR #9 merged as `4fbfd3919e9ea61ff5a84b42d785131b5ba0a915`.
The owner subsequently supplied sanitized production outcomes; Codex did not hold
production credentials during Gate 8 and did not rerun these live tests.

| Readiness item | Current result |
| --- | --- |
| Software license | **RESOLVED** — owner-selected Apache-2.0; canonical root LICENSE and OCI license label |
| Private vulnerability reporting | **RESOLVED** — enabled, independently confirmed through the GitHub API; owner's SECURITY.md preserved |
| Live Cloudflare R2 validation | **PASSED** — real browser direct upload, CORS, completion, totals and scoped event deletion |
| Live production Turnstile validation | **PASSED** — real challenge and upload-session creation through strict production Siteverify hostname/action checks |

Reference deployment: `https://drop.mariascloud.com`, private bucket
`mariascloud-photodrop`. [Live validation](live-validation.md) records exact sanitized
observations, evidence limits and procedures for new deployments. No HAR, credentials,
signed query strings, cookies or challenge tokens are included. These results resolve
the four earlier owner blockers, without claiming provider certification.

The first stable version remains unpublished `v1.0.0`. After this focused PR is
merged, the owner still deliberately finalizes release notes and pushes the intended
tag; confirm public GHCR package visibility on first publication. No product behavior,
Turnstile semantics or migration is changed by this finalization.

Finalization checks (same date): npm ci/check/build and all 34 frontend tests passed;
CGO-free Go tests, Linux race tests and vet passed. All eight release tests passed,
including canonical Apache text and rejection of missing/wrong OCI license labels.
Both amd64 and emulated arm64 candidates passed OCI SBOM/provenance and runtime
checks, including Apache-2.0 metadata, packaged LICENSE hash, health, UID 10001 and
SIGTERM. Compose config, 26-document/68-link coverage and diff checks passed.
The credential-pattern review of Git-visible files found no leaked validation
credentials or HAR additions; it is not an exhaustive security audit. Application
code, migrations 001–009 and the owner's SECURITY.md are unchanged. PR CI provides
the independent clean-runner result; no live production tests were rerun.
