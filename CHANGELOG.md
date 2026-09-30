# Changelog

<!-- release-notes: version-section -->

## [Unreleased]

## [1.1.0] — 2026-09-30

- Collect MP4 and QuickTime MOV videos alongside JPEG, PNG, WebP, GIF, HEIC and
  HEIF photos. Local and direct S3 uploads, export and Immich preserve original bytes.
- Set independent photo/video count, individual-size and storage limits. New-event
  totals calculate from count × file size; custom and unlimited totals remain available.
  Existing overall/session ceilings and exact stored quota values are preserved.
- Create an Immich album when creating an event, open it from administration, and
  optionally import new uploads automatically. Durable jobs coalesce bursts and recover
  after restart; failed imports support manual retry. Immich copies remain independent.
- Sign in using native OIDC, with an Authentik configuration guide, explicit subject/group
  authorization, or password fallback. Existing sessions and uploads work during IdP outages.
- Reject cross-class upload spoofing and malformed MP4/MOV ending at the signature boundary.
- Improve mixed-media terminology, mobile quota editing, operational guidance and release checks.

Upgrade: stop and back up application data before installing. Migrations 010–012 add
optional automatic import, short-lived OIDC login state and typed quotas. Existing
bindings remain manual-only, authentication remains password-only unless configured,
and new typed limits on existing events start unlimited. Existing NULL/custom storage totals are not
silently calculated. Roll back with the stopped backup and prior image, not by opening
upgraded data with an older binary.

Limitations: no transcoding, playback/gallery, resumable multipart uploads, guest accounts
or multiple administrator roles. Use one server per data directory. Signature checks
are not antivirus/full decoding; export requires a filesystem supporting hard links.

## [1.0.0] — 2026-09-25

- Event management with permanent public links, expiration, enable/disable and QR PNGs.
- Anonymous mobile image uploads, optional contributor names, progress and failed-only retry.
- Local storage and direct S3-compatible uploads with durable historical backend identity.
- Administrator authentication, origin/CSRF checks, quotas, rate limits, trusted proxies,
  optional strict Turnstile verification and upload recovery.
- Portable media/JSON export and native Immich API integration with persistent jobs,
  incremental import, failure recovery and independent media ownership.
- One-container deployment, persistent SQLite/data, non-root app, health and graceful shutdown.
- SemVer version metadata, GHCR release preparation, amd64/arm64 builds, OCI labels,
  BuildKit SBOM/provenance, operational guides and community templates.
- Apache-2.0 license and OCI license metadata; GitHub private vulnerability reporting enabled.
- Owner-completed live Cloudflare R2 and production Turnstile validation recorded.

Known limitations: images only; no video, resumable multipart upload, gallery, guest
accounts or multiple administrators. One server per data directory. Export requires
hard links. Immich v3.2.1 is the verified version. Reference live R2 and production-key
Turnstile validation passed; this does not certify every provider or deployment.
