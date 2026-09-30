# Release policy

PhotoDrop **1.1.0** is the current documented version. Git SemVer tags are authoritative;
the private frontend package has no competing application version. Untagged
builds default to `dev`. `photodrop version` / `--version` works without credentials
or opening the database. Release builds inject the version without `v` and commit:

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags="-s -w -X photodrop/internal/buildinfo.Version=1.1.0 -X photodrop/internal/buildinfo.Commit=$(git rev-parse HEAD)" \
  -o bin/photodrop ./cmd/photodrop
```

No build machine paths, usernames, environment or credentials are included in this
metadata. Version is CLI-local, not a new unauthenticated details endpoint.

## Post-merge owner procedure

1. Merge the 1.1.0 readiness PR and wait for main CI and the Immich workflow.
2. Review the [1.1.0 validation record](v1.1.0-validation.md), changelog, backup
   requirements and known limitations. Confirm the intended merged commit.
3. Deliberately create `v1.1.0` on that main commit and push that tag. The tag is
   the owner's publication action; a source checkout or readiness PR does not
   imply that a matching registry image exists.
4. Wait for artifact verification, then confirm the immutable `1.1.0` image,
   stable `1.1`, `1`, `latest` aliases and GitHub Release. Verify public pull access.

The first stable version, v1.0.0, was published on 2026-09-25. Its earlier
[publication incident](v1.0.0-release-history.md) is historical evidence, not a
pending recovery step for this release.

Accept tags `vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH-PRERELEASE`. Numeric fields
cannot have leading zeros. Build metadata (`+...`) is deliberately excluded because
Docker tags do not support it. Never reuse a version. Publish stable versions in
ascending order so moving aliases do not downgrade users. Major versions signal
breaking changes, minors compatible additions, patches compatible fixes. Prereleases
are explicitly unsupported for production. Subsequent work uses versioned issues and milestones.

## Workflow and artifacts

`release.yml` runs only on `v*` tag pushes. It validates SemVer, verifies the tagged
commit is an ancestor of current main with full history, and refuses to proceed
without LICENSE. It reuses the existing CI and real Immich workflows; they retain
contents-read access. The publish job alone has contents-write/packages-write.
There is no pull_request_target, secret forwarding to PRs, or custom signing scheme.
Release-sensitive actions are pinned to resolved immutable upstream commits.

Buildx cross-compiles CGO-free binaries for linux/amd64 and linux/arm64, builds the
minimal runtime, and publishes `ghcr.io/xxi0xx/photodrop:1.1.0` first. OCI source,
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
stable aliases `1.1`, `1`, `latest` promoted and a GitHub Release created. A prerelease
such as `1.2.0-rc.1` gets no moving aliases and a prerelease GitHub Release.

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
never marked latest. Notes come from `CHANGELOG.md` at the tagged commit. The marked changelog format
selects the exact version section, excluding future work and older entries. Legacy
untagged-format changelogs retain their original whole-file notes for recovery
compatibility; existing Releases are still never overwritten.

Identity and conflicts are rechecked near writes. Reruns accept matching partial
completion. Registry tags have no compare-and-swap across all aliases: exclude
external/manual publishers during recovery. A failure between writes may leave
some matching aliases; inspect, resolve any conflicting state deliberately, then
rerun. Auth/network/rate-limit errors fail closed, rather than being treated as
missing artifacts. Nothing overwrites an unexpected alias or Release automatically.
