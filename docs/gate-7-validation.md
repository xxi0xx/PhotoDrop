# Gate 7 validation record

Baseline: merged Gate 6 `7014b5bfb6a14265ce0e248e85ceac34881eaeaf`.
Branch: `codex/gate-7-production-ux`. Validation completed across September
17–23, 2026. Scope is production UX; no Gate 8 work is included.

## Implementation and fixes found during validation

- Migration `009_contributors.sql` adds nullable `contributor_name` columns to
  upload sessions and assets. Session creation validates/normalizes the name;
  local/direct asset creation copies it from the persisted session. Existing
  assets remain NULL. The protected event detail includes a bounded summary.
- A local, pinned `qrcode` encoder renders an accessible canvas and a downloadable
  1024px PNG. The QR contains exactly the existing resolved public URL. No QR
  service, tracking request, or CSP expansion was added.
- Guest selection, progress, partial failure, retry, completion, and add-more
  views retain the existing upload/finalization pipeline. Names are fixed per
  batch. Successful files are excluded from retry; `Retry-After` gates buttons.
- Browser validation found that a shared plain batch object received separate
  Svelte deep proxies in each file row, creating three grants. `UploadBatch`
  preserves object identity with reactive class fields. A compiled Svelte runtime
  test now proves three proxied rows share one grant, promise, and name.
- Browser inspection found QR encoder inline dimensions stretching the canvas
  on mobile. Removing only those inline dimensions lets CSS display a square
  208px QR while preserving the 1024px download.
- Turnstile uses supported compact sizing. Cloudflare's flexible size has a
  300px minimum, exceeding the inner card at 320px. Production hostname/action
  validation is unchanged. The injected widget/verifier remain test-only.
- Login errors are associated with their field; cancelling delete confirmation
  restores focus; completion focuses its heading; cancellation after grant
  creation no longer starts a local body transfer.

## Commands and results

All commands ran from the repository root (PowerShell equivalents for variables):

```sh
npm ci --prefix web
npm run check --prefix web
npm test --prefix web
npm run build --prefix web
gofmt -l cmd internal migrations scripts web/embed.go
CGO_ENABLED=0 go test -timeout 180s ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/photodrop.exe ./cmd/photodrop
docker compose config --quiet
bash scripts/smoke-compose.sh
node scripts/smoke-backends.mjs
docker compose -f scripts/compose-immich-test.yml build
node scripts/smoke-immich.mjs --browser
git diff --check
```

Frontend clean install/audit passed (zero reported vulnerabilities), Svelte check
had zero errors/warnings, **34 tests passed**, and production build passed.
Formatting, full CGO-free Go tests/build, and vet passed. Full Linux race/vet
passed using the established build-container workflow:

```sh
docker run --rm -v "$PWD:/src" -w /src \
  -v photodrop-go-cache:/go/pkg/mod \
  -v photodrop-go-build-cache:/root/.cache/go-build \
  golang:1.26 sh -ec 'go test -race -timeout 180s ./... && go vet ./...'
```

Git Bash used `MSYS_NO_PATHCONV=1`. Docker Desktop's CLI directory was added to
PATH. Production Compose smoke passed again after the final focus/copy edits:
HTTP/auth/events/local media, non-root UID, minimal runtime, stable SQLite bytes
across restart, and graceful SIGTERM. Production remains one container plus data.

The final resumed clean frontend and Windows Go checks passed again. A redundant
Linux rerun could not start because Docker Desktop encountered its own stale
`sailor-ingest.sock` startup error. The prior full Linux race/Compose/provider/
Immich passes remain applicable: no production Go behavior changed afterward.
Fresh Linux branch/PR CI provides the final remote gate; its links/status belong
to the PR, not an assertion about the broken local Docker startup.

## Browser lifecycle and recovery

Used the in-app browser initially and then Playwright Chromium against the same
disposable loopback fixture at port 8082. Real Immich used the separate port-8083
Compose fixture. Generated PNGs and fixture names were not real guest data.

