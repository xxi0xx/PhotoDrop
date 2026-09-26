# Changelog

## [Unreleased]

- MP4 and QuickTime MOV uploads alongside images, through the existing local/S3,
  quota, export and Immich pipelines. Original bytes are preserved; no transcoding
  or playback UI. No database migration or persisted API/schema renaming.

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
