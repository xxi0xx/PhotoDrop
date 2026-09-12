# Gate 4 validation

Validated on Windows with Go 1.26, Node 22+, Docker Desktop Linux containers,
and the Codex in-app Chromium browser on September 11–12, 2026. The baseline is
Gate 3 merged on `main` (`fa256d6aa4a8d6c1b1715f24003f781a18733d9b`).

**Implementation and local validation pass. Live Cloudflare R2 interoperability
and R2 browser CORS remain unverified (acceptance criteria 62–63): no live R2
bucket, endpoint, or credentials were available.** The deterministic S3 endpoint
below is a test double, not R2 or MinIO. GitHub's associated PR checks provide
the independent clean-runner CI result (criterion 78).

## Commands and results

All commands below completed successfully. The shell equivalents on Windows
used `$env:CGO_ENABLED='0'` where shown, and `bin/photodrop.exe` for the native
binary. `gofmt` and `git diff --check` emitted no findings.

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts/s3test web/embed.go
CGO_ENABLED=0 go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop
git diff --check
git diff --name-only -- 'migrations/00[1-4]*'
docker compose config --quiet
docker compose stop
bash scripts/smoke-compose.sh
docker compose up -d --wait
docker build --target backend -t photodrop:gate4-build .
docker run --rm photodrop:gate4-build sh -ec 'apk add --no-cache build-base >/dev/null; go test -race ./...; CGO_ENABLED=0 go test ./...; go vet ./...'
```

The native Windows invocation of the Compose smoke script used Git Bash and
`MSYS_NO_PATHCONV=1`. The script itself performs the production Compose build,
startup, health, and restart checks. A stopped pre-upgrade database backup was
kept outside version control. The original local Compose environment was
preserved and the normal container was restored healthy afterwards.

- Svelte: zero errors/warnings; frontend production build passed.
- Frontend recovery tests: 8/8 passed, using Node's built-in test runner.
- All Go packages passed both CGO-free tests and Linux race detection; vet passed.
- Production binary and Docker image built successfully. No Go/Node compiler,
  object-store sidecar, or added production container is required.
- Existing Gate 2 and Gate 3 Compose smoke tests passed, including event CRUD,
  session isolation, streaming uploads, counts, persistence, scoped deletion,
  CSRF/origin checks, non-root execution, and graceful SIGTERM.
- Migrations 001–004 have no diff. Fresh and Gate 1/2/3 upgrade tests pass;
  the Gate 3 fixture retains original history/checksums, local metadata and bytes.

## Deterministic S3 coverage

The AWS SDK for Go v2 talks to `internal/testutil/s3test`, a private in-memory
HTTP endpoint that verifies signatures with the SDK signer. Tests do not require
cloud credentials. The production binary does not import this package.

| Area | Verified behavior |
| --- | --- |
| Configuration | Local defaults; required S3 values; endpoint, bucket, region, prefix, path-style and TTL validation; credential redaction; retained backend in local mode. |
| Authorization | Pending row precedes signing; server-generated event/asset key; signed content type and `If-None-Match: *`; refresh keeps identity/key; ready assets cannot refresh. |
| Isolation | Wrong event/session/internal event ID denied; guests cannot supply arbitrary buckets/endpoints/keys; changed storage target fails closed. |
| Verification | HEAD exact size, optional declared MIME metadata consistency, GET `bytes=0-511` with `If-Match`, then Gate 3 signature sniffing for JPEG/PNG/WebP/GIF/HEIC/HEIF. |
| Failure paths | Missing/empty/wrong-size/fake objects, HEAD/GET outages, ignored ranges, changed ETags, canceled operations and failed final database commit cannot become ready. |
| Recovery | Lost PUT/complete response, expired authorization, repeated/concurrent completion, immutable prepare request identity and at most two application PUT attempts per run. |
| Event policy | New/refresh authorization requires an open event; valid preauthorized objects can complete after disable/expiry; deleting events cannot complete. |
| Deletion/cleanup | Mixed providers, partial remote failure/retry, missing objects, unrelated objects preserved, stale pending cleanup, fresh/refreshed/ready exclusions and late PUT reconciliation. |

`TestDirectUploadHTTPDataPathAndIsolation` additionally uses a JPEG body with
64 KiB of padding. It counts request-body reads at PhotoDrop: control requests
are bounded to 4,097 bytes, and the local raw-upload endpoint consumes zero
media bytes in S3 mode. The full body goes to the configured object endpoint;
verification reads only the prefix. Normal logs are checked for credential,
signed-query, and provider endpoint leakage.

## Real browser validation against the local test endpoint

The browser app used `http://localhost:8081`, an isolated temporary database,
a 1 MiB per-file limit, and a throwaway test administrator credential. Normal
Compose on port 8080 retained its own environment/data. The test endpoint was
launched with:

