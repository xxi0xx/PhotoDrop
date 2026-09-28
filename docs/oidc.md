# Native OIDC administrator sign-in (unreleased v1.1)

PhotoDrop is the OpenID Connect relying party. The browser visits the provider
once during sign-in, returns to PhotoDrop, and receives PhotoDrop's normal local
administrator session. The provider is not in front of public pages or uploads.
This adds another way to enter the same administrator role, not multiple accounts,
profiles, roles or guest authentication.

## Modes and configuration

| `PHOTODROP_ADMIN_AUTH` | Password | OIDC |
| --- | --- | --- |
| `password` (default) | Required/enabled | Disabled; configuration ignored |
| `oidc` | Not required; endpoint disabled, even with an old database credential | Required/enabled |
| `password+oidc` | Required/enabled | Required/enabled |

Begin with `password+oidc` if you need a local fallback while verifying your IdP.
Set private values in `.env`/deployment secrets, then recreate PhotoDrop. Production
Compose forwards these variables. The minimal group-based configuration is:

```dotenv
PHOTODROP_ADMIN_AUTH=oidc
PHOTODROP_BASE_URL=https://drop.example.com
PHOTODROP_OIDC_ISSUER=https://auth.example.com/application/o/photodrop/
PHOTODROP_OIDC_CLIENT_ID=replace-with-your-client-id
PHOTODROP_OIDC_CLIENT_SECRET=replace-with-your-client-secret
PHOTODROP_OIDC_ALLOWED_GROUPS=photodrop-admins
PHOTODROP_OIDC_GROUPS_CLAIM=groups
PHOTODROP_OIDC_SCOPES=openid profile email
```

Register the exact redirect URI:

```text
https://drop.example.com/api/admin/oidc/callback
```

Issuer paths are supported and their spelling/trailing slash is preserved. Use
HTTPS for both public origin and provider. HTTP is accepted only for `localhost`
or loopback IPs in development. Discovery, token and JWKS endpoints must also use
HTTPS or loopback HTTP; redirects and insecure downgrades are rejected. Configure
the canonical issuer rather than a URL that redirects. Host/X-Forwarded-* never
determines the callback. No endpoint can be supplied through a login request.

At least one explicit allowlist is required. `PHOTODROP_OIDC_ALLOWED_SUBJECTS`
accepts comma-separated stable provider `sub` identifiers; group authorization
accepts exact case-sensitive names from the configured top-level ID-token claim.
If both are configured, authorization is **allowed subject OR any allowed group**.
A malformed group claim fails the entire login when groups are configured, even
if the subject matches. Missing groups can only pass through an allowed subject.
No authorization is derived from email, display name or username. Every permitted
identity has the same full administrator access; no identity profile is persisted.

## Authentik setup

Use Authentik's **OAuth2/OpenID provider** associated with a PhotoDrop application.
Choose a **confidential client**, authorization-code flow, and a signing key for
signed ID tokens (RS256 is a straightforward choice). Configure a strict redirect
URI equal to the value above; do not use a wildcard. Copy the provider client ID
and secret privately into PhotoDrop.

Use Authentik's default **per-provider issuer** mode and copy its exact issuer,
typically `https://auth.example.com/application/o/photodrop/`. Global issuer mode
serves discovery at a different path and is not supported by PhotoDrop's single
issuer/discovery setting. PhotoDrop discovers endpoints rather than hardcoding
Authentik routes. These setup details follow the
[official OAuth2 provider documentation](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/).

