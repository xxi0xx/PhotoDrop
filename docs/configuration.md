# Configuration reference

**All changes require restarting/recreating PhotoDrop.** Export reads environment
when invoked. `.env` is Compose interpolation, not automatically loaded by Go.
Compose forwards its listed variables. Named S3/Immich variables need the
`compose.backends.yml` / `.env.backends` overlay or your own `env_file` overlay.
Secrets belong in private runtime configuration, never build arguments.

Unless stated otherwise, settings are optional and non-secret. These are actual
application defaults. Empty required values fail startup.

## Core and authentication

| Variable | Default / required | Format and effect | Sensitivity |
| --- | --- | --- | --- |
| `PHOTODROP_LISTEN_ADDR` | `:8080` | TCP host:port, numeric port 1–65535; Compose fixes `:8080` | Internal address |
| `PHOTODROP_DATA_DIR` | `/data` | Nonempty filesystem path; Compose fixes `/data` | Private state path / local backend root |
| `PHOTODROP_BASE_URL` | empty | HTTP(S) origin, optional trailing slash, no subpath/userinfo/query/fragment | Public; links, origin checks, secure cookies/HSTS |
| `PHOTODROP_ADMIN_AUTH` | `password` | Exact `password`, `oidc`, or `password+oidc`; selects enabled login methods | Policy |
| `PHOTODROP_ADMIN_PASSWORD` | Required in `password` and `password+oidc`; ignored in `oidc` | 12–72 bytes, not whitespace-only, no NUL; change + restart in a password-enabled mode revokes all local sessions | Secret |
| `PHOTODROP_MAX_FILE_SIZE` | `52428800` | Integer bytes 1–1073741824 | Public limit |

No separate admin username, session-secret, database URL or TLS-certificate
variable exists. Event quotas are set in the UI, not environment.

## Native OIDC administrator login

OIDC modes require `PHOTODROP_BASE_URL`: HTTPS public origin, HTTP only for
localhost/loopback development. Callback is exactly
`${PHOTODROP_BASE_URL}/api/admin/oidc/callback`, never derived from request headers.
All environment changes require restart; these settings do not affect storage or
Immich backend identity. Password mode ignores OIDC settings and contacts no provider.

| Variable | Default / required in OIDC modes | Format and effect | Sensitivity |
| --- | --- | --- | --- |
| `PHOTODROP_OIDC_ISSUER` | Required | Exact absolute HTTPS URL, paths/trailing slash preserved; no userinfo/query/fragment; HTTP only on loopback | Provider address |
| `PHOTODROP_OIDC_CLIENT_ID` | Required | Nonempty client ID, at most 4096 bytes, no control characters | Internal identifier |
| `PHOTODROP_OIDC_CLIENT_SECRET` | Required | Confidential client secret, at most 4096 bytes, no control characters | Secret |
| `PHOTODROP_OIDC_ALLOWED_SUBJECTS` | Empty; one allowlist required | Comma-separated exact stable `sub` values, trimmed around commas; at most 64 entries, 512 bytes each, 8192 total; no empty entries/control characters | Private authorization policy |
| `PHOTODROP_OIDC_ALLOWED_GROUPS` | Empty; one allowlist required | Same list format; exact case-sensitive group names | Private authorization policy |
| `PHOTODROP_OIDC_GROUPS_CLAIM` | `groups` | Top-level ID-token JSON claim name, at most 256 bytes, no whitespace/control characters; array of strings required when present and group authorization enabled | Claim mapping |
| `PHOTODROP_OIDC_SCOPES` | `openid profile email` | Single-space-separated OAuth scope tokens, at most 1024 bytes; printable ASCII excluding quote/backslash; `openid` always added, duplicates removed | Requested claims |

Subject allowlist **OR** any allowed group grants the same PhotoDrop administrator
access. Email/name is never an authorization boundary. Malformed group claims fail
closed; a missing group claim cannot satisfy group authorization. Claims must be
in the ID token, not only UserInfo. See [OIDC and Authentik setup](oidc.md).

## Security and grants

| Variable | Default | Accepted format / effect | Sensitivity |
| --- | --- | --- | --- |
| `PHOTODROP_UPLOAD_SESSION_TTL` | `2h` | Go duration 1m–24h; future grants | Operational |
| `PHOTODROP_UPLOAD_SESSION_MAX_ASSETS` | `100` | Integer 1–1000; future grants | Operational |
| `PHOTODROP_UPLOAD_SESSION_MAX_BYTES` | `5368709120` | Integer 1–1099511627776; future grants | Operational |
| `PHOTODROP_RATE_LIMIT_MULTIPLIER` | `1` | Integer 1–100 | Operational |
| `PHOTODROP_RATE_LIMIT_DISABLED` | `false` | Exactly true/false; leave false in production | Security-critical |
| `PHOTODROP_TRUSTED_PROXY_CIDRS` | empty | Up to 32 comma-separated IPv4/IPv6 CIDRs | Security-critical |
| `PHOTODROP_TURNSTILE_SITE_KEY` | empty | Paired with secret; non-whitespace key up to 256 bytes | Public |
| `PHOTODROP_TURNSTILE_SECRET_KEY` | empty | Paired with site key; requires BASE_URL | Secret |

