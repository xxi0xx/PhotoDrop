# PhotoDrop

PhotoDrop is intended to become a self-hosted event photo and video collection
system. **Gate 1 provides only the application foundation:** an embedded Svelte
landing page, an HTTP health endpoint, and a persistent SQLite database with
automatic migrations.

**Events, uploads, S3, Cloudflare R2, authentication, QR codes, exports, and Immich
integration are not implemented.**

## Architecture

- One Go executable serves HTTP and the compiled Svelte frontend on port 8080.
- SQLite uses `database/sql` and the CGO-free `modernc.org/sqlite` driver; no ORM.
- SQL migrations and frontend assets are embedded at build time.
- One production container and one `/data` volume; no external database, Redis,
  Node.js runtime, or reverse proxy is required.
- Structured JSON logs go to stdout. SIGINT/SIGTERM drains HTTP requests for up
  to 10 seconds, then closes SQLite. A failed startup exits with a useful error.

The frontend has no client-side router. `/` serves the application, static assets
are served directly, and unknown paths return 404. No SPA fallback is needed.

## Prerequisites

- Go 1.26 or newer.
- Node.js 22.12 or newer and npm (Node 22 is used in CI and Docker builds).
- Docker with Compose v2 for container deployment. Host Go and Node are not
  needed when building with Docker.

## Local development

From the repository root, build the frontend **before any Go test or build**:

```sh
npm ci --prefix web
npm run build --prefix web
```

Run on macOS/Linux:

```sh
PHOTODROP_DATA_DIR=./data go run ./cmd/photodrop
```

Run in Windows PowerShell:

```powershell
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
to `http://127.0.0.1:8080`. If you change the backend port, update this development
proxy in `web/vite.config.js`. The Vite server is never included in production.

## Tests and production build

These commands work from a fresh checkout, in this order:

```sh
npm ci --prefix web
npm run check --prefix web
npm run build --prefix web
go test ./...
go vet ./...
gofmt -l cmd internal migrations web/embed.go
go build -trimpath -ldflags="-s -w" -o bin/photodrop ./cmd/photodrop
```

The formatting command must print nothing. `npm run check` performs strict
TypeScript/Svelte and accessibility checks and fails on warnings. Go tests cover
configuration, SQLite creation and persistence, migration execution/rollback/
history/concurrency, HTTP health, embedded JS/CSS, 404s, and draining active
requests during shutdown. CI also runs `go test -race ./...` on Linux.

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

```sh
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
local storage. To back up this foundation, stop the service and copy `./data`.

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

Run the container integration check on Linux/macOS (or a shell with Docker and
curl available). It uses the repository's `./data` and port 8080:

```sh
sh scripts/smoke-compose.sh
docker compose down
```

It builds the image, verifies HTTP and non-root execution, checks that Node/npm/
Go are absent from the runtime, stops via SIGTERM, compares the stopped database
before/after restart, and checks clean exit codes. It preserves persistent data
and leaves the container stopped. CI runs this script; it never publishes images.

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `PHOTODROP_LISTEN_ADDR` | `:8080` | TCP `host:port`; numeric port 1–65535. Use `127.0.0.1:8080` for local-only access. |
| `PHOTODROP_DATA_DIR` | `/data` | Directory for `photodrop.db`; created on startup. Set `./data` for non-container development. |
| `PHOTODROP_BASE_URL` | unset | Optional public HTTP(S) origin, e.g. `https://photos.example.com`. Validated and normalized; no link-generation behavior exists in Gate 1. |

Explicitly empty listen/data values are invalid. The optional base URL accepts
an empty value or a full origin with an optional trailing slash, but no
credentials, subpath, query, or fragment. Hosting at the origin root is assumed.
Base URL does not configure the listener, TLS, or trust in forwarded headers.
Reverse proxy setup is outside Gate 1.

## Repository and migrations

```text
cmd/photodrop/       process startup and signal handling
internal/config/    environment parsing and validation
internal/database/  SQLite initialization and transactional migrations
internal/server/    HTTP routes, health probe, and graceful shutdown
migrations/         embedded, numbered SQL files
web/                Svelte source, Vite build, and Go embedding
scripts/            Compose smoke checks
.github/workflows/  tests and builds; no publishing
```

`001_init.sql` creates only `schema_migrations`. Add future migrations as
`002_description.sql`, `003_description.sql`, etc. Never edit, rename, remove,
or renumber an applied migration. The runner verifies checksums (ignoring CRLF
versus LF), rejects history newer than the binary, and applies pending files
and their history entries atomically in one `BEGIN IMMEDIATE` transaction. A
5-second SQLite busy timeout serializes concurrent startup attempts. A failure
rolls back the pending batch and prevents the HTTP server from starting.

Migration SQL must not contain its own transaction control or operations such
as `VACUUM` that cannot run inside a transaction. There are no domain tables or
media-storage abstractions in this gate.

The module is locally named `photodrop` until a public repository location is
chosen; no hosting provider or external service is assumed.
