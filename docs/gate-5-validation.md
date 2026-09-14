# Gate 5 validation record

Scope: Gate 5 security/abuse controls on top of merged backend-identity baseline
`f2b2ffaba8840f1a26f8a8a36a684f3172b3dd26`. Branch: `codex/gate-5-security-abuse`.
No Gate 6 export, Immich, QR, contributor identity, video, multipart upload,
gallery, or download functionality was added.

## Implementation and threat model

The control boundary is an anonymous, temporary upload grant. Optional Go-side
Turnstile verification precedes its creation; origin/rate checks precede expensive
verification and password hashing. Persisted grant bounds and atomic pending-row
reservations constrain new local/S3 writes. Cleanup resolves immutable historical
backends. Guests, filenames, MIME declarations, headers, and forwarded links are
untrusted; trusted host/admin credentials, one process per data directory, and
provider security remain deployment assumptions. Full DDoS/distributed controls
and compromised host/admin/storage credentials are outside this gate.

Migration `007_security_abuse.sql` adds event quotas and grant expiry/bounds/
verification metadata. Prior migration checksums and ready media are preserved;
legacy grants expire. All configuration, defaults, rates, proxy rules, headers,
quota units, cleanup cadence, and remaining storage/privacy limits are documented
in [security.md](security.md). In particular, default grants last two hours and
allow 100 assets/5 GiB; event quotas are optional and count all ready and pending
assets across historical providers. Expiry blocks preparation/refresh, while an
already-authorized valid pending object may finalize. Periodic cleanup runs every
five minutes within five seconds, capped at 1,000 pending and 1,000 retired keys,
plus 5,000 empty expired grants per pass. Production remains one container.

Material changes: `internal/abuse`, security configuration, event quota persistence,
`internal/media` grant/reservation/cleanup paths, `internal/server` guards/headers/
verification/test fixture, migration 007, guest upload/Turnstile/recovery components,
admin quota controls, Compose environment forwarding, and security/storage docs.

## Automated validation

