# PhotoDrop

PhotoDrop is a self-hosted project for event photo and video collection.
**Gate 2 implements event management:** one administrator can sign in, create and
manage events, and share independent public guest landing pages. The Gate 1
foundation, health endpoint, embedded frontend, and SQLite persistence remain.

**Uploads, media storage, S3, Cloudflare R2, Immich integration, QR generation,
and exports are not implemented.** Guest pages explain that photo sharing will
be available later; there are no upload controls or endpoints.

## Architecture

- One Go executable serves HTTP and the compiled Svelte frontend on port 8080.
- SQLite uses `database/sql` and the CGO-free `modernc.org/sqlite` driver; no ORM.
- SQL migrations and frontend assets are embedded at build time.
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
storage. It contains password hashes, sessions, and event information.

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
The script creates only temporary test events and removes them. It then verifies
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
fetching the event; an already-open guest page updates when reloaded.

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
| GET | `/api/public/events/{public_id}` | Public name/status and open-event description/date only |

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
internal/server/    protected APIs/pages, public lookup, health, and shutdown
migrations/         embedded, numbered SQL files
web/                Svelte source, Vite build, and Go embedding
scripts/            Compose smoke checks
.github/workflows/  tests and builds; no publishing
```

`001_init.sql` remains unchanged and creates `schema_migrations`.
`002_events.sql` adds events and the public-ID immutability trigger.
`003_admin_sessions.sql` adds the singleton hashed credential, sessions, and an
expiry index. A fresh install applies all three. A Gate 1 database automatically
receives only the new migrations on startup, preserving its original history.
Never edit, rename, remove,
or renumber an applied migration. The runner verifies checksums (ignoring CRLF
versus LF), rejects history newer than the binary, and applies pending files
and their history entries atomically in one `BEGIN IMMEDIATE` transaction. A
5-second SQLite busy timeout serializes concurrent startup attempts. A failure
rolls back the pending batch and prevents the HTTP server from starting.

Migration SQL must not contain its own transaction control or operations such
as `VACUUM` that cannot run inside a transaction. There are no media tables or
storage abstractions in this gate.

### Upgrade from Gate 1

Stop the old container and back up `./data` before upgrading. Add the now-required
`PHOTODROP_ADMIN_PASSWORD` to your environment or `.env`, then run
`docker compose up --build -d`. Startup verifies the existing migration checksum,
applies Gate 2, initializes the hashed administrator credential, and starts HTTP.
There is no automatic downgrade: the Gate 1 binary rejects a newer schema. To
roll back, stop PhotoDrop and restore the pre-upgrade backup with the old binary.
