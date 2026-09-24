# Controlled upgrades

Read release notes and [back up the stopped deployment](backup-restore.md). Record
`docker compose exec --user 10001 photodrop photodrop version`, image digest, Compose
files, and private historical backend/Immich settings. After the target exists,
set PHOTODROP_IMAGE to its full version in `.env`:

```sh
docker compose pull photodrop
docker compose stop
docker compose up --no-build --wait -d
docker compose ps
curl --fail http://localhost:8080/healthz
docker compose exec --user 10001 photodrop photodrop version
```

Use the same overlays throughout. Check logs privately, sign in, verify an existing
event/public link/counts, then test upload/export. Startup validates checksums and
applies new migrations transactionally. Gate 8 adds no migration; 001–009 are unchanged.

Migrations move forward. Never edit, rename, remove or renumber a released migration.
Downgrading may fail or be unsafe. Supported rollback: stop the new app, restore a
complete pre-upgrade backup into a separate directory with matching configuration
and old image, verify, then switch traffic. Schema rollback cannot undo remote deletions.

| Image reference | Meaning |
| --- | --- |
| `ghcr.io/xxi0xx/photodrop:1.0.0` | Full release tag; workflow refuses overwrite |
| `:1.0` | Moving stable patch line |
| `:1` | Moving stable major line |
| `:latest` | Most recently promoted stable release, never prerelease |
| `:1.1.0-rc.1` | Exact prerelease, no moving aliases |
| `ghcr.io/xxi0xx/photodrop@sha256:...` | Immutable manifest digest copied from registry/release |

Prefer a full version or digest for controlled upgrades. Blindly tracking latest
is not an upgrade policy. Commit SHAs identify source; separate sha-* image tags
are not published. Registry administrators can delete/retag artifacts; record digests.
