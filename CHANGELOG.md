# Changelog

<!-- release-notes: version-section -->

## [Unreleased]

## [1.3.0] — 2026-11-28

- Add hosts. People in `PHOTODROP_OIDC_HOST_GROUPS` (or `_HOST_SUBJECTS`) sign in
  through your identity provider as hosts; the existing allowlists still grant the
  administrator role. Each event belongs to the host who created it. Hosts see and
  manage only their own events; administrators see everything, can disable hosts on
  the new Hosts page, and can move an event to another host until it has an Immich
  album.
- Hosts link their own Immich account from the new Account page. PhotoDrop drives
  Immich's OAuth sign-in through your identity provider, never sees a password,
  checks that the Immich user is the host, and stores an encrypted API key that can
  only create albums and upload photos. Each event's albums and photos go to its
  host's own Immich library. Relink, unlink (which deletes the key in Immich) and
  forget are available. Linking needs Immich 3.4.2 or later.
- New `PHOTODROP_SECRETS_KEY_FILE` (a Docker secret) encrypts linked keys with
  AES-256-GCM; `PHOTODROP_SECRETS_PREVIOUS_KEY_FILES` and `photodrop rotate-secrets`
  rotate it. New `PHOTODROP_IMMICH_<KEY>_LINK_MATCH` chooses subject or verified-email
  matching. Immich v3.4.2 is now the tested version.
- Immich work for an event always uses its host's account. A target with a URL but
  no API key is now link-only, and choosing a server where the event's host has no
  account is refused instead of failing later.

Upgrade: everyone signs in once, because sessions now belong to a host. Sign in as
the administrator first so your identity keeps the existing events. Existing Immich
targets and bindings keep working. To add hosts and linking, follow the operator
setup in the upgrade guide, and back up the secrets key separately from `/data`.

## [1.2.1] — 2026-11-26

- Update go-jose to 4.1.6 (via go-oidc 3.21.0) for OIDC token and key validation fixes,
  including GHSA-mgcc-cww5-fp2q. JWKs with RSA keys larger than 4096 bits are now rejected.
- Update compatible Go dependencies (AWS SDK, golang.org/x/crypto, SQLite driver), frontend
  build tooling (Svelte, Vite) and pinned GitHub Actions.

Upgrade: no migrations or configuration changes. If OIDC is enabled, confirm your identity
provider's signing keys are RSA 4096 bits or smaller (or EC/Ed25519), then test a sign-in.

## [1.2.0] — 2026-11-26

- Raise the `PHOTODROP_MAX_FILE_SIZE` ceiling from 1 GiB to 2 GiB (2147483648 bytes).
  Each file is still one request, within S3/R2 single-upload limits.
- Allow 30 minutes per file transfer instead of 10: guest uploads, object-store reads
  for export/Immich and Immich uploads. Pending-upload cleanup still waits one hour.

Upgrade: no migrations. Existing configuration keeps working; values above 1 GiB
require this version. Presigned upload URLs still follow `PHOTODROP_S3_PRESIGN_TTL`;
validate a long direct upload against your provider before relying on it. The default
5 GiB upload-session total and lower per-event limits still apply.

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