1. Admin created/opened the event, copied its exact public URL, displayed the
   QR, downloaded the PNG, and decoded it with `jsqr`/`pngjs`. The decoded URL
   equalled the displayed/copied event URL. Encoder tests also prove deterministic
   output without network access, a white quiet zone, and unsafe URL rejection.
2. At 375px, selected three photos with `María 李`, observed overall and per-file
   progress, and forced one local 503. Two rows stayed Uploaded. Retry sent exactly
   one more local body (3 → 4), with the session counter remaining 1. Completion
   removed progress bars and focused “Thanks for sharing!”.
3. Add-more selected two new photos with `María & David`. Admin showed 7 photos /
   483 B: five with the earlier label (including two pre-existing fixture photos)
   and two with the later label. Earlier attribution was unchanged.
4. At 320px, one deterministic verification and one session covered three direct
   S3 photos. Counters showed 3 object-store PUTs and **0 PhotoDrop media bodies**.
   A forced finalization failure left two complete. After expiring the grant,
   retry finalized the existing object: PUTs stayed 3, sessions stayed 1, verifier
   calls stayed 1, and all three became ready. No new prepare/upload was needed.
5. Cleanup of a stale pending attempt preserved 10 ready assets, removed its
   69-byte reservation, and caused the UI to request fresh verification. Retry
   added exactly one ready asset and one PUT. The batch name remained unchanged.
   A separately cleaned empty grant likewise recovered with one new verification,
   one PUT, and one ready asset; no successful sibling was selected again.
6. An injected 429 with `Retry-After: 3` disabled retry, then allowed it after the
   interval (observed completion after 3193ms). No automatic upload loop ran.
7. Backend/frontend lost-PUT and lost-completion tests preserve request/session/
   asset IDs and finalize first. An additional browser TCP-response-drop experiment
   reached success on the same stored asset. Chromium transparently replayed the
   idempotent PUT after the closed connection; `If-None-Match: *` rejected a second
   write. This is a transport retry, not a second PhotoDrop asset. A strict “one
   TCP request” harness assertion was therefore inappropriate; the finalization
   browser evidence in step 4 and existing recovery tests establish the required
   application behavior. No production recovery logic was weakened for the test.
8. HTML-looking `<img ...>` attribution with accented/non-English text appeared
   literally in the admin summary, with no injected image element. Six recovered
   direct photos retained that name. Public response/log exclusion tests passed.
9. Disabling, expiring, and reopening the event preserved the entire QR data URL
   and public URL. Closed and missing event states were distinct. A finite quota
   produced the guest message to contact the host, with existing photos preserved.
10. Admin sections, pending/ready/storage data, export command copy, and disposable
    event creation/deletion passed. The event editor separates Event, Share,
    Guest uploads, Immich, Export, and Danger zone.

The response-drop experiment initially used browser interception that did not
preserve the uploaded binary as intended. The existing S3 fixture's response-drop
behavior was used to diagnose this instead. Temporary instrumentation was removed;
there is no new production fault injection or acknowledgment bypass.

## Mobile and accessibility

- **320, 375, and 430 CSS pixels**: guest selection/completion, admin QR/forms,
  and long-filename progress checked with DOM bounds and screenshots. Page
  scroll width matched viewport width. QR display measured 208 × 208. Upload
  buttons measured 45px high. Full filenames remain available in text/title
  while narrow rows use ellipsis. Compact test-widget dimensions fit at 320px.
- Keyboard flow passed: home link → contributor → chooser → upload → failed
  retry → completion → add more. Retry had a visible solid focus outline; Enter
  activated upload/retry/add-more. Completion focused its heading; add-more
  focused the chooser. Failure summaries use `role=status`.
- Admin Copy link → Visit guest page keyboard order and visible focus passed.
  Event inputs had associated labels. QR has a descriptive image role. Native
  buttons/selects preserve keyboard operation, including the Immich controls.
  Delete confirmation focused the destructive button; Keep event restored the
  original button. A newly created disposable event was deleted successfully.
