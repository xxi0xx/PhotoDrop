# Portable export and optional Immich integration

Gate 6 adds two independent ways to keep an event's ready photos. Export writes
ordinary files and JSON. The native Immich integration sends independent copies
through Immich's HTTP API. Neither feature changes guest uploads, quotas,
Turnstile verification, or the one-container PhotoDrop deployment.

## Export an event

Use the numeric event ID from its admin URL: `/admin/events/12` means `--event 12`.
The destination is the exact new directory supplied, with an existing writable
parent. Existing directories are refused, including apparently empty ones.

```sh
PHOTODROP_DATA_DIR=./data photodrop export --event 12 --output ./exports/wedding
```

The export command needs the source data directory and configured storage
credentials. It does not need an administrator password, frontend build at run
time, Turnstile configuration, or an Immich target. The ordinary server command
and `photodrop healthcheck` are unchanged. Use the matching PhotoDrop binary for
the data directory; the command verifies/applies embedded database migrations.

For Docker, add an optional mount with a small overlay, for example
`compose.export.yml`:

```yaml
services:
  photodrop:
    volumes:
      - ./exports:/export
```

Create `./exports` first and make it writable by container UID/GID `10001:10001`
using your host's filesystem permissions. PhotoDrop does not recursively change
ownership of export directories. Then:

```sh
docker compose -f compose.yml -f compose.export.yml up -d
docker compose -f compose.yml -f compose.export.yml exec --user 10001 photodrop \
  photodrop export --event 12 --output /export/wedding
```

The explicit `--user 10001` matters: `docker exec` bypasses the normal entrypoint's
privilege drop. `/export` is optional and absent from normal `compose.yml`.

```text
wedding/
  photos/
    IMG_0001.JPG
    IMG_0001_2.JPG
  photodrop-manifest.json
```

Only ready assets at the start of the export are included, ordered by upload
creation time then PhotoDrop asset ID. A later upload belongs to a later export.
Reads resolve each asset's recorded storage backend ID, regardless of the active
upload provider. Retain historical named S3 configurations as described in
[storage-backends.md](storage-backends.md). Missing credentials, changed object
metadata, unavailable objects, or read/write failures make export fail explicitly.

The filename is based on the original filename, with both slash styles treated
as separators. Unsafe/control characters become underscores; leading/trailing
dots/spaces and Windows device names are handled, and long names are shortened.
Collisions, including case differences, receive deterministic `_2`, `_3` suffixes.
The original filename remains in the manifest. Internal object keys are never
used as export names. Filenames are not identity; use `photoDropAssetId`.

Each file streams through a 32 KiB buffer into an exclusive `.part` file. Its
byte count is checked, the file is synced/closed, and a hard link atomically
publishes it without replacing an existing destination. The temporary name is
then removed. This requires a destination filesystem with hard-link support
(for example ext4, APFS, or NTFS); unsupported filesystems fail safely. You can
copy a completed export to another filesystem afterward. No full photo is
buffered in RAM.

On the first failure, the command exits nonzero and identifies the asset ID.
Completed files may remain; incomplete files are removed. No final manifest is
published unless every asset succeeds. An abrupt process termination can leave
`.part` files. Inspect/remove a failed directory or choose a new output path
before retrying. This is a deliberately simple export, not a resumable backup.
Avoid concurrent event deletion during CLI export: a separate process cannot
share the server's event lock. A missing source fails the export rather than
producing a misleading complete manifest.

## Manifest version 1

```json
{
  "formatVersion": 1,
  "event": {
    "id": 12,
    "publicId": "immutable-public-event-id",
    "name": "Wedding",
    "eventDate": "2026-09-04"
  },
  "exportedAt": "2026-09-15T12:00:00Z",
  "assets": [
    {
      "photoDropAssetId": "photodrop-asset-id",
      "originalFilename": "IMG_0001.JPG",
      "exportFilename": "IMG_0001.JPG",
      "mimeType": "image/jpeg",
      "sizeBytes": 5839201,
      "uploadedAt": "2026-09-04T16:30:00Z"
    }
  ]
}
```

`eventDate` may be null. `exportFilename` is relative to `photos/`; timestamps
are UTC RFC 3339. `uploadedAt` records PhotoDrop upload completion, not EXIF time.
The format excludes credentials, presigned URLs, backend addresses/keys, and
server filesystem paths. User-supplied original filenames and event text are
preserved as data. This is a portable event/media export, not a SQLite backup.

## Configure Immich

The adapter is validated against **Immich v3.2.1**. Its API contract is restricted
to major version 3; older/future major versions fail connection validation.
Other 3.x releases are not independently verified. The target must be reachable
from the PhotoDrop container, which may differ from your browser's address.

