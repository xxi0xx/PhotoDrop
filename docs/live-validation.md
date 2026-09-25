# Live deployment validation

## Production validation status

Reference validation **PASSED**, owner-completed on **2026-09-24 America/Chicago**
against `https://drop.mariascloud.com`, using the private R2 bucket
`mariascloud-photodrop` and real production Turnstile keys. These are sanitized
owner-reported outcomes, not a new Codex live test or a claim of provider certification.

### Cloudflare R2

- PhotoDrop created real direct-upload plans. Browser control requests went to
  `drop.mariascloud.com`; CORS preflights and image PUTs went directly to R2.
- Preflights returned HTTP 204 and allowed origin `https://drop.mariascloud.com`,
  method PUT, and headers Content-Type and If-None-Match.
- Image PUTs returned HTTP 200, sending `Content-Type: image/jpeg`,
  `If-None-Match: *` and the expected Origin. Responses allowed the same origin
  and exposed ETag. PhotoDrop completion calls returned HTTP 200.
- Objects appeared under the expected PhotoDrop event asset prefix; reported
  storage totals agreed. Deleting the test event removed its corresponding objects,
  leaving that prefix empty.
- No direct-upload media body passed through the PhotoDrop/Cloudflare Tunnel origin.

### Production Turnstile

The real client loaded from `challenges.cloudflare.com`; the user completed a real
challenge. The browser submitted its production response token to PhotoDrop's
upload-session endpoint on `drop.mariascloud.com`, which returned HTTP 201.
The normal direct-R2 flow then succeeded for both uploads and both completions.

Production session creation requires Siteverify success, the hostname derived from
PHOTODROP_BASE_URL, and action `photodrop_upload`. Successful session creation
therefore confirms that the server-side result passed these strict checks.
Siteverify is server-to-server and was **not** visible in the browser HAR. No token,
HAR, signed URL, account credential or authorization material is retained here.

The report establishes these specific browser/upload/deletion outcomes. It does not
claim all S3 providers work, a formal certification, or separate live instrumentation
of bounded reads/export hashes beyond the owner's supplied observations.
Deterministic regression evidence remains separate in [Gate 8 validation](gate-8-validation.md).

## Recommended validation for a new deployment

These procedures remain useful even though reference production validation passed.
Official test keys and injected verifiers are not production validation.

### R2 procedure

1. Prepare a private disposable R2 bucket and scoped Object Read & Write credentials.
   Keep them in private environment/secret storage, not a commit or command log.
2. Follow [S3/R2 configuration](storage.md), using a unique disposable prefix and
   exact HTTPS PhotoDrop origin in bucket CORS. Keep a record of only the keys this
   test creates. Do not use a production bucket-wide cleanup command.
3. Create a staging event and upload two tiny known images in a real browser.
   In developer tools observe PhotoDrop session/prepare controls, media PUT to the
   R2 origin, then PhotoDrop finalization. Record methods/origins/statuses only:
   redact query strings, tokens, signatures, cookies and authorization headers.
4. Verify HEAD and the bounded bytes=0-511 verification read with safe instrumentation
   or provider telemetry. Confirm PhotoDrop receives no media upload body. A successful
   SDK PUT alone does not verify browser CORS.
5. Export the event and compare SHA-256 with the originals. Delete the staging event;
   verify that exactly its recorded remote objects disappear. Clean up only test
   objects/prefix/configuration and record endpoint mode, date, result and limitations.

### Turnstile procedure

Configure [Turnstile](turnstile.md) for the actual public hostname. Create a staging
event, select several photos, complete the real widget, and verify one successful
Siteverify redemption/session per batch. Confirm expected hostname and action
`photodrop_upload` using protected temporary diagnostics without logging token,
secret, widget payload or Siteverify payload. Verify another batch obtains a new
grant, a rejected challenge cannot create one, and hostname/action mismatch fails
closed in existing server tests. Record only counts and safe outcomes. Remove staging
data and any diagnostics afterward. Never relax checks for localhost.

These are operator-run deployment checks, not claims made from CI.
