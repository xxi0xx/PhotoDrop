# HTTPS and reverse proxies

Use HTTPS publicly. PhotoDrop listens HTTP behind trusted infrastructure. Set
`PHOTODROP_BASE_URL=https://drop.example.com` to the exact browser origin, without
a subpath. It governs links, Origin/Referer checks, Secure cookies and HSTS; it
does not configure TLS or trust forwarded headers.

Restrict port 8080 to the intended proxy. For a host-local proxy, change the
Compose binding to `127.0.0.1:8080:8080`. For a remote proxy use a private network
and firewall. Do not cache API/admin/event responses. Pass Origin/Referer unmodified.

An external nginx TLS virtual host can use:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 50m;
    proxy_read_timeout 600s;
}
```

Configure TLS certificates/listener separately. Match the intended per-file body
limit; the smallest hop limit wins. This single-edge example replaces untrusted
XFF. Multi-proxy setups must maintain an accurate chain.

By default rate limits use the socket peer. Configure PHOTODROP_TRUSTED_PROXY_CIDRS
only for controlled proxy addresses as actually seen by PhotoDrop (Docker NAT may
change them). XFF is read right-to-left only from a trusted peer. Invalid chains
fall back to that peer. CF-Connecting-IP, X-Real-IP and Forwarded are not identity
sources. Forwarded scheme does not define the public origin. Never trust all networks.

## Cloudflare Tunnel / proxy

An external Tunnel can route `drop.example.com` to `http://localhost:8080` when
cloudflared runs on the host. In another container, localhost means that container;
configure a private network/address instead. Keep cloudflared separate and protect
its token. See [Cloudflare's published application guide](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/).

Cloudflare-proxied body limits depend on plan/zone settings and can reject local
uploads before PhotoDrop sees them. Check [official 413 guidance](https://developers.cloudflare.com/support/troubleshooting/http-status-codes/4xx-client-error/error-413/)
(reviewed 2026-09-23). Increasing PHOTODROP_MAX_FILE_SIZE does not increase the proxy
limit. There is no resumable/multipart workaround in PhotoDrop. Direct S3/R2 sends
media to object storage, avoiding the PhotoDrop proxy body path; small control
requests still cross it. R2 is optional.

## Alongside Immich

```text
photos.example.com -> Immich (its own database/library)
drop.example.com   -> PhotoDrop (/data and optional S3)
PhotoDrop         -> Immich HTTP API
```

The same NAS/host is fine; never mount Immich's managed library into PhotoDrop.
Each target has one origin used for API calls and browser links. Choose an origin
reachable by both or appropriate split DNS. Separate internal/public URLs are unsupported.
