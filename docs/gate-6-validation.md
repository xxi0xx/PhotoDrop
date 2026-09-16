# Gate 6 validation record

Baseline: merged Gate 5 commit `7b3a915536b01a4991a9a00aef5bfd641757497e`.
Branch: `codex/gate-6-export-immich`. Scope is portable export and optional native
Immich integration. No Gate 7 functionality is included.

## Architecture and changes

- `internal/storage/read.go` adds `Reader.Open` streams for local and S3 objects.
  `internal/media/read.go` snapshots ready metadata, pins an event against
  in-process deletion, verifies source size/type, and routes by recorded backend.
  The event/media packages do not import Immich.
- `internal/export` and `cmd/photodrop/export.go` implement numeric event-ID
  selection, new-directory export, deterministic safe/collision filenames,
  streamed byte verification, no-replace atomic publication, and the version-1
  manifest. Export loads only source configuration and is independent of Immich.
- `internal/config/immich.go` and `internal/integrations/immich` implement named
  non-secret targets, the bounded HTTP adapter, album-ID mapping, per-asset
  tracking, SQLite jobs, three concurrent transfers, cancellation, and recovery.
- Migration `008_immich.sql` adds four focused tables: `immich_targets`,
  `immich_event_imports`, `immich_asset_imports`, and `integration_jobs`.
  Migrations 001–007 are unchanged. Fresh/upgrade/restart tests preserve existing
  events, assets, quotas, and prior migration checksums/application times.
- Protected server routes and the event's `Immich.svelte` section provide
  connection tests, status/progress, new sends, failed retries, and cancellation.
  API keys/URLs are not accepted from browser requests or returned in status.
- `scripts/compose-immich-test.yml`, `scripts/immichtest`, and the fixture/smoke
  scripts provide isolated real Immich testing. `.github/workflows/immich.yml`
  adds a dedicated real integration workflow. Production `compose.yml` and
  Docker dependencies remain unchanged; Node/Immich CLI are not in the runtime.

Configuration adds `PHOTODROP_IMMICH_TARGET`, optional
`PHOTODROP_IMMICH_TARGETS`, and each named target's `_URL` and `_API_KEY` variables.
See [operating documentation](export-immich.md) for exact forwarding, permissions,
manifest fields, failure recovery, optional mounts, and limitations.

## Commands and results

From the repository root:

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts web/embed.go
CGO_ENABLED=0 go test -timeout 120s ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop.exe ./cmd/photodrop
docker compose config --quiet
bash scripts/smoke-compose.sh
node scripts/smoke-backends.mjs
docker compose -f scripts/compose-immich-test.yml build
node scripts/smoke-immich.mjs --browser
git diff --check
```

PowerShell used `$env:CGO_ENABLED='0'`; Git Bash smoke used
`$env:MSYS_NO_PATHCONV='1'`. Docker Desktop's CLI directory was added to PATH.
The full Linux race/vet command used an isolated build container:

```sh
docker run --rm -v "$PWD:/src" -w /src \
  -v photodrop-go-cache:/go/pkg/mod \
  -v photodrop-go-build-cache:/root/.cache/go-build \
  golang:1.26 sh -ec 'go test -race -timeout 180s ./... && go vet ./...'
