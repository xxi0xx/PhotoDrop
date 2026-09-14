# PhotoDrop

PhotoDrop is a self-hosted event photo collection app. **Gate 5 adds optional
Turnstile, temporary upload grants, atomic quotas, and abuse controls to local
and S3-compatible direct uploads, including the Cloudflare R2 S3 API:**
guests select photos, see per-file and overall
progress, and retry failed files. One administrator manages events and sees
completed photo counts and storage totals. The Go/Svelte/SQLite foundation,
authentication, event links, and one-container deployment remain intact.

**Video, multipart/resumable uploads, Immich, QR generation, public downloads, galleries, thumbnails, exports,
contributor names, and Gate 6 integrations are not implemented.**

See [security configuration and operating limits](docs/security.md) before sharing
an event publicly, and the [Gate 5 validation report](docs/gate-5-validation.md).
Quotas are optional; set an event's photo and storage limits for a resource ceiling.

See [S3/R2 setup and operating semantics](docs/storage.md). Local storage remains
the default. Live R2 interoperability and browser CORS require validation with
your private bucket; deterministic S3 tests do not establish a live R2 pass.
See [durable backend configuration and upgrades](docs/storage-backends.md) and
the [post-Gate-4 validation report](docs/storage-backends-validation.md).

## Architecture

- One Go executable serves HTTP and the compiled Svelte frontend on port 8080.
- SQLite uses `database/sql` and the CGO-free `modernc.org/sqlite` driver; no ORM.
- SQL migrations and frontend assets are embedded at build time.
- SQLite stores metadata and each asset's immutable storage backend identity. Image bytes live under
  `/data/uploads` in local mode or in a private S3-compatible bucket in S3 mode.
- One production container and one `/data` volume; no external database, Redis,
  Node.js runtime, or reverse proxy is required.
- Structured JSON logs go to stdout. SIGINT/SIGTERM drains HTTP requests for up
  to 10 seconds, then closes SQLite. A failed startup exits with a useful error.

The frontend selects a Svelte view for explicitly registered page routes; normal
links navigate between pages. The backend protects admin pages before serving
the embedded shell. Static assets are served directly and unknown paths return
404; there is no catch-all SPA fallback.

## Prerequisites

- Go 1.26 or newer.
- Node.js 22.12 or newer and npm (Node 22 is used in CI and Docker builds).
- Docker with Compose v2 for container deployment. Host Go and Node are not
  needed when building with Docker.

## Local development

Set `PHOTODROP_ADMIN_PASSWORD` to your own strong password of 12–72 bytes before
starting Go. It has no default. Keep the value private and recoverable in your
password manager or deployment environment. A missing or invalid value prevents
startup. Unlike Compose, `go run` does **not** load `.env` automatically.

From the repository root, build the frontend **before any Go test or build**:

```sh
npm ci --prefix web
npm run build --prefix web
```

Run on macOS/Linux:

```sh
# Read privately in Bash, then export for the Go process.
read -r -s -p 'Administrator password: ' PHOTODROP_ADMIN_PASSWORD; echo
export PHOTODROP_ADMIN_PASSWORD
PHOTODROP_DATA_DIR=./data go run ./cmd/photodrop
```

Run in Windows PowerShell:

```powershell
$credential = Read-Host 'Administrator password' -AsSecureString
$env:PHOTODROP_ADMIN_PASSWORD = [System.Net.NetworkCredential]::new('', $credential).Password
$env:PHOTODROP_DATA_DIR = './data'
go run ./cmd/photodrop
```

