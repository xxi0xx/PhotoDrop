# Changelog

## [Unreleased]

- Native OIDC administrator sign-in with Authentik setup guidance, explicit
  subject/group authorization, authorization-code + PKCE, browser-bound one-time
  transactions and existing local sessions. Password-only remains the default;
  optional OIDC-only and combined modes isolate provider outages from uploads.
  Migration 011 adds short-lived login transactions without changing session schema.

- Opt-in automatic Immich import per event/target, with durable bounded
  reconciliation, coalesced jobs and restart recovery. Failed imports still need
  manual retry. Migration 010 defaults existing bindings to manual-only.
- Reject truncated MP4/MOV objects ending exactly at the 512-byte sniff boundary
  without rejecting equivalent prefixes of larger valid objects.

- Optional Immich album setup during event creation and before any media exists,
  with persistent background provisioning, retry/restart recovery, and separate
  public browser URLs. Manual Send/Retry remains available.

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
