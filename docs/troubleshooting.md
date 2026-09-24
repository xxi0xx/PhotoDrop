# Troubleshooting

Start with safe diagnostics:

```sh
docker compose ps
docker compose config --quiet
docker compose logs --tail=100 photodrop
docker compose exec --user 10001 photodrop photodrop version
curl --fail http://localhost:8080/healthz
```

Read logs privately first. Before sharing, redact credentials, presigned URLs,
cookies, tokens, administrator password, Turnstile secret, Immich API keys, guest
links/personal data and host paths as appropriate. Do not post full `docker inspect`,
expanded Compose configuration or environment dumps: they can contain secrets.

| Symptom | Check / recovery |
| --- | --- |
| Unhealthy container | Startup logs, required password, storage settings, writable data mount, port conflicts; health is not a remote provider check |
| `/data` permission error | UID/GID 10001 access, parent mount, existing file ownership; entrypoint prepares only top-level directory; do not chmod 777 |
| Sign-in failure | Password bytes/current environment, correct public origin, HTTPS Secure cookie, clock/session expiry; password rotation revokes sessions |
| Wrong guest links | BASE_URL must be the exact public origin, no subpath; recreate app after changing environment |
| CSRF/origin rejection | Use the configured origin, preserve Origin/Referer through proxy; admin needs session and CSRF token; never disable checks |
| Everyone shares rate limit | Check actual socket peer and controlled XFF chain; trust only real proxy CIDRs, not all networks |
| Closed event | Enable state/expiration/quota; expiration never deletes originals; public link remains stable |
| Quota reached | Ready plus pending reservations count; review limits and stale cleanup, which can wait on old storage credentials |
| S3 authorization failure | Scoped permissions, correct region/endpoint/path style, expired credentials, clock and conditional PUT support |
| Browser S3 PUT fails | Exact CORS origin, PUT method, Content-Type and If-None-Match headers, HTTPS, expiry; inspect without sharing signed URL |
| Historical backend unavailable | Restore matching named credentials/configuration; never repoint an existing backend key |
| Turnstile failure | Both keys, allowed public hostname, BASE_URL, fixed action, outbound HTTPS; test-key dummy hostname is not a production bypass |
| Immich connection/import error | Tested v3.2.1 API, reachable single target URL, same account, four permissions in the integration guide; retry only failed work |
| Export error | New output directory with writable parent and hard-link support, correct UID, old backend credentials, existing source objects |
| Migration error | Stop and preserve state/logs; verify target image and original migration checksums, permissions/disk space; restore backup for rollback, never edit history |

Do not delete pending rows or change storage IDs to clear an error. For ambiguous
uploads keep the page open and use failed-only retry so finalize-first recovery can
preserve identity. See [security/recovery](security.md), [storage](storage.md),
[Immich/export](export-immich.md) and [backup/restore](backup-restore.md).
