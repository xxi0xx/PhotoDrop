# Live deployment validation

**UNVERIFIED: live Cloudflare R2 interoperability.** No dedicated live-test R2
credentials/bucket are available in this task's environment. Deterministic signed
S3 fixtures do not establish live compatibility.

Owner procedure before public v1 release:

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

**UNVERIFIED: live production-key Turnstile flow.** No production widget keys and
appropriate public staging hostname are available here. Official test keys and the
injected verifier are not equivalent.

Configure [Turnstile](turnstile.md) for the actual public hostname. Create a staging
event, select several photos, complete the real widget, and verify one successful
Siteverify redemption/session per batch. Confirm expected hostname and action
`photodrop_upload` using protected temporary diagnostics without logging token,
secret, widget payload or Siteverify payload. Verify another batch obtains a new
grant, a rejected challenge cannot create one, and hostname/action mismatch fails
closed in existing server tests. Record only counts and safe outcomes. Remove staging
data and any diagnostics afterward. Never relax checks for localhost.

These checks are owner-run public-release readiness items, not claims made from CI.