Enable the `openid`, `profile`, and `email` scope mappings. For group authorization,
ensure the chosen mapping includes a **string array in the ID token** under the
configured claim name. Authentik's profile mapping includes groups; inspect your
actual provider configuration because mappings can be customized. For a custom
scope, add it to `PHOTODROP_OIDC_SCOPES` and configure its mapping on the provider.
PhotoDrop always requests `openid`; it does not call UserInfo or request refresh
tokens by default. See Authentik's
[scope mappings](https://docs.goauthentik.io/add-secure-apps/providers/property-mappings/)
and [default mappings](https://github.com/goauthentik/authentik/blob/main/blueprints/system/providers-oauth2.yaml).

Create a dedicated group, add only intended administrators, and put its exact name
in PhotoDrop's allowlist. Alternatively obtain the stable `sub` privately from the
provider and allow it explicitly. Provider application access policy is useful,
but does not replace PhotoDrop's allowlist. Never post ID tokens to public JWT
debugging sites or issue reports.

Verify with a disposable/staging account: permitted login reaches `/admin`, an
unpermitted identity fails, and local logout leaves no PhotoDrop session. The
automated integration uses a disposable signed standards fixture, not an owner's
Authentik deployment; it does not claim live Authentik-version certification.

## Protocol and local session lifecycle

Public `GET /api/admin/auth/methods` exposes only two booleans. The login page
shows a password form, **Sign in with SSO**, or both. `GET /api/admin/oidc/login`
starts authorization-code flow with PKCE S256. The fixed callback is
`GET /api/admin/oidc/callback`; success redirects only to `/admin`. Failures return
only `/admin/login?error=oidc`, with no upstream details or arbitrary return URL.

Each transaction has independent 256-bit random state, nonce, PKCE verifier and
browser binding. Migration 011 stores state/nonce/browser hashes, the short-lived
verifier, and creation/expiry times. A five-minute HttpOnly SameSite=Lax cookie
binds the initiating browser; it is Secure under HTTPS and scoped to
`/api/admin/oidc`. Only the latest started flow in one browser is usable. Callback
atomically consumes valid unexpired browser-bound state before exchange; replay
and failed-flow reuse are rejected. Expired rows are cleaned in bounded batches
on initiation; the table has a 512-transaction admission cap.

Maintained Go OIDC/OAuth2 libraries perform discovery, exchange and signed-token
verification: exact issuer, audience/client ID, expiry, JWKS signature, plus
PhotoDrop nonce, nonempty subject and allowlist checks. Multiple audiences require
matching `azp`; if present it must match the client ID. No implicit flow is used.
Provider tokens/codes are never written to SQLite, cookies, frontend storage or
logs. Successful discovery/JWKS are cached in memory; failures are not permanently
cached. Requests are time/size bounded; login has separate concurrency and rate
limits from password login and guest uploads.

Both login methods replace any supplied prior session with a fresh random local
token. Only SHA-256 bearer hashes are stored, with the existing synchronizer CSRF
token and 12-hour expiry. Cookies are HttpOnly/SameSite=Lax and Secure under HTTPS.
Admin mutations still require same-origin evidence and CSRF. Password verification
retains bcrypt cost 12; real password rotation revokes **all** local sessions and
prevents an old password manager from creating a new password session.

Once logged in, provider outages do not affect admin sessions, public events,
guest uploads, health or the optional password fallback. Startup never discovers
the provider. New SSO attempts may fail during an outage; retry after recovery.
If discovery is cached, the browser may first show the unavailable provider page.
Logout revokes only the PhotoDrop session, not the provider SSO session. Clicking
SSO again may immediately sign in through that still-active provider session.

Authorization is evaluated at login only. Removing a group membership, changing
allowlists or disabling a login method does not revoke already-issued local
sessions; they last at most 12 hours. For immediate revocation, stop PhotoDrop and
use a trusted SQLite tool to delete `admin_sessions`, or rotate the configured
password in a password-enabled mode. Do not edit tokens or invent a user table.

## Troubleshooting and recovery

- Check auth mode, BASE_URL and exact registered callback first. OIDC-only needs
  no password; password-enabled modes still fail startup with an invalid password.
- Verify issuer spelling/path/trailing slash and canonical, nonredirecting HTTPS
  discovery. Authentik's per-provider issuer mode is expected.
- Ensure the PhotoDrop container can reach discovery, token and JWKS endpoints;
  do not put interactive bot challenges in front of server-to-server endpoints.
- Check client credentials privately and group claim/scopes/allowlists at the IdP.
  A valid IdP login without an authorized subject/group must be rejected.
- Start a new login after expiry, replay, interrupted exchange, or another tab
  replacing the binding cookie. Do not retry a consumed callback URL.
- If locked out, privately configure `password+oidc` with a strong password and
  recreate PhotoDrop. Do not weaken issuer/signature/nonce/group checks.
- Redact cookies, state, nonce, authorization codes, provider tokens/client secret
  and private identity claims from reports. Avoid proxy access logging of callback
  query strings; PhotoDrop itself logs only generic login outcomes.

See [configuration](configuration.md), [upgrade](upgrading.md), and
[backup/restore](backup-restore.md). No Authentik forward-auth, guest OIDC,
token refresh, provider logout propagation or separate authentication container
is required by PhotoDrop.
