# v1.1 Phase 4.5 validation: independent media quotas

> Historical validation record; not current product or release status.

Validated September 29, 2026 on `codex/v11-per-media-quotas`, based on merged
Phase 4 `95fbffa29d26b45e2e53f92b2128e4c231a860af`. Checkpoint
`bc0cc71a538999686024e56cad3a3fad21c14ecc` was committed and pushed after quota,
frontend and full Linux race validation. Its branch CI and real Immich workflows
passed. Final branch/PR run links and head are recorded in the PR.

## Schema and compatibility review

Only `012_media_quotas.sql` is added. Migrations 001–011 are byte-for-byte
unchanged. It adds six nullable INTEGER event columns: `max_photos`, `max_videos`
(CHECK 1–1,000,000), `max_photo_file_bytes`, `max_video_file_bytes`,
`max_photo_storage_bytes`, `max_video_storage_bytes` (CHECK 1–1,125,899,906,842,624).
It adds nullable TEXT `assets.media_class`, CHECK NULL or `photo`/`video`.
Existing event limits remain unchanged; new policies default to NULL.

Backfill uses ready `mime_type` or pending `expected_mime_type`: supported
JPEG/PNG/WebP/GIF/HEIC/HEIF become photo; MP4/QuickTime become video; unknown
values remain NULL. Filenames do not participate. Existing event/status indexing
bounds aggregate scans; no separate quota ledger or in-memory counters exist.

Admin event JSON accepts/returns all six additive nullable fields and retains
`max_assets`/`max_bytes`. PUT still replaces event policy. Typed `media.photos`
and `media.videos` add ready count/bytes and pending count/reserved bytes.
`media.ready_count` adds an explicit generic total; legacy `media.photo_count`
continues to mean all ready files, including video. Guests receive effective
`max_photo_file_size` and `max_video_file_size`, plus the retained generic
`max_file_size`; private usage counts are not exposed. See [quota reference](quotas.md).

## Correctness review

- Local requests read at most 512 classification bytes before reservation.
  Server Sniff chooses the class, not filename or browser MIME. The remainder
  streams without a database transaction or full-file buffer. Final EOF-aware
  Sniff preserves the malformed exact-512-byte BMFF rejection.
- One IMMEDIATE transaction checks overall event, generic session and matching
  typed count/storage, checks the effective per-file ceiling, and inserts the
  durable pending asset with class. Unknown-length local requests reserve the
  effective ceiling. Completion replaces reserved bytes with actual bytes.
- Direct preparation treats the class as an untrusted declaration, rejects
  conflicting supported MIME/class and unknown declarations, and never guesses
  from filenames. HEAD and bounded-prefix Sniff remain authoritative. Both
  cross-class spoof directions are rejected; safe object cleanup retains the
  immutable attempt and never transfers capacity to another quota bucket.
- Legacy NULL pending rows claim current typed capacity and transition to ready
  atomically. Concurrent completion cannot oversubscribe. Already typed pending
  reservations may finish after limits are lowered; lowering never deletes media.
- Two independent database connections synchronize concurrent reservations for
  photo/video count/storage, overall count/bytes and session count/bytes. Separate
  concurrent legacy completion tests exercise typed count and storage. No reported
  data races. Recovery tests retain finalize-first identity and successful siblings.
- The availability test's callback now runs after the classification prefix,
  where a reservation must exist. Its assertions for a durable pending row,
  deletion lock, no streaming SQL transaction and closed-event cleanup remain.
  This changes sequencing only, not production correctness requirements.

## Validation results

| Command or scenario | Result |
| --- | --- |
| `gofmt -l cmd internal migrations scripts web/embed.go` | Clean |
| `CGO_ENABLED=0 go test ./...` | Passed |
| Linux `go test -race ./...` | Passed locally and in checkpoint CI |
| `go vet ./...`; `go mod verify` | Passed; all modules verified |
| CGO-disabled trimmed production build | Passed |
| `npm ci --prefix web` | Locked install passed |
| `npm run check --prefix web` | Zero errors/warnings |
| `npm test --prefix web` | 42 tests passed |
| `npm run build --prefix web` | Passed |
| `node --test scripts/release.test.mjs scripts/release-recovery.test.mjs` | 18 tests passed |
| `node scripts/check-docs.mjs`; Actionlint; `git diff --check` | Passed |
| Example-environment Compose config; `node scripts/smoke-fresh.mjs --compose-smoke` | Passed; isolated Compose smoke wrapper avoids developer state |
| `node scripts/smoke-fresh.mjs` | Fresh documented local/S3 install, mixed media and export passed |
| `node scripts/smoke-backends.mjs` | Local → S3-A → S3-B → local, historical identity/retry and deletion passed |
| `node scripts/smoke-release.mjs` | Exact populated Phase 4 upgrade and stopped backup/destructive restore passed |
| `node scripts/smoke-oidc.mjs` | Signed disposable provider, binding/replay, restart, outage and password fallback passed |
| Build isolated Immich Compose; `node scripts/smoke-immich.mjs` | Real disposable Immich v3.2.1 lifecycle passed |
| `scripts/verify-image.sh` on amd64 and arm64 | Version/OCI labels, health, UID 10001, private data, minimal runtime, SIGTERM passed |
| Named local OCI build with `--sbom=true --provenance=mode=max`; `scripts/verify-oci.mjs` for both architectures | Digest integrity, SPDX SBOM and SLSA provenance verified; no push |

