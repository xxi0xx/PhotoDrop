# Storage modes

`PHOTODROP_STORAGE_PROVIDER=local` is the default. Guests stream images through
PhotoDrop into `/data/uploads`; Gate 3's limits, permissions, and cleanup apply.

`PHOTODROP_STORAGE_PROVIDER=s3` changes the path for new images: PhotoDrop accepts
small JSON control requests; the browser PUTs each image directly to the private
object-store origin. PhotoDrop verifies the object before marking it ready.
The normal deployment remains one PhotoDrop container and one `/data` volume
for SQLite and any historical local files. No SDK is added to the browser.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `PHOTODROP_STORAGE_PROVIDER` | `local` | `local` or `s3`; affects new uploads only. |
| `PHOTODROP_STORAGE_BACKEND_KEY` | `local-default` in local mode | Stable S3 destination name, required for the single-S3 syntax and active S3 selection. Never reuse a key for a different destination. |
| `PHOTODROP_S3_BACKENDS` | empty | Optional comma-separated named runtime backends; see [backend configuration](storage-backends.md). |
| `PHOTODROP_S3_BUCKET` | empty | Private bucket; required for a configured S3 backend. |
| `PHOTODROP_S3_REGION` | `auto` | Use `auto` for R2; the actual AWS region for AWS S3. |
| `PHOTODROP_S3_ENDPOINT` | empty | Optional HTTP(S) origin. Empty uses the SDK's AWS endpoint resolution. Use HTTPS outside local development. No credentials, path, query, or fragment. |
| `PHOTODROP_S3_ACCESS_KEY_ID` | empty | Server-side access key. |
| `PHOTODROP_S3_SECRET_ACCESS_KEY` | empty | Server-side secret key. |
| `PHOTODROP_S3_SESSION_TOKEN` | empty | Optional temporary-credential token. Its expiry may shorten URL validity. |
| `PHOTODROP_S3_PATH_STYLE` | `false` | Set `true` for providers requiring path-style addressing, such as a local MinIO endpoint. |
| `PHOTODROP_S3_PREFIX` | `photodrop/` | Optional prefix. Outer slashes are normalized; segments permit letters, digits, `_` and `-`. Empty means bucket root; traversal/empty internal segments are rejected. |
| `PHOTODROP_S3_PRESIGN_TTL` | `10m` | Go duration from `1m` through `15m`. |
| `PHOTODROP_MAX_FILE_SIZE` | `52428800` | Exact expected bytes must be positive and within this limit; default 50 MiB. |

Credentials are not needed for a local-only deployment. A configured S3 backend
requires its bucket and both keys, even if local is currently active, so partial
credential configurations fail clearly. Credentials, SDK errors, and presigned
URLs are excluded from normal application logs and database records. Standard
presigned URLs necessarily contain the signing access-key identifier and any
temporary security token as part of their narrowly scoped authorization; never
share them or treat them as public links. The secret signing key stays on the server.

Configuration is checked without test writes or bucket mutations. `/healthz`
has no live S3 dependency. Storage operations return safe errors during outages.
Cleanup has a two-second startup budget so a remote outage does not prevent the
admin/public pages from starting.

## Private bucket and permissions

Create/select a private bucket. PhotoDrop needs PUT, HEAD, ranged GET, and DELETE
permissions for its bucket/prefix. Use bucket-scoped credentials when supported;
account-wide administration access is unnecessary. The provider must enforce
conditional `PutObject` with `If-None-Match: *`, and support GET byte ranges and
`If-Match`. AWS S3 and R2 document these operations. A provider that ignores the
conditional header is not a supported secure deployment target.

No public bucket, object listing endpoint, public GET URL, or download link is
needed. PhotoDrop does not create buckets or edit their access/CORS policies.

## Cloudflare R2 setup

1. In the R2 dashboard, create or select a private bucket. Keep public access off.
2. Create S3-compatible credentials with Object Read & Write permissions scoped
   to that bucket. Obtain the access-key ID and secret; store them privately.
