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
Merge the focused readiness finalization PR after checks pass.
Review/finalize the Unreleased changelog into a dated 1.0.0 entry in a normal PR.
Only then deliberately create/push `v1.0.0` on the chosen merged main commit.
This preparation task does not create that tag or publish a stable release.

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
attestation manifests, pulls/runs both platforms, and checks CLI version, labels,
health, UID 10001, data permissions, minimal runtime, and SIGTERM. Only then are
stable aliases `1.0`, `1`, `latest` promoted and a GitHub Release created. A prerelease
such as `1.1.0-rc.1` gets no moving aliases and a prerelease GitHub Release.

If publication fails after the immutable image was pushed, no successful GitHub
Release is announced. Diagnose the artifact by digest. A rerun refuses to overwrite
the existing version. The owner must inspect/quarantine a failed artifact and choose
a new version or explicitly recover it; do not blindly delete/reuse a stable version.
Registry operations are not atomic across aliases; inspect every alias after failure.
Confirm GHCR package visibility is public before announcing anonymous installation.

PR/branch CI loads amd64/arm64 release-style builds locally and runs the same runtime
inspection, with no registry login/write credential or GitHub Release creation.
Loaded Docker images do not retain BuildKit attestations. CI therefore separately
exports OCI archives with SBOM/provenance and verifies their blob digests, metadata,
attestation subjects and predicates without publishing. Release verification checks
the actual registry artifact as well.
