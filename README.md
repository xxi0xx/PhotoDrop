# PhotoDrop

Collect event photos and videos from guests without asking them to create accounts. Share an
event link or QR code; guests choose files, optionally leave their name, and
upload from their phone. One administrator manages events, quotas, and exports.

PhotoDrop runs as one Go container with an embedded Svelte interface and SQLite.
It stores media locally or sends browser uploads directly to private S3-compatible
storage. Optional Immich integration copies completed media through its API.

**Release status:** `v1.0.0` is ready for the owner's release tag; publication
is triggered by that tag. Licensed under Apache-2.0. Owner-completed live R2
and production Turnstile validation passed; private vulnerability reporting is enabled.
See [release readiness](docs/gate-8-validation.md) for the dated validation record.

## Features

- Events with permanent public IDs, enable/disable state, dates, and expiration.
- Browser-generated QR codes and downloadable PNGs, with no external QR service.
- Mobile image/video selection, progress, failed-only retry, and add-more flow.
- Optional private contributor labels; no guest profiles or public photo browsing.
- Local or direct S3 uploads, historical backend identity, and finalize-first recovery.
- Administrator authentication, CSRF/origin checks, quotas, rate limits, optional Turnstile.
- Portable filesystem export and persistent, restart-safe Immich import jobs.

## How uploads work

```text
Local: Browser -- media bytes --> PhotoDrop --> /data/uploads
S3:    Browser -- control -----> PhotoDrop --> SQLite metadata
       Browser -- media bytes ----------------> private object storage
                                PhotoDrop --> HEAD / bounded verification read
```

PhotoDrop does not proxy direct-S3 media upload bodies. `/data` remains necessary
in both modes. Keep historical bucket configuration when changing storage.

## Quick start

Requires Docker Engine/Desktop with Compose v2. Host Go/Node are unnecessary.
Until stable images are published, build from source:

```sh
git clone https://github.com/xxi0xx/PhotoDrop.git
cd PhotoDrop
cp .env.example .env
# Edit .env: set PHOTODROP_ADMIN_PASSWORD to your own strong 12–72-byte password.
docker compose config --quiet
docker compose up --build --wait -d
curl --fail http://localhost:8080/healthz
```

Open [the local administrator login](http://localhost:8080/admin/login), create an
event, and copy its link or download its QR. For public deployment, configure HTTPS
and `PHOTODROP_BASE_URL` first. Do not share `.env` or expanded Compose output.
The [installation guide](docs/install.md) includes the post-release image workflow.

## Storage, proxies, and Immich

Local storage needs only the persistent `./data` mount. S3-compatible storage is
optional; [storage setup](docs/storage.md) covers private buckets, CORS, and R2.
Cloudflare-proxied body limits can affect local uploads; direct S3 avoids sending
media bodies through the PhotoDrop proxy. See [HTTPS and proxies](docs/reverse-proxy.md).

Immich remains a separate application and data store. Use its HTTP API, never a
shared internal library mount. **Immich v3.2.1** is the tested version; other 3.x
versions are not independently verified. See [export and Immich](docs/export-immich.md).

## Documentation

| Task | Guide |
| --- | --- |
| Install and configure | [Install](docs/install.md), [environment reference](docs/configuration.md) |
| Expose safely | [Reverse proxy / HTTPS](docs/reverse-proxy.md), [security controls](docs/security.md), [Turnstile](docs/turnstile.md) |
| Choose storage | [S3/R2](docs/storage.md), [historical backends](docs/storage-backends.md) |
| Share and collect | [Guest and administrator UX](docs/production-ux.md) |
| Keep and move media | [Export / Immich](docs/export-immich.md), [backup / restore](docs/backup-restore.md) |
| Operate and upgrade | [Upgrading](docs/upgrading.md), [troubleshooting](docs/troubleshooting.md) |
| Understand and contribute | [Architecture](docs/architecture.md), [contributing](CONTRIBUTING.md) |
| Release history and process | [Changelog](CHANGELOG.md), [release policy](docs/releases.md), [validation](docs/gate-8-validation.md) |

Earlier gate validation records remain in `docs/` as historical evidence.

## Security and contributing

Read [SECURITY.md](SECURITY.md) before reporting a vulnerability. Do not put exploit
details or secrets in public issues. GitHub private vulnerability reporting is enabled.
See [CONTRIBUTING.md](CONTRIBUTING.md) for tests and PRs.
Future work uses normal versioned issues/milestones rather than additional gates.

## License

Licensed under the [Apache License, Version 2.0](LICENSE) (SPDX: `Apache-2.0`).

## Current limitations

Supported on this development branch: JPEG, PNG, WebP, GIF, HEIC, HEIF, MP4 and
QuickTime MOV. Video support is unreleased (v1.0.0 supports images only). See
[media formats and validation](docs/media-formats.md). No transcoding, playback UI, multipart/resumable uploads,
public gallery/downloads, guest accounts, or multi-administrator model. Signature
checks are not antivirus or full media decoding. Use one server per data directory.
Export needs a destination filesystem supporting hard links. Recovery selections
exist only in the current browser page. See [live validation](docs/live-validation.md)
for the completed reference R2/production Turnstile flow and new-deployment checks.
