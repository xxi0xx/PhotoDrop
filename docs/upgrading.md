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
applies new migrations transactionally.

## Upgrading to multi-host accounts (next release)

Migration 013 adds hosts and event ownership. It needs no configuration change:

- **Everyone signs in again.** Sessions now name their host, so the migration
  resets them. The password credential is kept.
- **Sign in as the administrator first.** Host 1 is the existing administrator and
  owns every event created before the upgrade. Password sign-in always uses host 1.
  The first OIDC sign-in by an identity in the admin lists claims host 1; any later
  admin identity becomes a separate host. So, with OIDC enabled, sign in with your
  own admin identity before anyone else does.
- **Add hosts when ready.** Set `PHOTODROP_OIDC_HOST_GROUPS` (or `_HOST_SUBJECTS`)
  and recreate PhotoDrop. Hosts see only their own events. Without host lists,
  PhotoDrop behaves as before.
- **Immich accounts.** Migration 014 turns each Immich target that has bindings
  into the administrator's config account and attaches existing bindings to it.
  Existing albums, imports and automatic import keep working with no steps. A
  target with a URL but no API key becomes link-only.
- **Immich account linking.** Hosts can link their own Immich accounts (Account
  page). It needs Immich 3.4.2 with OAuth through the same identity provider, the
  redirect URI `<PHOTODROP_BASE_URL>/api/admin/destinations/immich/callback` on the
  Immich client, and a secrets key. See
  [linking](export-immich.md#linking-a-hosts-own-immich-account).
- **Optional secrets key.** Linking hosts' own Immich accounts needs
  `PHOTODROP_SECRETS_KEY_FILE` (see [configuration](configuration.md#secrets-key-for-linked-accounts)).
  Back the key file up separately from `/data`. Without it, PhotoDrop runs as before.

Rollback after the upgrade needs the stopped pre-upgrade backup: an older binary
refuses a database with migration 013 applied.

## v1.2.0 to v1.2.1

Dependency and security update; no migrations or configuration changes. go-jose
4.1.6 rejects JWKs with RSA keys larger than 4096 bits, so with OIDC enabled confirm
your provider's signing keys fit and test a sign-in after upgrading.

## v1.1.0 to v1.2.0

No migrations; data and configuration are unchanged. 1.2.0 accepts
`PHOTODROP_MAX_FILE_SIZE` up to 2147483648 (2 GiB) and allows 30 minutes per file
transfer. Raise the value only after the 1.2.0 image is running: 1.1.0 refuses to
start with a value above 1073741824. Rollback to 1.1.0 needs a value within its limit.

## v1.0.0 to v1.1.0

Migration 010 adds automatic Immich import state:
`immich_event_imports.auto_import INTEGER NOT NULL DEFAULT 0 CHECK (auto_import IN (0,1))`
and a partial index on enabled, ready bindings. Migrations 001–009 are unchanged.
Existing bindings remain manual-only; event/public IDs, album IDs/markers, target
identity and import history are preserved. Enable automatic import explicitly
after checking a binding. Back up before this schema upgrade; use the pre-upgrade
backup and old image for rollback, not an older binary against the upgraded data.

Migration 011 adds the short-lived `oidc_transactions`
table and expiry index. Migrations 001–010 and the `admin_sessions` schema are
unchanged. With no ADMIN_AUTH setting, an upgraded deployment remains
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
| `ghcr.io/xxi0xx/photodrop:1.2.1` | Full release tag; workflow refuses overwrite |
| `:1.2` | Moving stable patch line |
| `:1` | Moving stable major line |
| `:latest` | Most recently promoted stable release, never prerelease |
| `:1.2.0-rc.1` | Exact prerelease, no moving aliases |
| `ghcr.io/xxi0xx/photodrop@sha256:...` | Immutable manifest digest copied from registry/release |

Prefer a full version or digest for controlled upgrades. Blindly tracking latest
is not an upgrade policy. Commit SHAs identify source; separate sha-* image tags
are not published. Registry administrators can delete/retag artifacts; record digests.

## Typed event quotas

Migration 012 adds six nullable event limits and nullable asset `media_class`; migrations 001–011 are unchanged. Old overall `max_assets`/`max_bytes` are preserved and no limits are split or inferred. Supported ready MIME and pending expected MIME backfill photo/video; ambiguous pending rows stay NULL and must acquire current typed capacity at completion. New limits take effect when explicitly edited. Automatic storage calculation is editor-only: existing NULL/custom totals stay unchanged unless the administrator opts into calculation; exact formula-equal totals initialize linked. No migration beyond 012 is required. Stop and back up before upgrading; restore the pre-upgrade backup and old image for rollback. See [quota semantics](quotas.md).