```

The release-one-slot concurrency regression additionally ran ten times under
Linux `-race` with `go test -race -count=10 -run
TestThreeTransfersRunWhileFourthWaits ./internal/integrations/immich`.
Three uploads block on an explicit channel gate; the fourth cannot enter until
one is released. This replaces an earlier timing-dependent overlap assertion.

Frontend: clean install passed, zero Svelte errors/warnings, **29 tests passed**,
and production embedding build passed. CGO-free Go tests/build, vet, formatting,
full Linux race tests, and the repeated concurrency regression passed.
The production Compose smoke passed HTTP/auth/event/local-media behavior,
non-root UID, minimal runtime, stable SQLite restart bytes, and graceful SIGTERM.
The isolated provider regression also passed local → S3-A → S3-B → local,
historical refresh/finalization, mixed deletion, and missing-credential recovery.
GitHub checks are tracked separately in the PR.

## Portable export evidence

The real integration scenario created one event, uploaded locally, switched to
S3-A, uploaded again, switched to S3-B, and uploaded again. The production binary
exported all three through their recorded backend identities. Every exported
SHA-256 matched its original generated PNG; original duplicate filenames remained
in the manifest and all three output names were distinct. The manifest's event,
asset IDs, MIME types, sizes, and version were verified and contained no storage
keys, endpoints, credentials, or presigned URLs.

Unit tests cover ready-only snapshots, excluded pending rows, historical missing
credentials, missing objects, streaming without reading the full body at open,
closing streams, context cancellation, safe errors, unsafe/absolute/UTF-8 names,
case collisions, deterministic reruns, no overwrite, incomplete output cleanup,
no false manifest on failure, and event read/deletion locking. Root confinement
and explicit symlink tests cover source and destination escape attempts.

## Real Immich v3.2.1 evidence

The first test run started a fresh pinned Immich deployment with its required
Postgres/Valkey dependencies and machine learning disabled. The test account's
PhotoDrop API key had exactly `album.create`, `album.read`, `asset.upload`, and
`albumAsset.create`; no all-permissions key was used by the adapter.

1. Connection/auth/permission/version validation passed. One event album was
   created, three mixed-backend photos uploaded and assigned, and all three
   persisted as imported.
2. The album was renamed externally. One later PhotoDrop photo caused exactly
   one additional upload and used the same stored album ID.
3. A forced failure among two new photos left one failed record and one success.
   Failed-only retry issued one upload and recovered it.
4. The proxy hid a response **after real Immich accepted** another photo. Retry
   returned a native duplicate outcome/ID and assigned it without another copy.
5. Twenty new photos were queued. PhotoDrop was killed with SIGKILL after **16
   total assets were accounted for**. Restart resumed the same persisted job
   (job 7 in that fixture) and finished with **27 accounted-for Immich assets**.
6. Invalid credentials, missing credentials, and an offline Immich all returned
   safe operation errors while `/healthz` remained healthy.
7. Deleting the PhotoDrop event removed its local/S3 source media and integration
   state; the Immich album still contained all **27 copies**.

Deterministic tests additionally prove persisted IDs skip retransmission after
album-assignment failures, exact marker reconciliation after uncertain album
creation, transactional exclusive claims, duplicate-job rejection, bounded
concurrency, partial progress, failed/new eligibility, cancellation, and restart.
HTTP tests cover auth failures, malformed/oversized JSON, invalid IDs, truncated
sources, 4xx/5xx, refused redirects, unreachable servers, timeout, and cancellation.

## Browser/admin evidence

Validated in the in-app browser against the real test stack at loopback port
8083 using disposable credentials and generated photos:

- Signed in, opened the event, and observed target `integration-test` without
  credentials. Connection test showed **Connected · Immich 3.2.1**.
- Set the initial album name, started import, observed in-progress controls,
  and saw **1 of 1 accounted for**.
- Uploaded a new PNG through the guest file chooser and upload button. Without
  reloading admin, polling showed **1 imported / 1 not yet imported**. Sending
  it incremented proxy upload count from **33 to 34**, reaching two imported.
- Added two generated photos and forced one upload failure. The UI showed
  **3 imported / 1 failed**. Failed retry reached four imported; proxy counts
  increased by exactly one for that retry (36 to 37).
- Added four more photos and cancelled during controlled transfer delay. The UI
  showed **Import cancelled · 4 of 8 accounted for** and four retryable photos.
- Sent those four again, restarted PhotoDrop during the active job, and reopened
  admin. It rendered the resumed in-progress state and then **Import completed ·
  8 of 8 accounted for**. No warning/error console entries were captured on
  either the admin or guest tab.

An account usage limit initially rejected the browser tool before any UI test;
access resumed and every scenario above was subsequently performed. This is
not an outstanding browser limitation.

## Focused security review

This is a focused implementation/boundary review, not an exhaustive repository
audit. Reviewed production paths, supporting auth/storage semantics, and tests:

| Concern | Control/evidence |
| --- | --- |
| Credentials and authorization leakage | Runtime-only key; explicit DTOs; safe transport/provider errors; bounded JSON; no redirect credential forwarding; database/response assertions |
| SSRF and target confusion | Deployment-only HTTP(S) origin; request JSON rejects URL/key fields; normalized named identity conflicts fail; operations use persisted target ID |
| Export escape/overwrite | New output directory, basename sanitization, `os.Root`, exclusive temporary files, atomic no-replace links, traversal/absolute/symlink/overwrite tests |
| Manifest leakage | Explicit versioned portable fields; no backend metadata or server paths; original user filename remains data |
| Media memory/staging | Owned streaming readers, 32 KiB copy buffers, multipart pipe, no full-file staging/checksum prepass; only bounded control JSON uses ReadAll |
| Jobs and races | SQLite unique active-job index, IMMEDIATE claim transaction, one server owner, three slots, joined shutdown, persisted cancellation/progress, Linux race suite |
| Duplicate assets | Persist ID before assignment; completed rows skipped; uncertain uploads use verified native dedup; one attempt per job |
| Non-ready assets | Snapshot and enqueue SELECTs both filter `status='ready'`; pending exclusion tests |
| Delete propagation | Event pinned during work; cancellation permits deletion; local metadata cascades only; adapter has no remote delete operation; real copies survive source deletion |
| Historical providers | Every read resolves recorded backend ID and verifies owned key/size/type; mixed export hashes and missing-credentials tests |
| Admin-only operations | Existing session, origin and CSRF guards; bounded bodies; anonymous/foreign-origin/unknown URL-field tests |
| Gate 5 security | Production Turnstile hostname/action checks and test-only injected verifier remain unchanged; all earlier regressions remain included |

No unresolved confirmed security finding was identified. Deliberate limitations:
hard-link-capable export filesystem; avoid concurrent CLI/source deletion;
one server per data directory; uncertain album creation may need operator marker
repair; native dedup is not a cross-server exactly-once guarantee; completed
imports are not continuous reconciliation of externally deleted media; only
Immich v3.2.1 was independently tested. No live R2 or production Turnstile claim
is added by Gate 6.

## Final handoff

All local validation listed above passed. Ad hoc browser fixtures, research
downloads, and Gate 6 logs were removed after recording their results here.
The pre-upgrade data backup is retained locally; reusable integration helpers,
fixtures, and the separate Compose project are intentional test infrastructure.

The PR's checks provide the branch/PR CI results for its current commit. Handoff
requires both normal CI and the real Immich workflow to pass and the PR to be
mergeable. Merging and Gate 7 work are not part of Gate 6.
