# Release policy

The intended first stable release is **v1.0.0**. Git SemVer tags are authoritative;
the private frontend package has no competing application version. Untagged
builds default to `dev`. `photodrop version` / `--version` works without credentials
or opening the database. Release builds inject the version without `v` and commit:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags="-s -w -X photodrop/internal/buildinfo.Version=1.0.0 -X photodrop/internal/buildinfo.Commit=$(git rev-parse HEAD)" \
  -o bin/photodrop ./cmd/photodrop
```

No build machine paths, usernames, environment or credentials are included in this
metadata. Version is CLI-local, not a new unauthenticated details endpoint.

## Post-merge owner procedure

Gate 8 PR #9 is merged. The owner selected Apache-2.0, enabled private vulnerability
reporting, and completed live R2 and production Turnstile validation on 2026-09-24
(America/Chicago). See the [post-merge readiness record](gate-8-validation.md).
Readiness PR #16 and release-notes PR #17 are merged. The owner pushed `v1.0.0`
at `4dabc844b5f7e3c223ea84ee80d2e7ccb84b6d01`. Its immutable image exists;
publication stopped during runtime verification as recorded below. Recover that
artifact using the manual workflow after the fix is merged and main CI is green.
Do not create another tag or repeat the immutable-image build to repair this incident.

Accept tags `vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH-PRERELEASE`. Numeric fields
cannot have leading zeros. Build metadata (`+...`) is deliberately excluded because
Docker tags do not support it. Never reuse a version. Publish stable versions in
ascending order so moving aliases do not downgrade users. Major versions signal
breaking changes, minors compatible additions, patches compatible fixes. Prereleases
are explicitly unsupported for production. Subsequent work uses versioned issues
and milestones, not a Gate 9.

## Workflow and artifacts

`release.yml` runs only on `v*` tag pushes. It validates SemVer, verifies the tagged
commit is an ancestor of current main with full history, and refuses to proceed
without LICENSE. It reuses the existing CI and real Immich workflows; they retain
contents-read access. The publish job alone has contents-write/packages-write.
There is no pull_request_target, secret forwarding to PRs, or custom signing scheme.
Release-sensitive actions are pinned to resolved immutable upstream commits.

Buildx cross-compiles CGO-free binaries for linux/amd64 and linux/arm64, builds the
minimal runtime, and publishes `ghcr.io/xxi0xx/photodrop:1.0.0` first. OCI source,
revision, version, title and description labels identify the artifact. The license
label is `Apache-2.0`; the canonical LICENSE is included in the runtime image.
Runtime and OCI checks require the expected license label; runtime checks also
verify the packaged license text. Publication still requires a root LICENSE.
BuildKit publishes SBOM and maximum-mode provenance attestations with the image.
See [Docker's attestation guidance](https://docs.docker.com/build/ci/github-actions/attestations/).
Build arguments contain public version metadata only. This is not a claim of
bit-for-bit reproducibility or a separately signed GitHub attestation.

The workflow inspects the published digest for both platforms and associated
attestation manifests, selects exactly one non-attestation runtime child manifest
per required platform, and pulls/runs each by its distinct child digest. This avoids
Docker reusing one local index-digest reference for two architectures. Checks cover
the actual platform, CLI version, labels,
health, UID 10001, data permissions, minimal runtime, and SIGTERM. Only then are
stable aliases `1.0`, `1`, `latest` promoted and a GitHub Release created. A prerelease
such as `1.1.0-rc.1` gets no moving aliases and a prerelease GitHub Release.

If publication fails after the immutable image was pushed, no successful GitHub
Release is announced. Diagnose the artifact by digest. A rerun refuses to overwrite
the existing version. Use the explicit recovery workflow below for a known-good
immutable image whose verification/publication was interrupted. Never delete/reuse
the Git tag or rebuild/overwrite the immutable image to get past that guard.
Registry operations are not atomic across aliases; inspect every alias after failure.
Confirm GHCR package visibility is public before announcing anonymous installation.

PR/branch CI loads amd64/arm64 release-style builds locally and runs the same runtime
inspection, with no registry login/write credential or GitHub Release creation.
Loaded Docker images do not retain BuildKit attestations. CI therefore separately
exports OCI archives with SBOM/provenance and verifies their blob digests, metadata,
attestation subjects and predicates without publishing. Release verification checks
the actual registry artifact as well.

## Recover an already-published immutable image

`release-recover.yml` is an explicit `workflow_dispatch`, accepted only from its
trusted main definition. Inputs are the existing SemVer `tag` and an independently
confirmed `expected_digest`. It shares the normal release plan and concurrency
group; normal publication and recovery cannot overlap in this repository. Its
contents/packages write permissions serve alias promotion and Release creation
only. It contains no application build, immutable-tag push, image deletion or Git
tag mutation. The normal immutable-version guard remains unchanged.

Recovery fetches full history/tags, requires the remote tag, resolves annotated or
lightweight tags to a commit, checks current main ancestry and both root/tagged
LICENSE presence, and resolves the existing registry version tag. The registry
digest must equal the operator-confirmed input. It verifies the index, associated
attestations, both SPDX SBOMs and SLSA provenance, then runs both exact child digests
through the complete runtime checks. Revision must match the **resolved tag commit**,
not the workflow's current-main SHA. Application contents are never substituted.

Only after all artifact checks pass, recovery checks every alias and any existing
GitHub Release before writing anything. Missing aliases are created from the verified
index; matching digests are accepted; conflicting digests fail. An existing Release
must match tag, title, prerelease/draft state and tagged-source notes, or recovery fails
without editing it. A newer published stable SemVer Release blocks older stable
recovery even if aliases are missing. Prereleases have no moving aliases and are
never marked latest. Notes come from `CHANGELOG.md` at the tagged commit.

Identity and conflicts are rechecked near writes. Reruns accept matching partial
completion. Registry tags have no compare-and-swap across all aliases: exclude
external/manual publishers during recovery. A failure between writes may leave
some matching aliases; inspect, resolve any conflicting state deliberately, then
rerun. Auth/network/rate-limit errors fail closed, rather than being treated as
missing artifacts. Nothing overwrites an unexpected alias or Release automatically.

### v1.0.0 incident and owner procedure

[Release run 36129419967](https://github.com/xxi0xx/PhotoDrop/actions/runs/36129419967)
passed application, migration, Compose, recovery, Immich and both architecture build
checks. It published `ghcr.io/xxi0xx/photodrop:1.0.0` with index digest:

```text
sha256:526f2e14d60e2ce140acc746bdc420f6730e30442d28bbc685a1dba6054ffd72
```

Published manifest/attestation, SBOM and provenance checks passed, as did the full
amd64 runtime check. The arm64 pull failed with `cannot overwrite digest` because
Docker was asked to replace the platform selection behind the same local index
reference. Application validation did not fail. Moving aliases and the GitHub
Release were correctly withheld; the immutable artifact is evidence to preserve.

1. Review and merge the verification/recovery fix PR; wait for main CI to pass.
2. Confirm remote `v1.0.0` still resolves to the commit above and inspect the version
   tag read-only with `docker buildx imagetools inspect ghcr.io/xxi0xx/photodrop:1.0.0`.
   Confirm its index digest exactly matches the recorded value; do not rebuild it.
3. Ensure no external publisher is changing aliases, then deliberately dispatch:

   ```sh
   gh workflow run release-recover.yml --repo xxi0xx/PhotoDrop --ref main \
     -f tag=v1.0.0 \
     -f expected_digest=sha256:526f2e14d60e2ce140acc746bdc420f6730e30442d28bbc685a1dba6054ffd72
   ```

4. Watch the recovery run. It must verify both child runtimes before creating any
   missing `1.0`, `1`, `latest` aliases or the Release. Inspect all three aliases;
   each must resolve to the exact recorded index. Confirm the Release references
   the existing tag and digest and GHCR permits intended public pulls.

This fix PR does not dispatch recovery, promote aliases or create a Release.

Fix validation (2026-09-25): the shared verifier passed read-only against this
existing index, including both child-digest runtime checks. No registry writes
were made. All 34 frontend tests and check/build, CGO-free Go tests, Linux race/vet,
18 release/recovery tests, documentation/configuration checks and Actionlint passed.
A local amd64/arm64 OCI build retained valid SBOM/provenance. Tag-resolution tests
cover real annotated/lightweight Git tags; local Windows validation used Git for
Windows because the unrelated MSYS Git on PATH rejected revision-peeling syntax.