3. Copy the account's S3 API endpoint from R2. Configure the example below using
   the actual bucket and endpoint. PhotoDrop has no R2 account-ID setting.
4. Configure the bucket's CORS policy for PhotoDrop's public origin as below.
5. Start PhotoDrop and validate a browser upload, finalization, totals, and
   deletion. SDK/CLI success alone does not prove browser CORS works.

```dotenv
PHOTODROP_STORAGE_PROVIDER=s3
PHOTODROP_STORAGE_BACKEND_KEY=primary-r2
PHOTODROP_BASE_URL=https://photos.example.com
PHOTODROP_S3_BUCKET=photodrop
PHOTODROP_S3_REGION=auto
PHOTODROP_S3_ENDPOINT=https://ACCOUNT_ID.r2.cloudflarestorage.com
PHOTODROP_S3_ACCESS_KEY_ID=your-bucket-scoped-access-key
PHOTODROP_S3_SECRET_ACCESS_KEY=your-private-secret
PHOTODROP_S3_PATH_STYLE=false
PHOTODROP_S3_PREFIX=photodrop/
PHOTODROP_S3_PRESIGN_TTL=10m
```

This configuration follows the [Cloudflare Go SDK example](https://developers.cloudflare.com/r2/examples/aws/aws-sdk-go/).
The SDK selects the addressing form; PhotoDrop does not construct R2 URLs or
require Cloudflare Tunnel. **Live R2 interoperability has not been verified in
this development environment because R2 credentials were not available.**

## Browser CORS

Use the exact origin from `PHOTODROP_BASE_URL`. The two signed browser headers
are `Content-Type` and `If-None-Match`. For R2, an example bucket CORS policy is:

```json
[
  {
    "AllowedOrigins": ["https://photos.example.com"],
    "AllowedMethods": ["PUT"],
    "AllowedHeaders": ["Content-Type", "If-None-Match"],
    "ExposeHeaders": ["ETag"],
    "MaxAgeSeconds": 3600
  }
]
```

AWS S3's configuration API wraps this array in `{"CORSRules": [...]}`. For local
development, explicitly use `http://localhost:8080` (or the actual development
port). Do not use a wildcard production origin. PhotoDrop derives its CSP
`connect-src` from the SDK's exact upload origin in addition to `'self'`.

If preparation succeeds but PUT fails, inspect bucket CORS, the origin, HTTPS,
header permissions, and credential expiry. R2 may omit CORS headers on expired
authorization errors, making them opaque to browser JavaScript. PhotoDrop uses
its own completion/refresh APIs for recovery and never depends on parsing a
provider's error body. See [R2 CORS](https://developers.cloudflare.com/r2/buckets/cors/)
and [presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/).

## Direct-upload protocol

All PhotoDrop operations below are anonymous, event/session scoped, same-origin
JSON requests; they do not inherit admin privilege or require an admin CSRF token.
Let `P` be `/api/public/events/{public_id}/upload-sessions/{session_id}/assets`.

| Method and route | Behavior |
| --- | --- |
| `POST /api/public/events/{public_id}/upload-sessions` with `{}` | Creates a session and returns `upload_strategy: "local"` or `"direct"`. |
| `POST P/prepare` | Accepts `filename`, `size`, `content_type`, and a 32-hex `request_id`; persists a pending S3 asset before returning its ID and PUT plan. |
| `POST P/{asset_id}/authorize` with `{}` | Refreshes authorization for the same pending asset/key; requires an open event. |
| `POST P/{asset_id}/complete` with `{}` | Independently verifies the object and returns ready metadata. Repeating completion returns the same asset. |
| `POST P` with a raw image | Existing local-only upload path; rejects image bodies in S3 mode. |

Plans contain `strategy`, `method`, `url`, `headers`, and `expires_at`. The browser
sends the File directly to that URL, with exactly the returned headers, then
calls completion. It displays Preparing, upload progress, Verifying, and success
or a safe error. Three concurrent workers and the 100-file batch limit remain.

Each asset gets a random ID and key `<prefix>events/e<event-id>/assets/<asset-id>`.
The filename cannot influence the key. Preparation's browser request ID makes
repeated preparation of the same immutable metadata reuse its asset, including
after a lost preparation response. It is not content deduplication.

Authorization signs the content type and `If-None-Match: *`. The latter blocks
replaying a still-valid URL over an existing verified object. Ready assets cannot
receive fresh authorization. See [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html)
and [R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/).

## Verification, retries, and event closure

Completion uses authenticated HEAD, requires exact expected size, checks stored
content type where supplied, and GETs `bytes=0-511` with the HEAD ETag in
`If-Match`. Missing/inconsistent range metadata, changed objects, provider
errors, wrong sizes, and disguised non-images cannot become ready. Reads are
bounded even if a provider ignores Range. Gate 3's JPEG/PNG/WebP/GIF/HEIC/HEIF
sniffing determines the final trusted MIME type; full images are never decoded
or downloaded by PhotoDrop. Only the prefix crosses its server.

Wrong-size or invalid-image objects are deleted, but the pending asset and its
immutable expected metadata remain for retry. If verification or its final
database transaction fails transiently, existing bytes remain for retry.
Concurrent finalizers serialize per asset; SQLite write transactions are short
and never span remote I/O. Admin totals show verified `ready` assets, with pending
photo/byte reservations shown separately. Both count toward security quotas.

An ambiguous PUT first triggers completion. If the object is valid, it completes
without another upload. If it is missing/invalid, the client refreshes the same
asset/key and allows at most two PUT attempts per run. A manual retry also
finalizes first. A lost completion response therefore does not duplicate assets.
The attempt lives in the current page session; there is no permanent resumability.

New preparation and refresh require an open event. A previously authorized,
valid pending image may finish after disable/expiration. Deleting events cannot
finalize or issue new authorization. An already-issued URL cannot be revoked.

A presigned PUT does **not** enforce a portable pre-storage body-size limit.
Oversize declarations are rejected before signing; oversized actual objects are
rejected/deleted during completion. An abusive client can still consume storage
before verification. Gate 5 limits grants, preparations, and expected-byte
reservations; it cannot prevent this pre-verification object-store cost.

## Cleanup and provider switching

Startup attempts at most 1,000 stale pending assets, requiring both creation and
latest authorization expiry to be more than an hour old for S3. Fresh/recently
refreshed pending assets and all ready assets are excluded. Cleanup deletes only
recorded, validated keys; missing objects count as clean, provider errors retain
metadata. The same cleanup runs every five minutes, with a five-second cycle
budget; no bucket listing or separate worker service is used.

Event deletion dispatches using each asset's durable backend record. It removes media
before removing the corresponding metadata; partial failures keep the event
closed and retryable, matching Gate 3. Each retired S3 key is first recorded in
`s3_cleanup`, independently of event cascades. Because old URLs or uploads already
in flight can recreate a deleted object, startup also rechecks up to 1,000 retired
keys last checked more than an hour ago. Periodic cleanup repeats this check.
These small records are retained, keys are never reused, and rechecking cannot
delete a newer asset. Retired-key
metadata grows with S3 deletions; this is a deliberate recovery tradeoff.

Switching `local` ↔ S3-A ↔ S3-B affects new assets only. Each asset and retired
key points to an immutable `storage_backends` record. Supply runtime credentials
for every historical backend that still needs operations. Reusing a key with
different addressing metadata fails startup; select a new key instead. No media
is copied. With missing historical credentials, pages/counts/health still work,
but deletion remains incomplete until credentials are restored. See [durable
backend identity and Gate 4 upgrade instructions](storage-backends.md).

Use one PhotoDrop instance per data directory. Keep backups of `/data`, and use
your provider's backup/versioning policy for remote media; a SQLite backup alone
does not back up S3 objects.
