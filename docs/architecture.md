# Architecture

One Go process serves embedded Svelte, APIs, cleanup and Immich workers. SQLite
uses explicit SQL and a CGO-free driver. Frontend/migrations are embedded; runtime
has no Node/Go compiler. Use one server per persistent `/data` directory.

```mermaid
flowchart LR
  Browser -->|control, session, finalize| Go[PhotoDrop]
  Browser -->|direct image PUT| S3[Private object storage]
  Browser -->|local image POST| Go
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
