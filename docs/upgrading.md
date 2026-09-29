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
applies new migrations transactionally. Unreleased v1.1 Phase 3 adds migration 010:
`immich_event_imports.auto_import INTEGER NOT NULL DEFAULT 0 CHECK (auto_import IN (0,1))`
and a partial index on enabled, ready bindings. Migrations 001–009 are unchanged.
Existing bindings remain manual-only; event/public IDs, album IDs/markers, target
identity and import history are preserved. Enable automatic import explicitly
after checking a binding. Back up before this schema upgrade; use the pre-upgrade
backup and old image for rollback, not an older binary against the upgraded data.

Unreleased Phase 4 adds migration 011, only the short-lived `oidc_transactions`
table and expiry index. Migrations 001–010 and the `admin_sessions` schema are
unchanged. With no ADMIN_AUTH setting, an upgraded Phase 3 deployment remains
password-only; its bcrypt hash and unexpired sessions survive ordinary restart.
Switching to `password+oidc` preserves them, and `oidc` requires no password but
disables its endpoint even if a historical credential remains. Test SSO before
removing the password fallback. A changed password in a password-enabled mode
still revokes all sessions. Restore the stopped pre-upgrade backup with the old
image for rollback; do not remove migration 011 manually. See [OIDC](oidc.md).

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

## Phase 4.5 event quotas

Migration 012 adds six nullable event limits and nullable asset `media_class`; migrations 001–011 are unchanged. Old overall `max_assets`/`max_bytes` are preserved and no limits are split or inferred. Supported ready MIME and pending expected MIME backfill photo/video; ambiguous pending rows stay NULL and must acquire current typed capacity at completion. New limits take effect when explicitly edited. Stop and back up before upgrading; restore the pre-upgrade backup and old image for rollback. See [quota semantics](quotas.md).