```sh
go build -o bin/s3test ./scripts/s3test
./bin/s3test -listen 127.0.0.1:18090 -origin http://localhost:8081 -delay 10s -drop-put-responses 1
```

For this test only, S3 configuration was bucket `photos`, region `auto`, endpoint
`http://127.0.0.1:18090`, path-style `true`, prefix `test/`, TTL `10m`, and the
obviously fake credentials defined in `internal/testutil/s3test/server.go`.
An initial attempt on port 9090 could not bind because Windows reserved that
port; the UI safely reported storage unavailable. Validation was restarted
with a fresh temporary database and the reachable endpoint above.

1. Created an enabled event in the admin UI. Selected a 77-byte PNG and a
   614-byte JPEG in the guest UI. Both local uploads completed, progress reached
   100%, and admin totals became **2 photos / 691 bytes**.
2. Restarted the app with the same database and `storage_provider=s3`. The
   existing local assets, totals, and admin session persisted.
3. Selected three direct images: two separate `photo.png` files (77 bytes each)
   and `photo.jpg` (614 bytes). Observed per-file progress, overall progress,
   and zero newly completed photos while PUTs were still pending.
4. Disabled the event while authorized PUTs were in flight. All three still
   finalized successfully; totals became **5 photos / 1,459 bytes**. The test
   endpoint dropped one successful PUT response after storing the object.
   Chromium retried that HTTP PUT once, which the conditional write rejected;
   the application recovered by completion. Only three assets became ready.
5. Reenabled the event. Selected a **524,902-byte padded JPEG**, a 35-byte text
   file named `disguised.jpg`, and an oversized PNG. Observed the large file at
   **37%** with **196,608 bytes** sent. The large image succeeded independently;
   the fake image failed signature verification and was deleted; the oversized
   file failed the client limit without uploading.
6. Clicked **Retry Failed (2)**. The fake image reused its existing asset/key,
   failed again and was deleted again. The large image stayed complete and its
   object endpoint recorded exactly **one** full-body PUT. Totals remained
   **6 photos / 526,361 bytes**.
7. Restarted with `storage_provider=local`, retaining the same S3 configuration.
   All six ready assets and totals persisted. Another 77-byte local PNG upload
   succeeded, resulting in **7 photos / 526,438 bytes**.
8. Deleted the disposable mixed-provider event through the admin UI. The list
   showed no events, the guest URL showed Event not found, all local media files
   were gone, and every one of the five S3 keys that received PUTs ended with a
   DELETE request (including the rejected pending asset). No test helpers or
   test storage were added to normal Compose.

The endpoint's `/__test/requests` observations record only method, key path,
body length, and Range—not presigned queries. They prove that the browser sent
the 524,902-byte body to **127.0.0.1:18090**, independently of PhotoDrop on 8081.
All verification GETs used `bytes=0-511`. Combined with the instrumented HTTP
test, this validates the required separate media/control paths and bounded
verification download. This is actual browser cross-origin behavior against
the test endpoint, not a claim of R2 browser CORS validation.

## Explicit implementation review

Reviewed URL/credential logging, direct body routing, key/prefix injection,
event/session association, ready-object overwrite/replay, HEAD-only MIME trust,
whole-object reads, size mismatch, retries, provider switches, abandoned objects,
CORS headers and deletion scope. No unresolved locally verifiable failure was
found. Signed PUTs require conditional writes; supported providers must honor
that condition. SDK errors are mapped to safe errors instead of being logged or
returned raw. Only checked, persisted keys can be deleted; bucket listing is not
used. SQLite transactions never span remote verification I/O.

## Limits and live R2 follow-up

- **Criteria 62–63 are unverified.** With private bucket-scoped R2 credentials,
  follow [the storage setup](storage.md#cloudflare-r2-setup), configure the exact
  browser origin/CORS headers, and repeat prepare → browser PUT → HEAD/range
  verification → totals → deletion. Also verify conditional replay rejection.
  CLI uploads alone cannot validate browser CORS. Do not put these secrets in
  public/fork CI.
- Direct PUT cannot portably enforce the size ceiling before bytes reach the
  object store. Completion rejects/deletes mismatches. This is documented.
- Cleanup runs at startup with a time/batch budget; late objects can remain until
  a subsequent restart. Retired-key metadata is retained and grows with deletions.
- Retain S3 configuration when switching back to local. One historical S3 target
  is supported; changed endpoint/bucket/region/prefix requires restoring the
  original target to manage its assets. No storage migration is performed.
- Retry identity lasts for the current page session. This is single-PUT upload,
  without durable resumability, full-file decoding, or antivirus scanning.
- No Gate 5+ features were added: no videos, multipart/resumable protocol, QR,
  Immich, Turnstile, rate limiting, gallery, public downloads, or export.