```dotenv
PHOTODROP_IMMICH_TARGET=home
PHOTODROP_IMMICH_TARGETS=home
PHOTODROP_IMMICH_HOME_URL=https://photos.example.com
PHOTODROP_IMMICH_HOME_API_KEY=your-private-api-key
```

Keys follow the named storage convention: 1–63 lowercase letters/digits,
hyphen-separated, starting with a letter. Uppercase the key and replace hyphens
with underscores in environment prefixes. `PHOTODROP_IMMICH_TARGETS` is an
optional comma-separated list (up to 32), defaulting to the active target.
For example `home,old-home` loads both `PHOTODROP_IMMICH_HOME_*` and
`PHOTODROP_IMMICH_OLD_HOME_*`. The admin can inspect/select known historical
targets. There is no browser credential editor.

For Compose, explicitly forward these variables in an overlay such as
`compose.integration.yml`:

```yaml
services:
  photodrop:
    environment:
      PHOTODROP_IMMICH_TARGET: home
      PHOTODROP_IMMICH_TARGETS: home
      PHOTODROP_IMMICH_HOME_URL: '${PHOTODROP_IMMICH_HOME_URL}'
      PHOTODROP_IMMICH_HOME_API_KEY: '${PHOTODROP_IMMICH_HOME_API_KEY}'
```

Keep actual secrets in your untracked `.env` or deployment secret environment.
Then run `docker compose -f compose.yml -f compose.integration.yml up -d`.
Putting new named variables only in `.env` does not automatically forward them
through the existing production Compose file. You may combine the optional
export/storage overlays. Never publish a resolved Compose configuration with
credentials. Normal production Compose remains one PhotoDrop service; do not
add the test Immich stack to your production deployment.

The non-secret target key, normalized base origin, and creation time are stored
in `immich_targets`. Host casing, default ports, and a trailing slash normalize.
URLs must be HTTP(S) origins without credentials, paths (including `/api`), query,
or fragment. Prefer HTTPS outside a trusted private container network.
Changing a URL materially under an existing key fails startup rather than
reinterpreting historical imports. Use a new key for a different server, even
if the old key is no longer active. Rotate API keys for the same Immich user;
another user may not own the previously mapped album/assets.

API keys stay in process configuration, never SQLite, logs, or browser responses.
Omitting historical credentials keeps their records visible and makes operations
on that target safely unavailable. Immich outages, invalid keys, failed jobs,
and missing credentials do not change `/healthz`.

## Minimum API-key permissions

Create a key in your Immich account's API-key settings with exactly:

- `album.create`
- `album.read`
- `asset.upload`
- `albumAsset.create`

This four-permission set passed the real v3.2.1 integration suite. PhotoDrop does
not need delete, asset download, user administration, or full permissions.
`GET /api/api-keys/me` authenticates the key and exposes its permission list
without requiring `apiKey.read`; `GET /api/server/version` identifies the API
version. **Test Immich connection** checks these and verifies the required
permission set without creating an album or media. Actual create/upload/assignment
operations can still fail later because of quota, storage, or changed permissions.

