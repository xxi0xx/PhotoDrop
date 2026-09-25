# Contributing

Use Go 1.26, Node 22.12+ (CI uses Node 22), npm, Git, and Docker with Compose v2.
Linux is the CI/race reference platform; race testing requires a C toolchain.
Build the embedded frontend before Go tests. Windows can run CGO-free checks and
use Linux/Docker for race/integration checks.

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts web/embed.go
CGO_ENABLED=0 go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop ./cmd/photodrop
node --test scripts/release.test.mjs
node scripts/check-docs.mjs
git diff --check
```

Formatting must print nothing; Svelte check fails on warnings. In PowerShell set
`$env:CGO_ENABLED='0'` before the corresponding command, then remove it for race.
For local development supply a private PHOTODROP_ADMIN_PASSWORD and
PHOTODROP_DATA_DIR=./data; Go does not load `.env`. `npm run dev --prefix web`
starts Vite with backend proxying; the compiled build remains the production UI.

Run integration tests on disposable state, never the real deployment:

```sh
docker compose config --quiet
sh scripts/smoke-compose.sh
node scripts/smoke-backends.mjs
node scripts/smoke-fresh.mjs
node scripts/smoke-release.mjs
docker compose -f scripts/compose-immich-test.yml build
node scripts/smoke-immich.mjs
```

The first smoke uses the current Compose directory/data/8080 and leaves the app
stopped: run it in a fresh disposable checkout with an ephemeral password. Provider
and Immich suites use separate loopback projects. `--browser` on the Immich smoke
retains browser fixtures; remove only that test project's data afterward.
The fresh-install smoke copies only public Git-visible files into a temporary
directory, uses empty local/S3 state and an ephemeral password, and cleans its own
project. Its test-only overlay needs Compose 2.24.4+ for reset/override tags.
The release smoke builds exact merged Gate 7 source and the candidate, seeds
local/S3 and completed Immich metadata, then tests a stopped backup, upgrade and
destructive restore using uniquely named disposable volumes. Neither reads the
developer `.env` or mounts production data.
Run the real Immich suite for integration, storage/read/export, migration, worker,
or container changes. CI runs it on relevant paths. Do not replace it with mocks.

Make focused branches/PRs with concrete behavior and validation evidence. Add tests
for changed contracts and failure paths; document operational changes. **Never edit
an already-released migration; add a new numbered migration.** Keep secrets,
generated binaries, screenshots, logs, signed URLs and personal photos out of Git.
Test-only fault injection belongs in test executables/fixtures, never production
switches. No automatic dependency merging is enabled.

Report vulnerabilities through [SECURITY.md](SECURITY.md), not public bug reports.
The project uses [Apache-2.0](LICENSE). Release work follows [the policy](docs/releases.md).