The following commands were run from the repository root, with frontend assets
built before Go embedding. Native Windows and isolated Linux/Docker checks cover
the same source. The browser fixture is skipped in ordinary test/CI runs.

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts web/embed.go
CGO_ENABLED=0 go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop.exe ./cmd/photodrop
docker compose config --quiet
bash scripts/smoke-compose.sh
node scripts/smoke-backends.mjs
git diff --check
```

PowerShell used `$env:CGO_ENABLED='0'`; Git Bash smoke used
`$env:MSYS_NO_PATHCONV='1'`. Docker Desktop's installed CLI directory was added to
PATH. The exact Linux suite used the existing backend build image and the final
source mounted read-only, keeping toolchains outside the runtime image:

```powershell
docker run --rm --mount "type=bind,source=$PWD,target=/src,readonly" photodrop:backends-build sh -ec 'apk add --no-cache gcc musl-dev >/dev/null; go test -race ./...; CGO_ENABLED=0 go test ./...; go vet ./...; CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/photodrop ./cmd/photodrop'
```

Frontend: 26 tests pass, zero Svelte errors/warnings, production build succeeds.
Coverage includes the eight prior direct-upload recovery cases, expired-grant
finalization, security failures without retransmission, cleaned empty grants,
cleaned pending attempts, fresh authorization after expiry, and preservation of
both IDs across uncertain failures. A completed sibling is unchanged by recovery.

Backend coverage includes configuration pairing/defaults/redaction; defensive
Siteverify transport; wrong/missing hostname/action; replay; provider timeout and
failure; no token persistence/logging; all rate scopes/refill/capacity/concurrency;
NAT session independence; untrusted/trusted/multihop/IPv6 XFF; bounded bodies;
origin/CSRF regression; CSP/HSTS/privacy headers; periodic shutdown/reclamation;
and legacy migration without ready-media/history damage.

Quota races use **20 concurrent preparations across two database connections and
two service instances**, after nine existing assets with only one slot left. Exactly
one succeeds for event count, event bytes, session count, and session bytes. Other
cases cover exact/over-limit values, unknown/lying local lengths, quota edits,
mixed historical stores, cleanup release, missing-credential fairness, expiry,
and finalization after expiry. Cleanup lifecycle tests retain expired grants with
ready assets for lost-response recovery while reclaiming empty/abandoned grants.

Final checks after the recovery fix:

| Check | Result |
| --- | --- |
| Frontend install/check/test/build | Pass; 26 tests, zero diagnostics |
| Go formatting and diff whitespace | Pass |
| Windows CGO-free full Go suite/vet/production build | Pass |
| Linux full race suite | Pass |
| Linux CGO-free full suite/vet/production build | Pass |
| Final normal Compose smoke | Pass; event/local uploads, persistence, non-root runtime, restart, clean SIGTERM |
| Final isolated provider-switch Compose smoke | Pass; historical routing, mixed deletion, credential failure/retry |
| Browser fixture shutdown | Pass; fixture test exits successfully |
| Production test-helper isolation | `/__test/state` returns 404; production dependency graph excludes `internal/testutil` |

The normal service was restored healthy on port 8080 after smoke shutdown checks.
The isolated provider-test stack was stopped without deleting its test volume.

## Browser and integration evidence

Resumed the existing finite-quota event in the isolated browser fixture instead
of recreating it. Its initial ceiling was four photos and 1,073,742 bytes.

1. **Turnstile disabled/local:** uploaded PNG (77 B) and JPEG (614 B), observed
   per-file completion and 100% overall progress. No challenge script or verifier
   call occurred. No console errors/CSP violations.
2. **Same event/S3-A:** 524,902 B JPEG and 77 B PNG completed directly; the third
   file was quota-rejected. Admin showed four ready photos/525,670 B. Completed
   items remained successful and the guest saw the organizer/quota message.
3. **Quota edits/reservations:** lowering count below four showed over-quota without
   deletion. Raising it allowed one pending 77 B reservation. Cleanup removed it,
   usage returned to four ready/zero reserved, and the failed JPEG could retry.
4. **Verified multi-file flow:** the test-only widget/verifier authorized one grant
   for two S3 photos; aggregate verifier calls increased by exactly one. No
   challenge per file. Forced verifier rejection created no asset, displayed a
   safe verification error, and a fresh verification recovered the same selection.
5. **Expiry:** after one file succeeded and one hit quota, expired the verified
   grant. New preparation failed with the expiry message; fresh verification
   uploaded only the failed file. The completed sibling remained complete.
6. **Throttling:** bounded synthetic requests to the isolated session endpoint,
   including changing spoofed XFF values from an untrusted peer, produced 2,701
   `429` responses with `Retry-After: 1`. Browser displayed the temporary wait
   message and recovered afterward. Separate deterministic HTTP tests verify
   unrelated established sessions remain usable and trusted proxy identities
   separate correctly. No external service was flood-tested.
7. **Cleaned-attempt recovery fix:** forced HEAD verification failures after one
   successful browser PUT. Manual retry remained finalize-first, with one asset,
   one verification call, and no second PUT. Expiry/cleanup then removed the
   pending asset and released its 77 B. Typed `asset_not_found` caused the UI to
   request a fresh verification. Retry completed one replacement asset; verifier
   calls became two, and all eleven previous ready assets remained ready.
8. **Direct data path/content checks:** object-store observations recorded browser
   PUT bodies (including the 524,902 B JPEG), followed by HEAD and GET
   `Range: bytes=0-511`. Go HTTP body-count tests confirm only bounded metadata
   crosses PhotoDrop for direct uploads. CSP permits configured historical object
   origins. The provider-switch Compose suite exercises local → S3-A → S3-B →
   local, historical authorization/finalization, mixed deletion, unavailable
   credentials, and successful retry once credentials return.

The browser fixture was compiled with
`go test -c -o .tmp/security-browser.test.exe ./internal/server`, run with
`PHOTODROP_BROWSER_FIXTURE=1` and `-test.run=^TestSecurityBrowserFixture$ -test.timeout=2h -test.v`.
It serves loopback only and has deterministic settings/expiry/cleanup endpoints.
Its optional widget simulator and injected verifier exist only in `_test.go`;
normal Go/Docker production builds contain neither test routes nor bypass flags.
The completed fixture was stopped and temporary tabs closed. The one-off rate
script and compiled browser fixture were removed; ignored logs/test data and the
pre-upgrade backup were retained. No production `.env` or real credentials were
copied into tracked artifacts.

## External validation limitations

The official Cloudflare test widget loaded its script, but hung with `300030` in
the available in-app browser. The same failure reproduced on an independent
minimal page **without Svelte or CSP**, isolating it from PhotoDrop's policy.
Deterministic widget/verifier browser scenarios above passed with production CSP
enabled and no new CSP errors. This establishes application behavior, not a live
Cloudflare widget interoperability pass. See [Cloudflare error guidance](https://developers.cloudflare.com/turnstile/troubleshooting/client-side-errors/error-codes/).

An actual Siteverify call with Cloudflare's published disposable test credentials
returned success for `example.com` without an action. It was deliberately **not**
accepted as a production success. Production still requires the configured
hostname and exact `photodrop_upload` action. Live production-key Turnstile and
private R2 interoperability remain unverified; neither production credentials nor
a live R2 bucket were supplied. No real challenge was bypassed.

GitHub private vulnerability reporting was checked and is disabled. `SECURITY.md`
documents the absence of a published private channel and asks reporters to request
one without disclosing vulnerability details publicly. No email was invented.

## Focused security review

Reviewed changed production paths and supporting auth/storage boundaries against
the supplied threat model. This is a focused implementation review, not a claim
of an exhaustive repository-wide security audit.

| Concern | Reviewed control/evidence |
| --- | --- |
| Client-only challenge, replay, wrong host/action | Server Siteverify before grant INSERT; required success/hostname/action; duplicate redemption cannot create a second grant; fixed HTTPS URL/context/body bound |
| Secret, token, presign leakage | Configuration formatting redaction; explicit public response shapes; no token SQL fields; safe verifier errors; log/content regression assertions |
| Siteverify/bcrypt flooding | Rate guards precede work; bounded process buckets; 8/2 concurrent work slots; short verification timeout |
| Proxy spoofing/NAT breakage | Peer trust checked before XFF; right-to-left validated chain; separate per-session control scopes; spoof/concurrency tests |
| Quota oversubscription/local length lies | IMMEDIATE SQLite reservation transaction; ready+pending sums; two-connection races; actual stream bounded by reserved size |
| Pending starvation/ready deletion | Capped periodic scans with cursors; active-event exclusion and fresh status check; only owned backend keys; missing credentials retain retry state |
| Expired-grant reuse/presign farming | Persisted expiry checked before local reservation/prepare/authorization; per-session bounds; completion alone remains permitted |
| Unbounded JSON/foreign origins | Existing strict bounded decoder; per-route caps; origin checks before guards; unchanged admin CSRF/session enforcement |
| S3 invalid accumulation/historical routing | Invalid objects deleted with retry metadata retained; periodic stale/retired cleanup; immutable backend routing; provider-switch tests |
| CSP regression | Self-hosted build works; direct object origins allowed; Cloudflare script/frame origin conditional; standalone widget limitation documented |
| Cleaned-grant recovery | Fixed typed 404 retry loop; stage-aware grant renewal; uncertain finalization retains IDs; backend/frontend/browser regressions pass |

Review found and fixed the cleaned-grant/pending-attempt retry loop. Browser work
also prompted a storage-quota display fix that preserves exact stored bytes and
a singular-second rate-limit message. No unresolved confirmed vulnerability was
identified in the reviewed Gate 5 paths. Remaining limits are deliberate: local
lost-response retries can duplicate an upload, per-process controls are not DDoS
protection, event quotas must be configured for an event-wide ceiling, missing
historical credentials defer cleanup, and S3 bytes arrive before verification.

## GitHub

See the PR checks and task handoff for branch/PR CI run links at the reviewed
revision. The PR is opened only after local validation and focused review; no
merge or Gate 6 work is part of this handoff.
