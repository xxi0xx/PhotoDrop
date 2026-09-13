# Post-Gate-4 storage backend validation

Validated September 12–13, 2026, against merged Gate 4 baseline
`5f196ec7d225666838821d04ebb9fb5a4153d935` on
`codex/durable-storage-backends`. This patch stops before Gate 5.

## Implemented contract

Migration `006_storage_backends.sql` adds immutable, non-secret backend records
and indexed foreign keys on assets and retired S3 cleanup records. The built-in
`local-default` record has ID 1. New assets acquire their permanent backend ID at
creation. Backend keys identify runtime configurations; environment variables
never use generated database IDs. Provider, endpoint, bucket, region, path style,
and prefix cannot be repointed under an existing key. Credential rotation is allowed.

Local-only databases upgrade automatically. Gate 4 S3 fingerprints, including
retired cleanup keys, require exactly one matching original runtime configuration
with an explicit backend key. Missing, wrong, or ambiguous matches fail startup
before cleanup/HTTP serving. Registration and binding roll back together; schema
006 can remain installed so corrected configuration can complete the upgrade.
Migrations 001–005 are unchanged. The old provider/target columns remain only as
compatibility and legacy-binding metadata; backend ID controls operations.

`PHOTODROP_STORAGE_PROVIDER=local` selects the built-in local destination.
S3 mode selects `PHOTODROP_STORAGE_BACKEND_KEY` for new assets. Named credential
sets use `PHOTODROP_S3_BACKENDS` and validated key-specific environment prefixes.
The original single-S3 variables remain supported with an explicit S3 key.
The optional `compose.backends.yml` passes `.env.backends` into the same production
container; local-only deployment still needs no backend configuration file.

Historical authorization refresh, HEAD, ranged GET, deletion, stale-pending
cleanup, and retired-object cleanup resolve the stored backend ID. There is no
fallback to the active bucket. Missing already-bound historical credentials allow
startup, health, and browsing, while affected operations retain retry state.
Event deletion remains closed/incomplete until its required cleanup succeeds.

## Automated validation

Commands executed from the repository root on Windows PowerShell, with Go 1.26,
Node 22+, Docker Desktop, and Git Bash. All completed successfully:

```powershell
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web

$env:CGO_ENABLED = '0'
go test ./...
go vet ./...
go build -trimpath -ldflags='-s -w' -o bin/photodrop.exe ./cmd/photodrop
gofmt -l cmd internal migrations scripts/s3test web/embed.go

docker compose config --quiet
node scripts/smoke-backends.mjs
docker run --rm photodrop:backends-build sh -ec 'apk add --no-cache build-base >/dev/null; go test -race ./...; CGO_ENABLED=0 go test ./...; go vet ./...'

$env:MSYS_NO_PATHCONV = '1'
& 'C:\Program Files\Git\bin\bash.exe' scripts/smoke-compose.sh
docker compose up -d --wait

git diff --check
git diff --name-only -- 'migrations/00[1-5]*'
```

Docker commands require the Docker Desktop CLI on PATH. The isolated smoke builds
both `photodrop:backends-build` and the production runtime image. The ordinary
Compose smoke also builds its image. The Linux command provides the C toolchain
needed for race detection; Windows Go tests and production build used CGO=0.

Results:

- Frontend: dependency installation reported zero vulnerabilities; strict checks
  returned zero errors/warnings; all 8 recovery tests passed; production build
  succeeded with 119 modules. No frontend source or guest protocol changes.
- Go: all packages passed on Windows without CGO and Linux with/without race
  detection. Vet and the CGO-free production build passed.
- Formatting and diff checks produced no offending files/whitespace; the
  migrations 001–005 diff was empty.
- The existing event/upload Compose smoke passed, including persistence, unrelated
  media preservation, SIGTERM shutdown, non-root runtime, and absence of development
  tools from the production image. The normal app was restored healthy on port 8080.