Action is fixed at `photodrop_upload`; hostname derives from BASE_URL. There is
no production verifier bypass. See [security](security.md) for rates and recovery.

## Storage and historical backends

| Variable | Default / required | Format and interaction | Sensitivity |
| --- | --- | --- | --- |
| `PHOTODROP_STORAGE_PROVIDER` | `local` | local or s3; changes new uploads only | Operational |
| `PHOTODROP_STORAGE_BACKEND_KEY` | Local: `local-default`; S3: required | 1–63 lowercase letters/digits with single hyphens, starts with letter | Stable identifier |
| `PHOTODROP_S3_BACKENDS` | empty | Up to 32 comma-separated keys, no spaces/duplicates/reserved local-default | Identifiers |
| `PHOTODROP_S3_BUCKET` | Required when configured | Private bucket, 3–63 characters, not IP address | Addressing |
| `PHOTODROP_S3_REGION` | `auto` | Lowercase region identifier; actual AWS region for AWS | Addressing |
| `PHOTODROP_S3_ENDPOINT` | empty | HTTP(S) origin; empty uses AWS SDK resolution; HTTPS publicly | Addressing |
| `PHOTODROP_S3_PATH_STYLE` | `false` | Exactly true/false | Addressing |
| `PHOTODROP_S3_PREFIX` | `photodrop/` | Slash-separated letters/digits/underscore/hyphen, up to 200 characters before trailing slash; empty permits bucket root | Addressing |
| `PHOTODROP_S3_ACCESS_KEY_ID` | Required when configured | Nonblank, no NUL/newline | Sensitive credential identifier |
| `PHOTODROP_S3_SECRET_ACCESS_KEY` | Required when configured | Nonblank, no NUL/newline | Secret |
| `PHOTODROP_S3_SESSION_TOKEN` | empty | Optional temporary token, no NUL/newline | Secret |
| `PHOTODROP_S3_PRESIGN_TTL` | `10m` | Go duration 1m–15m | Runtime policy |

Every S3 suffix also exists as `PHOTODROP_S3_<KEY>_<SUFFIX>` for each S3_BACKENDS
key; uppercase it and replace hyphens with underscores. Defaults, requirements,
and sensitivities are identical. Example: `PHOTODROP_S3_OLD_R2_BUCKET` for `old-r2`.
Do not define the same key through both syntaxes.

Endpoint, bucket, region, path style, and prefix become immutable once registered.
A new destination needs a new key. Credentials/TTL can rotate under the same key.
Local storage resolves from DATA_DIR. Keep historical credentials/addressing for
reads, recovery, delete, and export. Switching providers never moves bytes.
See [backend configuration](storage-backends.md).

## Immich and operational commands

| Variable | Default / required | Format and effect | Sensitivity |
| --- | --- | --- | --- |
| `PHOTODROP_IMMICH_TARGET` | empty | Active target key; backend-key grammar | Identifier |
| `PHOTODROP_IMMICH_TARGETS` | Active target when omitted | Up to 32 comma-separated unique keys | Identifiers |
| `PHOTODROP_IMMICH_<KEY>_URL` | empty | HTTP(S) origin used for server API requests; immutable identity | Internal addressing |
| `PHOTODROP_IMMICH_<KEY>_PUBLIC_URL` | empty / optional | HTTP(S) browser origin without credentials, path, query or fragment; enables album navigation; restart required | Public addressing, never secret |
| `PHOTODROP_IMMICH_<KEY>_API_KEY` | empty | Up to 4096 bytes, no NUL/newline; required for operations | Secret |

Use uppercase/underscore key substitution. Historical targets can omit URL/key and
remain unavailable without erasing identity. Rotate keys for the same server/account;
another server needs a new target key. The optional PUBLIC_URL is runtime-only,
may change without changing target identity, and is never used for API requests.
Without it, the admin shows no browser album link.
See [Immich](export-immich.md) for API permissions.

Automatic import is not an environment variable. Its `auto_import` setting is
stored per event/target binding, defaults off, and is changed through the admin UI
or protected API without restarting. It does not change immutable target identity.
It selects ready, never-selected media in background batches; failed imports still
require manual retry, and guest completion is independent of Immich availability.

Export reads DATA_DIR and storage configuration only; there are no additional
export environment variables. Version is build-time metadata. `PHOTODROP_IMAGE`
is **Compose-only**: default `photodrop:local`, or a released GHCR tag/digest.
`PHOTODROP_TEST_*` and browser-fixture switches are not production configuration.

## Event upload policy

The administrator editor can calculate typed storage from count × individual
size, or save a custom/unlimited value. This adds no environment variable or
persistent mode: only existing explicit integer-byte event fields are saved.

Per-event photo/video count, individual size and storage limits are stored in SQLite, not environment variables. `PHOTODROP_MAX_FILE_SIZE` remains the hard server ceiling. See [event limits and API fields](quotas.md) for independent buckets, overall safety ceilings and generic upload-session limits.