The initial local OCI export omitted an image name, yielding an empty attestation
subject. Rebuilding with a local image tag passed the unchanged strict verifier.
The existing CI export already names the image and passed. No publishing or
attestation policy was weakened.

Immich's outage test initially observed terminal job state with earlier progress
counts: the status endpoint reads those separately. Its wait now requires both
terminal state and expected counts, retaining the assertions. The full rerun
passed; production Immich behavior is unchanged.

## Browser and operational evidence

Disposable loopback fixture, 390×844 mobile viewport:

- Separate photo/video panels, secondary overall controls, associated labels and
  descriptions, stacked layout with no horizontal overflow.
- Blank count/storage policies persisted as unlimited. An unrelated description
  edit preserved an exact 1,049-byte photo limit despite MiB display rounding.
  Above-server video limit produced the field-specific validation message.
- Oversized photo rejected during selection; small PNG and larger MP4 accepted
  against their different effective limits. A second photo hit the photo count
  limit while MOV still succeeded. Raising the photo limit and failed-only retry
  produced exactly one additional file; completed siblings were not resent.
- Photo, video and overall limit notices appeared independently. Admin counts
  and storage matched types; contributor attribution and QR/public URL remained.
- Switching the same fixture to S3-A allowed browser PNG/MP4 uploads. Object
  fixture recorded 69- and 1,509-byte PUTs, HEADs and `bytes=0-511` verification
  reads. Admin then showed three photos and three videos across local/S3.
- Unknown browser MIME direct-upload rejection and misleading filename/type
  handling are covered by frontend/backend tests; browser metadata is only a
  preflight hint. Server spoof tests verify both reserved/actual class mismatches.

Upgrade starts from the exact merged Phase 4 commit, with local photo/video,
external S3 photo, photo/video/generic pending reservations, contributor names,
overall quotas, multiple backend identities, completed Immich bindings/imports/
jobs and an administrator session. Both upgrade and destructive disposable
restore preserve IDs, public/QR URL, original hashes, old quotas, backend identity,
session and Immich metadata. Typed fields stay NULL; ready and known pending
classes backfill correctly; generic pending remains NULL. Migration checksums and
timestamps survive. Existing sessions work through password+OIDC and OIDC-only
restarts with an unavailable provider. Mixed export hashes remain identical.
S3 objects stay external to the application backup and unchanged.

Real Immich v3.2.1 tests cover early/empty album provisioning, manual import/retry,
automatic local/S3 photo/video import, original hashes, 10+2 burst coalescing,
restart reconciliation, partial failure, outage isolation, and independent
deletion retaining all 27 remote copies. Media tests retain all supported image,
MP4/MOV acceptance and AVIF/audio/3GP/unknown/malformed BMFF rejection.

## Review boundaries and limitations

Focused correctness/security review found no new secret-bearing logging, trusted
browser class, filename-based quota choice, or nontransactional reservation.
Existing authentication, Turnstile verifier/widget separation, CSRF/proxy rules,
storage identity, export identity, dependencies and release permissions remain
unchanged. Runtime images contain no source, test helpers, credentials or build
toolchain. This is a focused review, not a formal security audit.

One server per data directory remains supported. NULL legacy reservations consume
overall/session capacity until first classified, then must fit current typed
limits. Direct browser uploads require supported MIME metadata; ambiguous browser
metadata cannot choose a quota class. Live R2 and production Authentik/Turnstile
are not certified by deterministic tests. No owner's production Authentik or
Immich service was contacted.

No release/tag created, GHCR image published, registry alias moved, recovery
workflow invoked or production deployment performed. The PR is intentionally
left unmerged.