Contract sources: [pinned OpenAPI schema](https://github.com/immich-app/immich/blob/v3.2.1/open-api/immich-openapi-specs.json),
[API-key controller](https://github.com/immich-app/immich/blob/v3.2.1/server/src/controllers/api-key.controller.ts),
and [official API documentation](https://docs.immich.app/api/).

## Albums, jobs, and recovery

Open an existing event in the admin, test the target connection, optionally
change the initial album name, then send its unimported photos. A short protected
POST commits a SQLite job and returns HTTP 202. The existing Go process runs one
job at a time with at most **three concurrent uploads**, then persists progress
for each photo. The page polls every two seconds; leaving the page does not stop
the job. Transfers stream local/S3 bodies through `io.Pipe` multipart requests
directly to Immich. No full-file staging or PhotoDrop content-hash system is used.

The first import creates a dedicated album, defaulting to the event name. Its
Immich album ID is saved and used thereafter; external album renaming is safe.
PhotoDrop does not search by name and select an arbitrary existing album.
A random marker is put in the album description to reconcile an uncertain first
creation. After an interrupted/lost album response, PhotoDrop finds that exact
marker; if absent or ambiguous, it fails closed rather than creating more albums.
Keep that marker until the initial album association has completed. Once its ID
is persisted, the description is no longer used for normal operations.

If creation was never accepted but its outcome is uncertain, inspect Immich
before recovery. Restore the marker on the intended album and retry. Alternatively,
after confirming no matching album exists, stop PhotoDrop, back up SQLite, and
use a trusted SQLite tool to set only that `immich_event_imports` row's
`album_state` back to `new` (it must have `immich_album_id IS NULL`). Restart and
retry. This manual recovery prevents uncontrolled album duplication; do not
blindly reset uncertain creation state.

`immich_asset_imports` records a unique event/target/asset association, status,
remote asset ID, attempt count, safe error, and timestamps. States are `pending`,
`importing`, `imported`, `duplicate`, or `failed`. A successful upload's remote ID
is saved **before** album assignment. If assignment fails, retry only adds that
known ID to the album. Successful/duplicate records are never reuploaded during
an ordinary retry. New sends discover newly ready assets; failed-only retries
exclude new photos. Previously cancelled pending work is included in a new send.

Immich returns `created` or `duplicate` with a remote asset ID. A lost upload
response leaves its outcome unresolved; the next retry streams it again and
relies on Immich's own content deduplication. The real suite hides an accepted
response and proves a duplicate ID is returned and assigned without another
Immich copy. This is not a cross-server exactly-once guarantee: deduplication is
scoped to Immich's user/content state. Each asset gets at most one attempt per
job; there are no infinite immediate retry loops. Manual retries and process
restart may repeat uncertain work. Do not delete a still-unresolved remote copy
or change account identity during recovery.

Queued/running jobs and per-asset progress survive restart. On server startup,
interrupted running jobs become queued and `importing` assets become pending,
preserving any known remote ID. Already-accounted assets stay accounted for.
The worker uses the server lifetime context, separate from startup's 15-second
budget. SIGTERM stops scheduling/cancels in-flight requests and joins workers
before closing SQLite. Only one PhotoDrop server process may own a `/data`
directory; running multiple servers against it is unsupported. Export CLI
processes start no worker.

Cancel import stops scheduling and cancels in-flight requests; completed records
remain. A read lock pins the event during an active job, so deletion returns a
busy response. Cancel, wait for cancellation status, then delete. Queued jobs
and integration metadata cascade with event deletion.

Deleting a PhotoDrop event does NOT delete assets already imported into Immich.
Deleting assets from Immich does NOT delete PhotoDrop originals.
PhotoDrop never deletes Immich assets or albums.
Do not manually copy files into Immich's managed library directory.

Control requests use 15-second deadlines (connection test has a total 20-second
HTTP budget); uploads/S3 reads allow up to 10 minutes and honor cancellation.
JSON responses are bounded (64 KiB normally, 4 MiB for album listings/details).
Credentials are never forwarded across redirects. Provider response bodies and
transport errors are replaced with safe error messages. URLs are deployment
configuration, not public/admin-request input.

## Manual fallback and limitations

You can export first and independently use the [official Immich CLI](https://docs.immich.app/features/command-line-interface/)
to upload the resulting `photos/` directory. Follow that CLI version's setup and
permission requirements. PhotoDrop does not invoke it or include Node.js in its
production container. Export has no Immich dependency.

Unreleased MP4/MOV support preserves original video bytes through export and
Immich import; see [media formats](media-formats.md). There is no gallery/public
download, transcoding, playback UI, media migration or EXIF processing. QR sharing and optional contributor names are available in the
[guest UX](production-ux.md). Completed
imports are an accounting record, not a continuous synchronization/audit of
externally deleted Immich media. Restoring a different Immich deployment requires
a new target key. S3 credentials must allow reading historical source objects.

## Real integration environment

```sh
docker compose -f scripts/compose-immich-test.yml build
node scripts/smoke-immich.mjs
docker compose -f scripts/compose-immich-test.yml down
```

The separate Compose project pins Immich v3.2.1, its official Postgres image and
Valkey dependency. Machine learning is disabled. All ports bind to loopback.
Disposable account credentials are fixtures, and runtime test API keys have the
four required permissions. It also starts two signed S3 test endpoints and a
test-only proxy that forces failures/response loss against the **real** Immich
API. The proxy cannot be selected by the production executable.

The smoke script validates mixed export hashes, auth, album create/rename,
upload/assignment, incremental work, failure/retry, real duplicate reconciliation,
abrupt process restart, outage/missing-key health, and independent deletion.
`--browser` leaves a fresh event and generated image in ignored `.tmp/` for
manual browser validation. The dedicated **Immich integration** GitHub workflow
runs the lifecycle for relevant branches/PRs and can also be dispatched manually.
Normal CI retains the full regression, race, Docker, and provider-switch checks.
Test volumes are preserved by `down`; remove only this disposable project's
volumes explicitly when you want a fresh Immich installation.

For additional browser failure/cancellation cases, with that fixture running in
local mode, `node scripts/seed-immich-browser.mjs 4` creates four additional
generated photos through the real guest upload endpoint. The proxy's loopback
`POST http://localhost:2284/__test/control` accepts `fail_next`, `lose_next`, and
`delay_ms` test controls; GET returns aggregate upload/outcome counters. These
controls belong only to the separate test executable, never the production app.
