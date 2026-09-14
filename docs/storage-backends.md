# Durable storage backend identity

A backend key names one permanent storage destination, for example `primary-r2`,
`old-r2`, or `aws-prod`. Changing the active key affects new assets only. Existing
assets keep their original backend even when a pending upload is finalized after
a configuration change. The guest API, local streaming, direct PUT, progress,
verification, retry policy, and one-container production architecture are unchanged.

## Data model

Migration `006_storage_backends.sql` adds `storage_backends`: ID, unique key,
provider type, endpoint, bucket, region, path-style flag, prefix, and creation time.
It adds foreign-key associations from both `assets` and `s3_cleanup`. The latter
survives event deletion so late objects can be reconciled against their original
destination. Indexes cover the backend associations.

The built-in `local-default` backend derives its filesystem root from
`PHOTODROP_DATA_DIR`; it stores no absolute filesystem path and needs no new local
configuration. Every newly created asset receives its backend ID immediately.
Database triggers prohibit changing an established association, altering a
backend record, or deleting/reusing its identity. Even an unused registered key
is immutable: choose a new key for a different destination.

`storage_backend_id` is authoritative. Gate 4's `storage_provider` and
`storage_target` columns remain as compatibility/legacy migration metadata.
They do not select the backend for verification, refresh, deletion, or cleanup.
Their fingerprint is consulted only when binding an unassociated Gate 4 row.
Migrations 001–005 are unchanged.

Only non-secret addressing fields are persisted. Access-key IDs, secret keys,
session tokens, administrator passwords, and presigned URLs are not stored in
backend records. Credentials can rotate without changing backend identity.

## One S3 destination

The existing S3 variables still work; add a stable key:

```dotenv
PHOTODROP_STORAGE_PROVIDER=s3
PHOTODROP_STORAGE_BACKEND_KEY=primary-r2
PHOTODROP_S3_BUCKET=photos
PHOTODROP_S3_ENDPOINT=https://ACCOUNT.r2.cloudflarestorage.com
PHOTODROP_S3_REGION=auto
PHOTODROP_S3_PATH_STYLE=false
PHOTODROP_S3_PREFIX=photodrop/
PHOTODROP_S3_ACCESS_KEY_ID=your-access-key
PHOTODROP_S3_SECRET_ACCESS_KEY=your-secret
PHOTODROP_S3_PRESIGN_TTL=10m
```

