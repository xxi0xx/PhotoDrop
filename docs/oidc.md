# Native OIDC sign-in for administrators and hosts

PhotoDrop is the OpenID Connect relying party. The browser visits the provider
once during sign-in, returns to PhotoDrop, and receives PhotoDrop's normal local
session. The provider is not in front of public pages or uploads. Each identity
becomes a **host**: an administrator, who sees and manages every event and host,
or a host, who sees and manages only its own events. Guests never sign in.

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

At least one of four explicit role lists is required. The subject lists accept
comma-separated stable provider `sub` identifiers; the group lists accept exact
case-sensitive names from the configured top-level ID-token claim.

| Variable | Grants |
| --- | --- |
| `PHOTODROP_OIDC_ALLOWED_SUBJECTS`, `PHOTODROP_OIDC_ALLOWED_GROUPS` | **admin** |
| `PHOTODROP_OIDC_HOST_SUBJECTS`, `PHOTODROP_OIDC_HOST_GROUPS` | **host** |

Within a role, a listed subject **OR** any listed group matches. Admin wins when an
identity matches both roles. An identity that matches no list is refused. A
malformed group claim fails the entire login when any group list is configured,
even if the subject matches. Missing groups can only pass through a listed subject.
No authorization is derived from email, display name or username: PhotoDrop stores
them only to label the host. Keep `profile` and `email` in `PHOTODROP_OIDC_SCOPES`
for those labels and for Immich account linking.

To add hosts, create a group such as `photodrop-hosts`, add the hosts to it, and
set `PHOTODROP_OIDC_HOST_GROUPS=photodrop-hosts`. Administrators stay in the
existing admin group. A deployment that sets no host list behaves as before.

## Hosts

PhotoDrop keeps one row per identity (issuer plus `sub`) with its role, display
name and email. The role is derived again at **every** sign-in, so a group change
takes effect at the next sign-in. Host 1 is the original administrator:

- Password sign-in, where enabled, always signs in host 1, and host 1 then stays an
  administrator whatever lists its OIDC identity is in.
- After upgrading to multi-host, the **first admin OIDC sign-in claims host 1**.
  That administrator keeps every event created before the upgrade. Sign in as the
  administrator before any other admin does.
- Every other identity becomes a new host on its first sign-in.

Each event belongs to one host: whoever created it. A host's requests for another
host's event get `404`, as if it did not exist. Administrators open **Hosts** to
see every host and to disable or enable one; hosts are never deleted. Disabling
signs that host out at once and blocks further sign-ins; its events stay. Nobody
can disable their own host. An administrator can move an event to another host
from the event page, but only before an Immich album exists for it.

### Immich account linking

Hosts link their own Immich accounts through Immich's OAuth sign-in (see
[linking](export-immich.md#linking-a-hosts-own-immich-account)). On the identity
provider's client for **Immich**, add the redirect URI
`https://drop.example.com/api/admin/destinations/immich/callback` (strict match).
Compare the Subject mode of the PhotoDrop and Immich providers (authentik:
Applications → Providers → each provider → Advanced protocol settings). If they
differ, set `PHOTODROP_IMMICH_<KEY>_LINK_MATCH=email`; otherwise the first link
attempt reports a subject mismatch and creates nothing. A host's email is stored
only when the ID token says `email_verified: true`, so email matching needs the
provider to send that claim.

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
retains bcrypt cost 12; real password rotation revokes host 1's local sessions and
prevents an old password manager from creating a new password session. Other
hosts' sessions are unaffected.

Once logged in, provider outages do not affect admin sessions, public events,
guest uploads, health or the optional password fallback. Startup never discovers
the provider. New SSO attempts may fail during an outage; retry after recovery.
If discovery is cached, the browser may first show the unavailable provider page.
Logout revokes only the PhotoDrop session, not the provider SSO session. Clicking
SSO again may immediately sign in through that still-active provider session.

The role is evaluated at sign-in. Removing a group membership, changing role lists
or disabling a login method stops the next sign-in, but does not revoke an
already-issued local session; it lasts at most 12 hours. For immediate removal, an
administrator **disables** the host on the Hosts page, which deletes its sessions.
Rotating the configured password revokes host 1's sessions. Do not edit tokens.

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
[backup/restore](backup-restore.md), plus the [historical OIDC validation record](v11-phase4-validation.md).
No Authentik forward-auth, guest OIDC,
token refresh, provider logout propagation or separate authentication container
is required by PhotoDrop.
