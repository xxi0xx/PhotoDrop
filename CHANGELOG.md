# Changelog

## [Unreleased] — prepared for 1.0.0

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

Known limitations: images only; no video, resumable multipart upload, gallery, guest
accounts or multiple administrators. One server per data directory. Export requires
hard links. Immich v3.2.1 is the verified version. Live R2 and production-key Turnstile
remain unverified. License and private reporting readiness need owner action.
No stable release date has been assigned.