Keys are 1–63 lowercase letters/digits with single separating hyphens, starting
with a letter. Underscores, uppercase, whitespace, paths, and punctuation are
rejected. `local-default` is reserved. Bucket, endpoint, prefix and other S3
validation remains as described in [Storage](storage.md#configuration).

On startup, an unknown key is registered. An existing key must match all its
stored addressing fields, including path style. Equivalent host casing, outer
prefix slashes, and endpoint trailing slashes normalize before comparison.
Endpoint/bucket/region/path-style/prefix changes under the same key fail clearly
and instruct the administrator to choose a new key. Nothing is silently repointed.

`PHOTODROP_STORAGE_PROVIDER=local` always selects the built-in local backend for
new uploads. If single-S3 variables remain configured for historical access, keep
their S3 backend key as well; it names that configured S3 destination even while
local is active. Without S3 settings, the key can be omitted entirely.

## Several historical S3 destinations

Use `PHOTODROP_S3_BACKENDS` to enumerate up to 32 named credential/configuration
sets. For each validated key, uppercase it and replace hyphens with underscores
to obtain its environment prefix. For example `primary-r2` uses
`PHOTODROP_S3_PRIMARY_R2_`. This mapping cannot collide because underscores are
not allowed in keys.

```dotenv
PHOTODROP_STORAGE_PROVIDER=s3
PHOTODROP_STORAGE_BACKEND_KEY=new-r2
PHOTODROP_S3_BACKENDS=primary-r2,new-r2

PHOTODROP_S3_PRIMARY_R2_BUCKET=photos-old
PHOTODROP_S3_PRIMARY_R2_ENDPOINT=https://OLD_ACCOUNT.r2.cloudflarestorage.com
PHOTODROP_S3_PRIMARY_R2_ACCESS_KEY_ID=old-access-key
PHOTODROP_S3_PRIMARY_R2_SECRET_ACCESS_KEY=old-secret

PHOTODROP_S3_NEW_R2_BUCKET=photos-new
PHOTODROP_S3_NEW_R2_ENDPOINT=https://NEW_ACCOUNT.r2.cloudflarestorage.com
PHOTODROP_S3_NEW_R2_ACCESS_KEY_ID=new-access-key
PHOTODROP_S3_NEW_R2_SECRET_ACCESS_KEY=new-secret
```

Named backends accept the same suffixes and defaults as the existing S3 fields:
`BUCKET`, `ENDPOINT`, `REGION` (`auto`), `PATH_STYLE` (`false`), `PREFIX`
(`photodrop/`), `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, optional `SESSION_TOKEN`,
and `PRESIGN_TTL` (`10m`, bounded 1m–15m). Specify the real AWS region for AWS S3.
Historical addressing settings must match those originally registered, including
any non-default prefix/path-style/region. TTL is a runtime policy, not identity.

The single-S3 syntax may coexist with named historical backends, but do not
define the same key twice. When using only named definitions, leave ordinary
`PHOTODROP_S3_BUCKET` and credential variables empty. Exactly one destination
is active; an active S3 key must have a complete runtime credential set.

For Docker Compose:

1. Keep the administrator password and active provider/key in `.env`.
2. Copy `.env.backends.example` to `.env.backends`, replace its example values,
   and list the named backends to configure. Keep this file private. It is ignored
   by Git and excluded from the Docker build context. Single-quote dotenv values
   containing `$` or `#`.
3. Run `docker compose -f compose.yml -f compose.backends.yml up --build -d`.

The override only passes this env file into the same production container. Normal
`docker compose up` still needs no backend file for local-only installations.
For other container hosts, supply the equivalent named environment variables.

To temporarily withhold historical credentials, remove that key from
`PHOTODROP_S3_BACKENDS` rather than leaving a listed definition incomplete.
The database keeps its identity. Browsing and `/healthz` continue to work, but
operations requiring that backend retain retry state and log a safe message such
as `storage backend "old-r2" is not configured for this runtime`. Guest errors
remain generic. Restore the matching definition to resume deletion/cleanup.

## Upgrade from Gate 4

Stop PhotoDrop and back up `/data` before upgrading. Local-only databases migrate
automatically to `local-default` without extra configuration.

If Gate 4 S3 assets **or retired S3 cleanup keys** exist, supply their original
S3 configuration and a chosen stable key on the first upgraded startup. An example
is the existing single-S3 variables plus `PHOTODROP_STORAGE_BACKEND_KEY=primary-r2`.
If several Gate 4 fingerprints exist, supply a named configuration for each.

Migration 006 preserves the legacy fingerprints with initially unbound S3
associations. Before cleanup or HTTP serving, startup matches every legacy
fingerprint to exactly one explicitly configured backend and binds all rows in
one transaction. Gate 4 fingerprints cover endpoint, region, bucket and prefix;
provide the original path-style setting too, which becomes immutable in the new
record. Ambiguous aliases, a different bucket, or missing original configuration
cause an actionable startup failure. The application never guesses that all old
objects belong to today's active destination.

The schema migration and startup reconciliation are separate transactions. A
reconciliation failure can leave schema version 6 installed, but leaves the
legacy associations and new registrations uncommitted. Correct configuration
and restart to finish. Restore the pre-upgrade backup to run an older binary.
Once all rows are bound, unused historical credentials may be omitted without
blocking startup. Backends and asset associations survive process restarts.

## Operations and limits

For every operation, PhotoDrop resolves the asset/cleanup row's backend ID to
the persisted record and its matching runtime client. No current-provider or
current-bucket fallback exists. Deleting an event containing local, S3-A and S3-B
assets dispatches each deletion separately. A missing backend or remote failure
keeps the event closed and deletion retryable; necessary metadata is retained.

Startup cleanup retains Gate 4's one-hour staleness rules, two-second budget,
bounded batches, and recorded-key-only deletion. Missing historical credentials
defer the relevant cleanup. Gate 5 also runs bounded cleanup every five minutes, and
retired-key records are retained. CSP allows the exact upload origins of the
configured backends so historical pending assets can refresh in the existing UI.

This patch does not move/copy media, provide multiple active destinations, create
a backend/credential-management UI, or add an encryption system. It does not
add media features. Gate 5 abuse controls are described in [security.md](security.md). Live R2 testing remains
unverified without credentials; deterministic tests and isolated Compose/browser
tests cover backend routing separately from live-provider interoperability.

See the [validation report](storage-backends-validation.md) for commands, migration
coverage, the Compose provider-switch scenarios, and browser regression evidence.