Existing Gate 1–4 tests remain in the suite. Added coverage exercises fresh and
prior-gate database upgrades, legacy local/S3/retired binding, rollback on missing
or ambiguous configuration, migration-history preservation, immutable foreign-key
associations, stable registration, destination mismatch rejection, credential
rotation, key validation/normalization, and secret exclusion from records and logs.
Media tests exercise A preparation followed by B activation, historical refresh
and finalization, stale and retired A cleanup, local/A/B mixed deletion, missing A
credentials and restoration, and unrelated-object preservation. Mutating obsolete
provider/target metadata does not redirect an already-bound asset.

## Compose provider-switch scenarios

`node scripts/smoke-backends.mjs` uses a separate project, named volume, app on
loopback port 8081, and two deterministic S3 test endpoints on loopback ports
18090/18091. Its disposable credentials are test fixtures. It does not modify the
normal app's administrator credentials or data. Production Compose remains one
container; the extra object-server service exists only in the test stack.

Both required variants passed:

1. Local upload L; restart with A; prepare and PUT A; restart with B; refresh and
   finalize A against A; prepare/PUT/finalize B; restart with local active.
2. Verify three ready images and their total bytes. With all historical backends
   configured, delete the event: local file absent, A/B request histories each
   contain HEAD and `Range: bytes=0-511` GET and end with DELETE, event returns 404.
3. Repeat while omitting A from runtime configuration. Delete returns 500,
   `/healthz` stays 200, and A receives no deletion through another destination.
   Restore A and retry: deletion returns 204, local file is absent, both remote
   histories end with DELETE, and the event returns 404.

The Go integration test additionally checks persisted backend associations and
retained metadata directly, plus unrelated objects on both remote endpoints.

## Browser regression

The unchanged guest/admin UI was exercised in the isolated Compose environment:

- Local: PNG (77 bytes) and JPEG (614 bytes) uploaded successfully; progress reached
  100% and admin totals showed two photos / 691 bytes.
- Active A: a 524,902-byte JPEG and a 77-byte PNG succeeded. A 35-byte text fixture
  disguised as JPEG failed image verification safely. Per-file and overall progress
  and verification states appeared; admin totals rose only for the two valid files.
- After switching active configuration to B without reloading the guest page,
  **Retry Failed** retried only the rejected A asset. A recorded two PUTs for that
  same key; successful A images were not retransmitted. The disguised file remained
  rejected. A fresh guest session then uploaded a 614-byte JPEG to B successfully.
- SQLite inspection showed two ready local assets (691 bytes), two ready A assets
  (524,979 bytes), one pending rejected A attempt (zero counted bytes), and one ready
  B asset (614 bytes): five photos / 526,284 bytes in total.
- With local active and A credentials omitted, deleting this mixed event showed
  the existing incomplete-cleanup message and kept the disabled event available
  for retry. After restoring A on September 13, the browser retry completed;
  administration showed **No events yet** and the guest link **Event not found**.
- Final inspection found no asset rows or local uploaded files. Every recorded
  browser object key on A/B ended with DELETE, including the rejected A attempt.

## Review, CI, and limits

Review of changed routing, persistence, configuration, and logging paths found no
active-provider fallback or runtime credential persistence. Endpoint validation
rejects embedded credentials/query strings; key validation prevents arbitrary
environment/path fragments. Deletion checks the recorded backend's generated
event/asset key and prefix. Tests verify safe config formatting and backend logs.

GitHub Actions runs frontend checks/tests/build, race and CGO-free Go tests, vet,
production build, the existing Compose smoke, and the new two-backend Compose
smoke on `ubuntu-latest`. Refer to the pull request's checks for its immutable
commit-specific CI result; local results alone do not establish a clean runner pass.

Live R2/AWS/MinIO interoperability was not newly verified with real provider
credentials. Deterministic S3 tests establish routing and lifecycle behavior, not
live-provider CORS or account policy. Gate 4 fingerprints did not encode path style;
the first upgrade must supply the original setting. Keys cannot be renamed/reused,
including registered-but-unused keys. Cleanup retains Gate 4's bounded startup-only
policy and retired-key retention; unavailable backends or late PUTs can require a
later restart/retry. One app instance per data directory remains required.

Changing the active backend does not copy media. No multiple active destinations,
backend-management UI, credential storage, or Gate 5 functionality was implemented.
