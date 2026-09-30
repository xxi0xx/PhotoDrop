# Architecture

One Go process serves embedded Svelte, APIs, cleanup and Immich workers. SQLite
uses explicit SQL and a CGO-free driver. Frontend/migrations are embedded; runtime
has no Node/Go compiler. Use one server per persistent `/data` directory.

```mermaid
flowchart LR
  Browser -->|control, session, finalize| Go[PhotoDrop]
  Browser -->|direct media PUT| S3[Private object storage]
  Browser -->|local media POST| Go
  Go --> DB[SQLite metadata]
  Go --> Local[/data/uploads]
  Go -->|HEAD and bounded signature read| S3
  Go -->|optional verification| Turnstile
  Go -->|optional API copies| Immich
  Export[Export CLI] -->|backend reads| Local
  Export -->|backend reads| S3
```

PhotoDrop authorizes/verifies/finalizes direct uploads and stores metadata; it
does not proxy their media bodies. Local uploads use bounded streams/private files.
Short database transactions do not span remote I/O. Assets retain immutable backend
identity; switches affect future uploads. Runtime credentials resolve old backends.
Uncertain direct attempts preserve IDs and finalize first; confirmed expiry/cleanup
can establish a new grant. Recovery is page-scoped, not resumable multipart upload.

One administrator uses hashed credentials, opaque persistent sessions and CSRF/origin
checks. Guests use event-scoped grants, optional Turnstile and unverified contributor
labels. Limits/quotas bound abuse; signature sniffing is not malware analysis.
Export streams ready assets to ordinary files/manifest. Immich jobs persist/recover
in SQLite; Immich remains independent with separate originals/backups. Credentials
stay in runtime configuration, never frontend bundles or backend identity records.

Native OIDC is optional administrator login only: authorization-code + PKCE with
lazy discovery and signed ID-token validation, then the same opaque local session
used by passwords. SQLite holds one-time browser-bound login transactions for five
minutes, never provider tokens. Subject/group allowlists authorize at login. The
provider is outside subsequent API/session checks, public pages, uploads and health.

Immich automatic import is an opt-in policy on each event/target binding. The
existing worker reconciles ready, never-selected assets at startup, periodically,
and after jobs. Bounded keyset pages visit 32 bindings and select up to 256 assets
per binding. An immediate SQLite transaction inserts selection rows and one job;
the partial unique active-job index is the final concurrency guard. Later uploads
remain discoverable without in-memory notifications, including after restart.
Failed selections require manual retry; failed provisioning is never automatically
retried. Upload HTTP handlers do not call or wait for Immich. See the
[operating semantics](export-immich.md#automatic-import-unreleased-v11).

## Typed upload reservations

Migration 012 adds optional event photo/video quotas and durable asset `media_class`. Local uploads sniff a bounded prefix before the transactional reservation; direct S3 reserves an untrusted class that final signature verification must match. Asset rows account for ready actual bytes and pending reserved bytes across all backends. Unknown historical reservations acquire typed capacity atomically on completion. See [quota architecture and compatibility](quotas.md).
