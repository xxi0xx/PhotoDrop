# Security and abuse controls

PhotoDrop intentionally lets anyone holding an event link submit images without
an account. Links may leak or be forwarded; random event IDs are not passwords.
Guest headers, names, MIME declarations, tokens, and image bodies are untrusted.
Gate 5 bounds anonymous grants and reservations and reduces request/CPU abuse.
It assumes one application process per data directory and a trusted administrator
and host. Compromised administrator/storage credentials, hostile host access,
distributed limits, and full DDoS mitigation are outside this model.

## Optional Turnstile

Set both `PHOTODROP_TURNSTILE_SITE_KEY` and `PHOTODROP_TURNSTILE_SECRET_KEY`, or
leave both empty. Partial configuration fails startup. When enabled, also set
`PHOTODROP_BASE_URL` to the exact public origin, for example `https://photos.example.org`.
The site key is public; keep the secret only in server environment/secret storage.
Configure that hostname in the Turnstile dashboard. PhotoDrop itself need not be
proxied by Cloudflare. No Worker, sidecar, or Cloudflare storage is required.

After selecting photos, the guest completes verification once to create an
upload session. The Go server redeems the token over HTTPS Siteverify and requires
`success`, the hostname from `PHOTODROP_BASE_URL`, and action `photodrop_upload`.
Browser claims are never authoritative. Transport has a five-second timeout,
16 KiB response bound, no redirects, and no automatic redemption retries. Tokens
are single-use at the provider; ambiguous failures require a new widget token.
See [Cloudflare validation](https://developers.cloudflare.com/turnstile/get-started/server-side-validation/).

New sessions fail closed if validation fails or the provider is unavailable.
Existing valid grants, event/admin browsing, and `/healthz` continue working.
Enabling Turnstile does not retroactively revoke existing grants. Challenges are
not repeated for each file, presign refresh, or finalization within a valid grant.
The script loads only for configured challenge UI after photo selection. Tokens
stay in page memory until consumed/reset, never URLs, localStorage, SQLite, or logs.
The server does not send the guest IP to Siteverify. Loading the widget still
connects the guest's browser to Cloudflare under its privacy terms.

The deterministic browser verifier and optional widget simulator live exclusively
in a Go `_test.go` fixture bound to loopback. They have no production configuration,
header, query switch, or production route. Official provider test credentials may
return placeholder hostnames or omit action; production validation never relaxes
those checks to accept test credentials.

## Temporary grants and quotas

| Environment variable | Default | Accepted range |
| --- | --- | --- |
| `PHOTODROP_UPLOAD_SESSION_TTL` | `2h` | Go duration, `1m`–`24h` |
| `PHOTODROP_UPLOAD_SESSION_MAX_ASSETS` | `100` | 1–1,000 |
| `PHOTODROP_UPLOAD_SESSION_MAX_BYTES` | `5368709120` (5 GiB) | 1–1 TiB, integer bytes |
| `PHOTODROP_RATE_LIMIT_MULTIPLIER` | `1` | 1–100 |
| `PHOTODROP_RATE_LIMIT_DISABLED` | `false` | `true` or `false` |
| `PHOTODROP_TRUSTED_PROXY_CIDRS` | empty | Up to 32 comma-separated CIDRs |
| `PHOTODROP_TURNSTILE_SITE_KEY` | empty | Public widget key; paired with secret |
| `PHOTODROP_TURNSTILE_SECRET_KEY` | empty | Server-only key; paired with site key |

Expiry and per-session bounds are persisted when each grant is created. Changes
to defaults apply to future grants. Expired grants cannot start local uploads,
prepare assets, or refresh presigns. An already-authorized S3 pending object can
still finalize after grant expiry if it passes the original content/size checks;
repeated completion remains idempotent. A local upload admitted before grant
expiry can finish, subject to the existing event-open policy and upload deadline.
Retrying after expiry obtains a new grant/verification for failed work and retains
completed photos. If cleanup has definitively removed a grant or pending attempt,
its typed 404 response also allows a new grant on retry. Network errors, generic
404s, server failures, or uncertain finalization keep the existing IDs and attempt
finalization first. This is page-session recovery, not persistent resumability.

Administrators can optionally set `max_assets` (1–1,000,000) and `max_bytes`
(1–1 PiB). The editor displays GiB while storing integer bytes; saving an unchanged
display preserves the original exact byte value. Empty limits are unlimited.
**Set event quotas when sharing a public link if you need a resource ceiling.**
Per-session limits alone do not prevent a guest obtaining multiple grants.

Quota usage is ready photo count/actual bytes plus pending count/expected bytes,
across local, active S3, and all historical backends. Checking capacity and inserting
the pending row use one IMMEDIATE SQLite transaction. The pending row itself is
the durable reservation; completion converts it into actual usage without double
counting. Unknown-length local streams reserve the per-file maximum; known-length
streams cannot write beyond their reservation. Lowering limits deletes nothing;
over-quota events show a warning and reject new reservations. Raising/removing a
limit restores admission. Counts and reservations survive restarts.

Migration `007_security_abuse.sql` adds nullable event quotas and persisted grant
expiry, bounds, and optional verification time. Migrations 001–006 are unchanged.
Legacy sessions expire deterministically at the Unix epoch; ready assets remain
intact. Legacy local pending rows conservatively reserve 1 GiB until cleanup.
Back up `/data` before upgrading; rolling back to an older binary is unsupported.

## Reclamation

Startup retains its two-second cleanup budget. During uptime a single maintenance
loop runs every five minutes with a five-second context budget. Each pass considers
up to 1,000 stale pending assets, up to 1,000 retired S3 keys, and deletes up to 5,000
expired grants with no assets. Pending creation and latest authorization must both
be more than an hour old; ready assets are excluded. Cursors let later records
progress even if earlier historical credentials are unavailable. Event locks and
a fresh eligibility check prevent cleanup racing active upload/finalization.
Shutdown cancels and joins maintenance.

Cleanup deletes only recorded, validated keys through the recorded durable backend.
Missing credentials/provider errors retain retry metadata and reservations; they
may delay capacity recovery. Invalid S3 objects are deleted on verification but
their pending reservation remains for same-key retry until stale cleanup. Retired
keys remain recorded and are checked again to remove late arrivals. Those records
grow with deletions; cleanup is eventual, and outages/backlogs can extend delays.
No new container, queue, Redis, or required cron job is introduced.

## Rate limiting and proxy trust

Token buckets refill continuously; the default burst equals one minute's allowance.
All listed rates are multiplied by `PHOTODROP_RATE_LIMIT_MULTIPLIER`.

| Route group | Scope | Requests/minute (burst) | Process ceiling/minute |
| --- | --- | --- | --- |
| Administrator login | Resolved IP | 10 | 60 |
| New upload session | Event + resolved IP | 120 | 600 |
| Local upload, prepare, authorize | Separate route + event + session | 120 each | 6,000 combined control |
| Complete | Event + session | 300 | Same 6,000 control ceiling |

After grants exist, guests behind one venue NAT have separate route/session
allowances. Process ceilings still protect shared resources. Checks run before
Siteverify/bcrypt; additional semaphores cap concurrent verification at eight and
password checks at two. `429` carries integer `Retry-After` seconds and a safe
message; retry after that delay. Turning rate limiting off retains semaphores,
origin checks, grant bounds, and quotas but removes these request-rate defenses.

Limiter state is concurrency-safe, capped at 10,000 SHA-256-keyed buckets, and
prunes entries idle for ten minutes at most once a minute. At capacity, new keys
are rejected for 60 seconds instead of evicting active keys to reset allowances.
Restart clears counters. Limits are per process, not coordinated across replicas.
Addresses exist transiently while handling requests and key derivation. Raw guest
addresses are not stored in the database or application security logs. Hashed
buckets are transient, not a promise of IP anonymization. Configure your reverse
proxy/access-log retention separately.

By default PhotoDrop ignores all forwarded client-IP headers and uses the socket
peer. Only when that peer is within `PHOTODROP_TRUSTED_PROXY_CIDRS` does it parse
`X-Forwarded-For`, walking from right to left through trusted hops and stopping at
the nearest untrusted address. The whole chain must be valid, at most 32 hops and
4 KiB; malformed/empty chains fall back to the peer. IPv4-mapped IPv6 is normalized.
`X-Real-IP`, `Forwarded`, and `CF-Connecting-IP` are not identity sources.

**Do not configure a proxy CIDR as trusted unless traffic reaching PhotoDrop from
that network is actually controlled by the administrator.** Configure only the
actual reverse-proxy address/subnet, restrict direct access to the backend, and
have the edge proxy replace untrusted incoming XFF or append the observed peer
correctly. For example, trust `10.20.0.5/32` only if that host is your controlled
proxy. Never trust `0.0.0.0/0` or `::/0` to make forwarding work. Docker NAT may hide
the original peer; configure the real proxy hop deliberately. Cloudflare is optional.

## Origins, bodies, headers, and remaining limits

State-changing public controls require the exact configured browser origin (or
the request origin when BASE_URL is empty); a same-origin Referer is the fallback.
Requests with neither Origin nor a same-origin Referer are rejected, including
scripts: non-browser clients must supply the expected Origin. This blocks browser
drive-by requests, not an attacker who can set HTTP headers. Admin session/CSRF
protections remain required. Direct image PUTs go to the object-store origin and
use its CORS policy; PhotoDrop never proxies those image bodies.

Login/control JSON is bounded (1 KiB login/logout/delete/refresh/finalization,
4 KiB session/preparation, 32 KiB event editing); unknown fields and trailing JSON
are rejected. Local media
keeps its configured per-file stream bound and deadline. Public event, API, and auth responses use
`nosniff`, `no-referrer`, `X-Frame-Options: DENY`, `Cache-Control: no-store`, and a
Permissions-Policy disabling camera, microphone, geolocation, and payment.
CSP permits self-hosted scripts/styles, denies objects/embedding/base changes,
permits configured S3 origins for connections, and allows Cloudflare script/frame
resources only when Turnstile is enabled. HTTPS BASE_URL or direct TLS adds HSTS
`max-age=31536000`; HTTP localhost does not. No preload/includeSubDomains is set.
Set the correct HTTPS public URL behind TLS termination. See
[Turnstile CSP guidance](https://developers.cloudflare.com/turnstile/reference/content-security-policy/).

Security logs record safe categories/scopes for throttling, verification/origin
rejection, expiry, quota failures, and cleanup. They omit passwords, grant/admin
session IDs, CSRF/challenge tokens, presigned URLs, storage secrets, and image bodies.
Health reflects the process/database, not provider availability or quota exhaustion.
S3 bytes reach storage before final verification: malicious clients with a valid
presign can temporarily exceed declared bytes or upload invalid content. Use
provider cost controls and carefully scoped credentials; application quotas bound
accepted reservations, not all pre-verification provider traffic. File sniffing is
not antivirus or image decoding. No gallery, download, export, Immich, QR, video,
multipart, contributor identity, or other Gate 6 functionality is provided.
