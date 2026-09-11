# Gate 3 validation

Validated on 2026-09-11 on Windows and in Linux Docker containers.

## Automated checks

All commands below passed. Frontend checking reported zero errors and warnings.
Run from the repository root; PowerShell uses `$env:CGO_ENABLED='0'` for the
CGO-free commands and names the Windows output `bin/photodrop.exe`.

```sh
npm ci --prefix web
npm run check --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations web/embed.go
CGO_ENABLED=0 go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop
docker compose config --quiet
docker build --target backend -t photodrop:gate3-build .
docker run --rm photodrop:gate3-build sh -ec 'apk add --no-cache build-base >/dev/null; go test -race ./...; CGO_ENABLED=0 go test ./...; go vet ./...'
bash scripts/smoke-compose.sh
docker compose up --wait --wait-timeout 120 -d
git diff --check
```

`smoke-compose.sh` runs `docker compose up --build --wait --wait-timeout 120 -d`
and both `smoke-events.mjs` and `smoke-uploads.mjs`. On Windows, Git Bash was
used with the Docker CLI on PATH and `MSYS_NO_PATHCONV=1`.

The automated coverage includes all existing Gate 1/2 tests, fresh and upgraded
database migrations with checksum preservation, bounded incremental writes,
unknown-length oversize bodies, image sniffing, unsafe filename metadata,
session/event isolation, concurrent duplicate names, interrupted writes,
completion database errors, pending cleanup, and partial deletion retries.

Compose checks verified uploaded file hashes under `/data/uploads`, ready-only
counts and byte totals, persistence across restart, removal of only the deleted
event's files, UID 10001, absence of Node/npm/Go in the runtime, and graceful
SIGTERM with exit status zero. The stopped database hash survived restart.

## Browser checks

Used an isolated instance of the production Go binary with an empty test data
directory, a throwaway password, and a 1 MiB file limit. A localhost test proxy
delayed upload bodies to make real XHR progress visible with small fixtures.
These helpers and data are ignored workspace artifacts, not production code.

1. Signed in and created an enabled event through the administrator UI.
2. Opened its public page and selected a 77-byte PNG and 614-byte JPEG through
   the browser's multi-file chooser. Observed per-file and overall progress,
   finishing states, and the thank-you completion state.
3. Recovered a proxy-induced failed request using **Retry Failed**. The completed
   JPEG stayed complete; admin totals became exactly 2 photos / 691 bytes.
4. Selected a valid PNG, text disguised as `disguised.jpg`, and an oversized
   PNG. The valid image succeeded; the others displayed type/size errors.
   Retrying failures did not duplicate the valid image: 3 photos / 768 bytes.
5. Checked the guest form, errors, progress and retry control at a 390-pixel
   viewport width, with no horizontal overflow; restored the default viewport.
6. Disabled the event while its guest page remained loaded, then retried an
   asset request with its existing upload session. It displayed “This event is
   no longer accepting uploads.”
7. Re-enabled the event and uploaded two files both named `photo.png`; both
   completed separately, bringing admin totals to 5 photos / 922 bytes.
8. Deleted the disposable event through its confirmation UI. The admin list
   became empty, the guest page showed “Event not found,” and the test upload
   directory contained no files.

## Limits of these checks

Image validation is header sniffing, not full decoding or malware scanning.
HEIC/HEIF checks use small signature fixtures, not a broad camera corpus.
Browser testing used the desktop in-app browser and a phone-width viewport,
not physical mobile devices or an external HTTPS reverse proxy. Restart and
injected I/O/database failures were tested; abrupt host power loss was not.
Use one application instance per data directory. No Gate 4+ features were added.
