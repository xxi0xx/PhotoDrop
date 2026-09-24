# Installation

Use Docker Engine/Desktop with Compose v2 (`docker compose version`), working
registry access and persistent local disk. Release targets are Linux amd64/arm64.
Do not put SQLite on network storage or run two servers on one data directory.

## Before stable images are published

```sh
git clone https://github.com/xxi0xx/PhotoDrop.git
cd PhotoDrop
cp .env.example .env
```

Privately edit `.env`: supply `PHOTODROP_ADMIN_PASSWORD` (12–72 bytes; no default).
Single-quote dotenv values containing `$` or `#`. Protect the file (`chmod 600 .env`
on Linux). Leave `PHOTODROP_BASE_URL` empty for local HTTP. For public use set the
exact HTTPS origin, such as `https://drop.example.com`, and configure [HTTPS](reverse-proxy.md).

```sh
docker compose config --quiet
docker compose up --build --wait -d
docker compose ps
curl --fail http://localhost:8080/healthz
docker compose exec --user 10001 photodrop photodrop version
```

Health returns `{"status":"ok"}` after initialization/migrations; it is not a
continuous remote-storage/Immich check. Source builds report `dev`. Open
`/admin/login` on the configured origin, sign in, create an event, and save it.
Set finite photo/byte quotas before public sharing if you need a resource ceiling.
**Share event** copies its link and downloads a QR PNG. Open the link as a guest
and upload a small supported image.

## Released image (only after publication)

Use Compose/example files from the matching release checkout. Set in `.env`:

```dotenv
PHOTODROP_IMAGE=ghcr.io/xxi0xx/photodrop:1.0.0
```

Run `docker compose pull photodrop`, then `docker compose up --no-build --wait -d`.
The initial image does not exist until the owner completes the release process.
Do not use `up --build` with a release image name: it tags a local build with that name.

## Persistent state and network

The default mount is `./data:/data`, containing `photodrop.db`, SQLite sidecars when
present, and `uploads/`. The entrypoint prepares only the top-level directory as
UID/GID 10001 and mode 0700, then executes the non-root application. It does not
recursively change ownership. Existing files must be writable by 10001; Docker
Desktop modes may differ from Linux. A custom Docker `user` skips preparation.
Use `--user 10001` with `docker compose exec`, which bypasses the entrypoint.

One production service is required. Storage, proxy, Turnstile, and Immich are
optional external services. Compose publishes HTTP port 8080: firewall it or bind
it to loopback if a local proxy is the only intended caller. Named environment
variables require an [overlay](configuration.md); adding them to `.env` alone
does not forward them. `docker compose down` preserves the bind mount. Follow
[backup/restore](backup-restore.md) before deleting or replacing any persistent state.