Open [http://localhost:8080](http://localhost:8080). The Go process serves the
embedded build; rebuild the frontend and restart Go to see frontend changes.

For frontend hot reload, keep Go running and use a second terminal:

```sh
npm run dev --prefix web
```

Vite prints its local URL (normally `http://127.0.0.1:5173`) and proxies `/healthz`
and `/api` to `http://127.0.0.1:8080`, preserving the browser's Host for the origin
check. Leave `PHOTODROP_BASE_URL` unset during development or set it to the exact
Vite origin. If you change the backend port, update `web/vite.config.js`.
The Vite server is never included in production.

## Tests and production build

These commands work from a fresh checkout, in this order:

```sh
npm ci --prefix web
npm run check --prefix web
npm run build --prefix web
go test ./...
go test -race ./...
CGO_ENABLED=0 go test ./...
go vet ./...
gofmt -l cmd internal migrations web/embed.go
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/photodrop ./cmd/photodrop
```

The formatting command must print nothing. `npm run check` performs strict
TypeScript/Svelte and accessibility checks and fails on warnings. Go tests cover
configuration, SQLite creation and persistence, migration execution/rollback/
history/concurrency, HTTP health, embedded JS/CSS, 404s, and draining active
requests during shutdown. Gate 2 adds authentication, cookie/CSRF checks, session
rotation/logout/expiry/persistence, password changes, event CRUD/validation,
public-ID invariants, guest availability, and a Gate 1 database upgrade test.
Gate 3 adds bounded streaming, image validation, upload lifecycle and cleanup,
session/event isolation, concurrent uploads, safe media deletion, ready-only
statistics, and a Gate 2 database upgrade test.
The race detector requires a C toolchain; CI runs it on Linux. In PowerShell,
set `$env:CGO_ENABLED = '0'` for the CGO-free commands instead of shell prefixes.
Ordinary tests inject their own credentials and do not need a configured password.

For an explicitly CGO-free production build on macOS/Linux:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/photodrop ./cmd/photodrop
PHOTODROP_DATA_DIR=./data ./bin/photodrop
```

On Windows:

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags='-s -w' -o bin/photodrop.exe ./cmd/photodrop
$env:PHOTODROP_DATA_DIR = './data'
./bin/photodrop.exe
```

The resulting executable needs neither Node nor the `web/dist` directory at
runtime. `web/dist` is generated and intentionally excluded from Git.

## Docker Compose

Copy `.env.example` to `.env`, then set `PHOTODROP_ADMIN_PASSWORD` to your own
strong 12–72-byte password. The example intentionally has no password value.
Single-quote the value in `.env` if it contains `$` or `#`. Keep `.env` private;
it is excluded from both Git and the Docker build context. The credential is
runtime configuration, never a Docker build argument. You may instead supply it
through the Compose process environment. Do not run `docker compose config`
without `--quiet` in shared logs, because expanded configuration includes secrets.

```sh
docker compose config --quiet
docker compose up --build -d
docker compose ps
curl http://localhost:8080/healthz
docker compose logs -f
```

Wait for the service to report healthy. `/healthz` returns HTTP 200 with
`{"status":"ok"}` after successful database initialization and migrations. It is
a liveness endpoint, not a continuous database integrity check.

The container's built-in health check uses `photodrop healthcheck`, which probes
the configured listening address without opening or migrating SQLite. No curl
binary is needed inside the image.

```sh
docker compose restart
docker compose down
```

Both preserve the bind-mounted database at **`./data/photodrop.db`**. The default
container data directory is **`/data`**. SQLite may create a temporary rollback
journal alongside the database; keep the entire data directory on persistent
local storage. To back up PhotoDrop, stop the service and copy `./data` to secure
storage. Back up the whole directory together: it contains password hashes,
sessions, event/asset metadata, and uploaded photos. Do not back up only SQLite.

Build the image separately with:

```sh
docker build -t photodrop:local .
```

The Alpine runtime includes the executable and a small privilege-drop helper.
To support a fresh `./data` bind mount on Linux, the entrypoint starts as root,
creates/chowns only the configured data directory to **UID/GID 10001**, then
executes PhotoDrop as that non-root user and PID 1. It does not recursively change
ownership of existing files. Existing database files must be writable by 10001.
You can supply Docker's `--user` (or Compose `user`) to skip preparation when the
mounted directory is already writable by your chosen user. Docker Desktop
handles bind-mount permissions differently from native Linux.

Run the container integration check on Linux/macOS (or a shell with Docker,
curl, and Node.js 22.12+ available). Configure the password first, through `.env`
or the environment. It uses the repository's `./data` and port 8080:

```sh
sh scripts/smoke-compose.sh
docker compose down
```

It builds the image and runs `node scripts/smoke-events.mjs`: login, CSRF rejection,
two independent events, public lookup, disable/expire/re-enable/edit, persistence
of the same session and event across restart, deletion, public 404, and logout.
It also runs `node scripts/smoke-uploads.mjs` with a tiny embedded PNG fixture:
anonymous sessions, concurrent uploads with duplicate/path-like filenames,
non-image rejection, cross-event isolation, closed-event rejection, admin totals,
media hashes across restart, and event deletion that preserves unrelated media.
Both scripts create only temporary test events and remove them. It then verifies
HTTP and non-root execution, checks that Node/npm/Go are absent from the runtime,
stops via SIGTERM, compares the stopped database before/after restart, and checks
clean exit codes. It preserves existing events and leaves the container stopped;
use `docker compose up --wait -d` to resume. Node is only a build/test dependency.
CI uses an ephemeral masked password and runs this script; it never publishes images.

## Manage and share events

1. Open `/admin/login` and sign in with the configured password.
2. Choose **New Event**, enter a name, optionally add description/date/expiration,
   and select **Create event**. New events default to enabled in the UI.
3. From the event list, choose **Manage** to edit, disable/re-enable, or copy/visit
   the public link. Deletion requires an explicit confirmation and is permanent.

Each event has an immutable public URL such as `/e/Nk4Pr8sVhx7JQ2mC9Lwu0aBd`.
Anyone with that link can view the guest page without signing in. Internal SQLite
IDs are used only by administration; they do not work as guest identifiers.
Anyone with an open event link can also upload supported images.
Disabling or expiring an event keeps it in the admin list and shows guests a
generic closed page containing its name. Closed pages omit the description/date
and do not disclose why the event closed. Deleted or unknown links return HTTP 404.

Names are trimmed and contain 1–200 Unicode characters; optional descriptions
are trimmed and limited to 4,000. Both render as plain text. `event_date` is an
optional ISO calendar date (`YYYY-MM-DD`), with no timezone conversion. Expiration
is an optional instant supplied as RFC3339 with a timezone and persisted in UTC.
The editor displays/accepts expiration in the administrator's local timezone.
An event is open only when enabled and the current time is strictly before its
expiration, if set. Expiration never deletes data. Status is evaluated when
fetching the event; an already-open guest page updates when reloaded. Uploads
independently recheck availability before accepting a file and before marking it
ready, so a stale browser page cannot upload to an event that has closed.

## Guest uploads and local storage

Open an event link, choose up to **100 images per browser batch**, then choose
**Upload Photos**. PhotoDrop sends one request per image, with at most **three
concurrent transfers**. Each row shows waiting, progress/finishing, uploaded, or
failed state; overall progress uses the selected files' byte totals. **Retry
Failed** retries only failed files. **Cancel uploads** stops queued requests and
aborts active transfers without deleting completed photos. After success, use
**Add More Photos** for a new batch. No file previews or gallery are provided.

Supported types are **JPEG, PNG, WebP, GIF, HEIC, and HEIF**. The default limit is
**52,428,800 bytes (50 MiB) per file**, configured with `PHOTODROP_MAX_FILE_SIZE`.
The browser's `accept="image/*"` is a picker hint; the server independently
validates the original filename, declared media type, initial content bytes,
and actual streamed byte count. Missing/empty files and unsupported types such
as SVG, AVIF, video, or disguised text are rejected. HEIC/HEIF detection uses a
bounded initial `ftyp` brand check; an `ftyp` box beyond the first 512 bytes is
not supported. Validation is lightweight signature sniffing, not full image
decoding, malware detection, or a guarantee that every image decoder can open a file.

Files live at `PHOTODROP_DATA_DIR/uploads/e<internal-event-id>_<random-asset-id>`.
These flat keys are generated server-side using 128 random bits per asset.
Original filenames are preserved verbatim as metadata (1–255 valid Unicode
characters; no NUL/newline, not whitespace-only) and rendered as escaped text.
They never participate in a storage path. Key syntax is strictly validated;
Go's `os.Root` confines operations to storage. Deletion also verifies that the
key belongs to the asset and event in question, and removes only individual
files, without recursive deletion or following final symlinks. An upload-root
symlink is rejected. Directories use mode 0700 and new files use mode 0600 on
Linux; media is not executable. No media path is served over HTTP.

`internal/media` manages authorization and metadata through a small `Store`
interface (`Put` and `Delete`). Only `internal/storage.Local` handles filesystem
operations. S3 uses a separate `Direct` capability for authorization, HEAD,
ranged reads, and deletion; it never receives an image-body reader.
The API/lifecycle below describes **local mode**. See [direct uploads](docs/storage.md)
for S3 mode, verification, idempotent retries, and provider switching.

### Upload API and lifecycle

Both guest endpoints are anonymous and ignore admin cookies/privileges. They
require the existing same-origin `Origin`/`Referer` check, **not an admin CSRF
token**. There is no cross-origin upload API.

| Method | Route | Request / response |
| --- | --- | --- |
| POST | `/api/public/events/{public_id}/upload-sessions` | JSON `{}`; returns `{"upload_session":{"id":"..."},"upload_strategy":"local"}` with HTTP 201 (`direct` in S3 mode) |
| POST | `/api/public/events/{public_id}/upload-sessions/{session_id}/assets` | One raw image body; returns guest-safe asset metadata with HTTP 201 |

Each upload session has a random 128-bit ID and belongs to exactly one event.
The file request supplies `Content-Type` and `Content-Disposition: attachment;
filename*=UTF-8''IMG_1234.JPG` (percent-encode the UTF-8 filename). No multipart
form envelope is used. The browser sends the `File` directly with XMLHttpRequest
for real upload-progress events; it never converts files into base64.

Success returns `{"asset":{"id":"...","filename":"IMG_1234.JPG",
"mime_type":"image/jpeg","size":1234,"status":"ready"}}`. It contains no
internal event ID, storage key, or filesystem path. Errors use the existing JSON
envelope: 413 for oversize, 415 for unsupported content, 422 for empty files or
invalid filenames, 409 for closed events, and 404 for unknown events or sessions
that do not belong to that event. SQL/filesystem errors are logged internally
and return safe generic messages. Original filenames, upload session IDs, file
contents, and credentials are not logged.

The persistence sequence is:

1. Verify the event/session association and create a `pending` asset in a short
   SQLite transaction, then commit before reading the image stream.
2. Sniff at most 512 bytes, then stream through bounded readers and a fixed
   32 KiB copy buffer into an exclusively created `<key>.part` file.
3. Check the actual byte limit, flush and close the file, and rename to its final
   key on the same filesystem. Verify final size; sync the directory on Linux.
4. Recheck event availability, mark the asset `ready`, record MIME/size/completion
   time, and update its upload session in a short SQLite transaction.

Only then is success reported. Failed attempts remove temporary/final objects
and their pending metadata. If cleanup fails, the pending row remains as a
recovery record. SQLite transactions are never held while receiving media.
`Content-Length` is an early check, not the limit: unknown-length/chunked requests
are also bounded. The upload path extends read/write deadlines to ten minutes;
other requests keep the existing 15-second read/30-second write deadlines, and
header reads remain limited to five seconds. A normal HTTPS reverse proxy needs
to allow the configured request size/duration; no buffering, special forwarded
headers, proxy dependency, or extra container is required.

Local-mode retries create a new asset attempt and cannot overwrite a completed object.
Completion state is retained only in the current page session. If the server
committed a photo but its success response was lost, a retry may create a second
copy; there is no durable idempotency or content deduplication. Upload session IDs
are temporary bounded upload grants, not guest identities or permanent resumability.

### Cleanup and event deletion

Completed files and metadata survive restart. At startup, PhotoDrop attempts
cleanup of at most **1,000 pending assets older than one hour**, including both
`.part` and already-renamed files. Recent pending rows remain incomplete and
never count as uploaded photos, but reserve quota capacity. A bounded internal
maintenance loop retries cleanup every five minutes with a five-second budget.
Cleanup failures are logged and retain their metadata. There is no separate
worker service or unbounded full-filesystem scan. Normal shutdown drains for ten seconds;
transfers that outlast shutdown can be interrupted and recovered by this policy.
Run **one PhotoDrop instance per data directory**; event transfer/deletion locks
are local to that process, and sharing storage between live instances is unsupported.

Admin event responses contain `media.photo_count` and `media.storage_bytes`,
calculated in SQLite from **ready assets only**, plus separate pending count and
reserved-byte fields when nonzero. Both contribute to quotas. Refresh the event list or reload
the editor to see new uploads. There is no individual-photo browsing/deletion UI.

Deleting an event now permanently removes its photos and upload metadata. If
transfers are active for that event, deletion returns 409; disable the event and
retry once active transfers finish. Cleanup persists a `deleting` flag, disables
the event, and blocks edits/new uploads. Before removing each file it takes that
asset out of ready state, then removes its files and row. On partial failure the
event stays closed and visible with a cleanup warning; retry **Delete event** to
continue. The event/session rows are removed only after media cleanup succeeds.
Unrelated events' files are never deletion targets. There is no total-storage
quota in this gate; disk capacity remains an operator responsibility.

## Authentication and API

One shared administrator password is hashed with bcrypt (cost 12) before storage
in SQLite. Passwords, hashes, session tokens, CSRF tokens, and request bodies are
not logged. Changing the configured password and restarting revokes all existing
sessions; restarting with the same password preserves them. There is no password
reset service: recovery means changing the deployment credential and restarting.

Login issues a fresh opaque token with 256 random bits, stores only its SHA-256
digest server-side, and revokes the previous cookie's session. The host-only
`photodrop_session` cookie has `HttpOnly`, `SameSite=Lax`, and a 12-hour absolute
expiry (no sliding renewal). Logout immediately invalidates it. No authentication
tokens use URLs or localStorage. Sessions persist in SQLite until expiry/logout/
password change; expired rows are pruned on successful login.

All mutations, including login, require a matching `Origin` (or `Referer` fallback
when Origin is absent); absent/null/cross-site origins are rejected. Authenticated
mutations also require `X-CSRF-Token`, obtained from login or the session endpoint
and held only in frontend memory. SameSite cookies supplement these checks.
There is no CORS support. The configured base URL is the expected origin; when
unset, the direct request scheme and Host are used only for origin validation.
Forwarded headers are not trusted. JSON bodies have size limits and reject unknown
fields; authorization and validation always run server-side. Errors are structured
as `{"error":{"code":"...","message":"...","fields":{...}}}` (fields optional).

| Method | Route | Access / purpose |
| --- | --- | --- |
| POST | `/api/admin/login` | Password + same-origin check; creates session |
| GET | `/api/admin/session` | Session required; CSRF token and expiry |
| POST | `/api/admin/logout` | Session + origin + CSRF; revokes session |
| GET / POST | `/api/admin/events` | List / create; admin only |
| GET / PUT / DELETE | `/api/admin/events/{id}` | Read / replace editable fields / delete; admin only |
| GET | `/api/public/events/{public_id}` | Public name/status and open-event description/date/upload limit |

Create/update bodies contain `name`, optional `description`, nullable `event_date`,
required boolean `enabled`, and nullable `expires_at`. IDs/timestamps cannot be
supplied or changed. Validation errors return 422; invalid JSON returns 400;
unauthenticated API calls return 401 and failed CSRF checks return 403. Protected
page requests redirect to `/admin/login`. Public IDs contain 144 cryptographically
random bits, are database-unique, and cannot be modified even by an ordinary SQL
update. SQL queries are parameterized. Admin/API/guest responses are not cached;
security headers restrict script sources, framing, and referrer disclosure.

For production, serve through HTTPS and set `PHOTODROP_BASE_URL` to that HTTPS
origin. This enables `Secure` cookies even when TLS terminates upstream. Direct
HTTP is supported for local development; HTTPS base URLs must be accessed through
that origin for login and mutations to work. PhotoDrop does not terminate TLS
itself or add a reverse-proxy runtime dependency. There is deliberately no rate
limiting, multi-user identity, OAuth, registration, or background processing in
this gate. Use a strong administrator password and restrict admin exposure as
appropriate for your deployment.

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `PHOTODROP_LISTEN_ADDR` | `:8080` | TCP `host:port`; numeric port 1–65535. Use `127.0.0.1:8080` for local-only access. |
| `PHOTODROP_DATA_DIR` | `/data` | Directory for `photodrop.db`; created on startup. Set `./data` for non-container development. |
| `PHOTODROP_BASE_URL` | unset | Public HTTP(S) origin, e.g. `https://photos.example.com`. Used for guest links, CSRF origin, and HTTPS cookie security. |
| `PHOTODROP_ADMIN_PASSWORD` | **required; no default** | Single administrator password, 12–72 bytes, not whitespace-only and without NUL. Changing it and restarting revokes sessions. |
| `PHOTODROP_MAX_FILE_SIZE` | `52428800` | Maximum bytes per image (50 MiB); integer from 1 to 1073741824 (1 GiB). |
| `PHOTODROP_STORAGE_PROVIDER` | `local` | New-upload strategy: `local` or `s3`. Historical assets retain their provider. |

S3 settings, defaults, and credential handling are documented in [Storage](docs/storage.md#configuration)
and included in `.env.example`. Local mode needs no S3 credentials.
See [Gate 4 validation](docs/gate-4-validation.md) for test commands, browser
data-path evidence, and the explicitly unverified live R2 checks.

Explicitly empty listen/data values are invalid. The optional base URL accepts
an empty value or a full origin with an optional trailing slash, but no
credentials, subpath, query, or fragment. Hosting at the origin root is assumed.
Base URL does not configure the listener, TLS, or trust in forwarded headers.
The server returns relative guest links when base URL is unset; the browser
resolves them against its own origin for copying. Configured links never use an
untrusted request Host header. Base URL host casing and default ports are normalized.

## Repository and migrations

```text
cmd/photodrop/       process startup and signal handling
internal/config/    environment parsing and validation
internal/database/  SQLite initialization and transactional migrations
internal/auth/      administrator password and persistent opaque sessions
internal/events/    validation, random public IDs, and explicit SQLite queries
internal/media/     upload authorization, asset lifecycle, statistics, cleanup
internal/storage/   bounded local writes; AWS SDK S3 authorization/verification
internal/testutil/  tiny generated image fixtures for tests
internal/server/    protected APIs/pages, public lookup, health, and shutdown
migrations/         embedded, numbered SQL files
web/                Svelte source, Vite build, and Go embedding
scripts/            Compose smoke checks
.github/workflows/  tests and builds; no publishing
```

`001_init.sql` remains unchanged and creates `schema_migrations`.
`002_events.sql` adds events and the public-ID immutability trigger.
`003_admin_sessions.sql` adds the singleton hashed credential, sessions, and an
expiry index. `004_local_uploads.sql` adds upload sessions, assets, the durable
event-deletion marker, foreign keys, and indexes for real event/status/session/
cleanup queries. `005_s3_storage.sql` adds durable asset provider/target identity,
expected size/type, authorization expiry, browser request identity, and retired
S3-key reconciliation records. Gate 3 assets default to `local`; no files move.
`006_storage_backends.sql` adds immutable named destinations and authoritative
asset/retired-key backend associations. See [backend upgrade instructions](docs/storage-backends.md).
`007_security_abuse.sql` adds event quotas, expiring upload grants, per-session
bounds, and verification timestamps. Legacy grants expire without deleting ready assets.
A fresh install applies all seven. Gate 1, Gate 2, Gate 3, and Gate 4 databases
receive only new migrations, preserving existing events, admin sessions, and history.
Never edit, rename, remove,
or renumber an applied migration. The runner verifies checksums (ignoring CRLF
versus LF), rejects history newer than the binary, and applies pending files
and their history entries atomically in one `BEGIN IMMEDIATE` transaction. A
5-second SQLite busy timeout serializes concurrent startup attempts. A failure
rolls back the pending batch and prevents the HTTP server from starting.

Migration SQL must not contain its own transaction control or operations such
as `VACUUM` that cannot run inside a transaction. Asset rows contain metadata
only; image bytes are never stored in SQLite.

### Upgrade from Gate 1, Gate 2, Gate 3, or Gate 4

Stop the old container and back up `./data` before upgrading. Add the now-required
`PHOTODROP_ADMIN_PASSWORD` to your environment or `.env`, then run
`docker compose up --build -d`. Startup verifies the existing migration checksum,
applies pending migrations through Gate 5, initializes/verifies the administrator
credential, prepares local upload storage, and starts HTTP. There is no automatic
downgrade: older binaries reject the newer schema. To
roll back, stop PhotoDrop and restore the pre-upgrade backup with the old binary.
