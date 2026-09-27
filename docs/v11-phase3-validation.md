# v1.1 Phase 3 validation

Validated 2026-09-27 on `codex/v11-immich-auto-import`, based on merged Phase 2
`73b7eee9c7c252dedf8a441a38971c144403c247`. This is unreleased development work.

## Behavior and integrity

- Migration 010 adds `immich_event_imports.auto_import INTEGER NOT NULL DEFAULT 0
  CHECK (auto_import IN (0,1))` and `immich_auto_bindings` on binding ID where
  `auto_import=1 AND album_state='ready'`. Existing and new bindings default off.
  Migrations 001–009 and the unique partial `integration_jobs_active` index are unchanged.
- Event creation accepts optional `immich.auto_import`. Existing bindings use
  authenticated, Origin/CSRF-protected `PUT .../immich/auto-import`; status returns
  the persisted boolean. Both UI checkboxes reflect opt-in policy.
- Bounded reconciliation selects only ready assets without an import row. Selection
  and one binding-level job commit atomically. Startup, periodic and post-job scans
  discover uploads without a wake; queued/running jobs retain their original selection.
- Failed selected rows require manual Retry, including validation failures before
  any transfer. Album provisioning selects zero assets and never loops on failure.
  Turning policy off does not cancel queued work or remove either copy/history.
- BMFF classification inspects at most 512 bytes. Known total size distinguishes
  malformed exact-512-byte EOF from the same prefix of a valid larger object.
  Local finalization uses counted streamed size; S3 uses verified HEAD size.

## Local validation results

All commands below passed against the final implementation. Windows host tooling
used Node 24.21.0 / Go 1.27.0; Linux race/vet used `golang:1.27` (Go 1.27.1).

| Check | Result |
| --- | --- |
| `npm ci --prefix web`, `npm run check --prefix web` | Locked install; zero Svelte errors/warnings |
| `npm test --prefix web`, `npm run build --prefix web` | 38 tests; production embed build |
| `gofmt -l cmd internal migrations scripts web/embed.go` | No output |
| `CGO_ENABLED=0 go test ./...`, `go vet ./...`, `go mod verify` | Pass |
| Linux `go test -race ./...` and `go vet ./...` | Pass |
| `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop` | Pass |
| `node --test scripts/release.test.mjs scripts/release-recovery.test.mjs` | 18 tests; local/mock tests only, no recovery workflow invoked |
| `node scripts/check-docs.mjs`, Actionlint | Links/anchors/environment coverage and workflow syntax pass |
| `docker compose --env-file .env.example config --quiet` | Pass with disposable password |
| `node scripts/smoke-fresh.mjs --compose-smoke` | Existing Compose smoke in isolated fresh checkout; health, persistence, UID, SIGTERM |
| `node scripts/smoke-fresh.mjs` | Fresh local and deterministic S3 mixed-media uploads; contributor/export hashes |
| `node scripts/smoke-backends.mjs` | Local → S3-A → S3-B → local; historical finalization, refresh, deletion, missing credentials/retry |
| `docker compose -f scripts/compose-immich-test.yml build`, `node scripts/smoke-immich.mjs` | Complete real disposable Immich 3.2.1 suite |
| `node scripts/smoke-release.mjs` | Populated exact Phase 2 baseline → candidate upgrade, stopped backup, destructive disposable restore |
| Local amd64/arm64 builds + `scripts/verify-image.sh` | Version/OCI labels, UID 10001, private `/data`, minimal runtime, health, graceful SIGTERM |
| Local multi-arch OCI build (`--sbom=true --provenance=mode=max`) + `scripts/verify-oci.mjs` | Both child architectures, digest integrity, SPDX SBOM and SLSA provenance verified |
| `git diff --check`, old-migration/dependency/release-workflow comparison | Pass; no dependency or publishing changes |

Targeted tests cover bursts of 1/10/100, concurrent reconciliation, queued and
running arrivals, completion-boundary/lost wakes, restart and no-wake periodic
discovery, transactional rollback, page/selection bounds, independent targets,
disabled policy, cancellation, failed provisioning, and manual retry after
validation/upload/assignment failures. Standard and extended box headers for MP4
and MOV cover exact EOF, identical larger prefixes, local known/unknown length,
and S3 rejection/cleanup. Existing format/brand tests remain green.

Real Immich 3.2.1 tests use gates and state predicates for new race scenarios:
zero-selection provisioning with waiting S3 PNG/MP4/MOV, original-byte readback,
10+2 burst follow-up, enable/disable backlog, local uploads, SIGKILL recovery,
partial failure without automatic retry, explicit Retry, outage-independent guest
completion and health, and PhotoDrop deletion preserving all 22 automatic copies.
The existing manual lifecycle also passes, preserving its 27 independent copies.
The owner's earlier Phase 1/2 Immich 3.2.2 validation is not a Phase 3 test claim.

Browser validation on the isolated fixture confirmed new-event policy defaults
off, opted-in creation, ready empty album, existing-binding disable/re-enable,
persisted off state after reload, and intact QR/admin sections. The test session
was signed out and closed afterward.

The upgrade/restore scenario preserved event/public URLs, login sessions and new
login, local/S3 SHA-256, contributor attribution, quota settings, historical backend
identity, pending/ready assets, and Immich jobs/mappings. Old migration checksums
and timestamps were identical; only migration 010/default policy were added.
The snapshot helper opens the baseline without applying candidate migrations.
External S3 objects remain separate and unchanged through metadata restore.

Initial test fixture issues were corrected without changing quotas or polling
semantics: bursts over 100 use fresh guest sessions, periodic discovery has a
deadline longer than its five-second interval, and synthetic historical targets
are queried explicitly. A Linux package scan overlapped `npm ci` replacing files;
its completed rerun passed. Native Git was selected explicitly for Windows release
helper tests after a PATH wrapper failed to resolve a peeled FETCH_HEAD revision.

## Review and remaining limits

Focused review checked transaction/job uniqueness, failure isolation, protected
API routing, bounded request bodies and signature inspection, stable target
identity, unchanged upload/quota/Turnstile boundaries, and safe logging. Fault
gates remain solely in `scripts/immichtest`; no production debug control or secret
was introduced. This is not a formal security audit.

One server still owns each data directory. The worker processes one job at a time;
five seconds is an idle scan interval, not a latency SLA. More than 256 untracked
assets need multiple bounded batches. Cancelled selected work pauses automatic
selection until manual Send resumes it. Imported copies are independent; no
continuous remote reconciliation, deletion synchronization, or full video parser
is provided. Failed imports and failed album setup require explicit retry.

Only local test images/artifacts were built. No release/tag was created, no GHCR
image or alias was published/moved, no recovery workflow or production deployment
was invoked, and no OIDC work was added. The Phase 3 PR is to remain unmerged.