- Invalid contributor and login errors were connected through `aria-describedby`
  and `aria-invalid`. Progress is labelled, completion/status is live, and statuses
  use text as well as color. Normal text/focus/notice/error colors were reviewed
  against their backgrounds. No new animation was introduced.
- Browser console errors were accounted for by deliberately injected 403/404/429/
  503/network failures, fixture restarts, and test ports sharing a localhost cookie.
  No unexplained application exception or warning remained. This is a focused
  Chromium/keyboard/accessibility review, not formal WCAG certification or a
  physical iOS/Android/screen-reader certification.

## Preserved Gates 1–6

- Provider regression passed local → S3-A → S3-B → local, historical refresh/
  finalization, mixed deletion, and missing-credential recovery.
- Mixed local/historical S3-A/S3-B export passed original-file SHA-256 checks,
  safe collision filenames, manifest verification, and ready-only selection.
- Real pinned Immich 3.2.1 passed four-permission auth, album creation/assignment,
  external album rename, incremental import, failed-only retry, lost-response
  deduplication, revoked/missing credentials, and offline health behavior.
  Abrupt restart recovered job 13 with 16 assets accounted for, ending at 27.
  Deleting the PhotoDrop event left all 27 real Immich copies intact.
- Immich admin browser connection test showed Connected, imported the initial
  photo, then sent two new photos with one forced failure. Failed-only retry
  increased real proxy upload count **35 → 36**, ending at 3 imported / 0 failed.
- Migration tests cover fresh DB, actual 001–008 upgrade, restart, existing
  event/asset/Immich state, NULL attribution, and checksum/application-time
  preservation. Git diff confirms migrations 001–008 are byte-for-byte unchanged.

## Focused privacy/security review

Reviewed changed runtime paths, migration, dependencies, supporting auth/storage
contracts, and tests. This is a focused implementation review, not a separate
exhaustive Codex Security scan.

| Boundary | Result |
| --- | --- |
| Stored XSS / HTML | Normal Svelte escaping; no `{@html}`; literal-name browser assertion |
| Name validation | Server trims whitespace, validates UTF-8, rejects internal controls and >100 runes; bounded session JSON |
| Attribution spoofing/mutation | Name enters only at session creation; assets copy the event-bound session value transactionally; retries do not rewrite it |
| Public/admin separation | Explicit public DTOs omit attribution; existing admin session/CSRF/origin protections guard summaries |
| QR leakage | Only the public event URL; local encoder; no tokens/credentials/names; HTTP(S) and no URL userinfo |
| Logs/browser storage | Contributor/log exclusion tests; no name/token/grant localStorage or sessionStorage |
| Provider errors | Direct upload ignores raw provider bodies; backend safe errors preserved; status mapping remains user-facing |
| Retry identity | Completed rows excluded; shared batch grant; uncertain outcomes retain IDs and finalize first; cleanup is authoritative |
| Turnstile | Strict hostname/action and replay boundary unchanged; browser verifier/widget are in `_test.go` only |
| Deployment/scope | No new service, public gallery, account system, browser ZIP export, or Gate 8 release/community infrastructure |

No unresolved confirmed security finding was identified. Names remain unverified
labels, up to 100 summary groups are shown, selection/recovery state is in-memory,
and live production Turnstile/R2 interoperability is not newly claimed. The
existing export/Immich operating limitations continue to apply.

## Handoff

See [operating behavior](production-ux.md). Temporary browser scripts, screenshots,
logs, test images/state, and generated executables are removed before commit;
intentional fixture/test source and pre-upgrade backups are retained. Disposable
Docker projects can be removed when the local engine is available; no production
data or Docker factory reset is part of this change. The PR must have green CI
and real Immich workflows for both push and pull_request and be mergeable before
handoff. Merging and Gate 8 are outside this task.
