# Security policy

Until v1.0.0 is published, fixes target the latest `main`. After publication,
the latest stable v1 release is supported; older patch releases and prereleases
are not separately maintained. Upgrade to the latest supported release. No
multi-year support commitment is made. Read
[deployment controls and limitations](docs/security.md) before Internet exposure.

## Reporting

Do not post exploit details, secrets, or guest media in a public issue. GitHub
private vulnerability reporting is currently disabled for this repository, and
no separate private reporting address has been published (checked 2026-09-23).
**Owner action required before public v1 release:** enable GitHub private
vulnerability reporting and verify the private reporting form is usable, or
publish a deliberately selected private contact route. Do not send a report to
an invented address. Once enabled, use GitHub's Security tab / Report a vulnerability
for this repository. Until then, request a private contact channel without posting
vulnerability details. Once a private
channel is established, send affected commit/version, configuration with secrets
removed, reproduction steps, expected/actual behavior, and estimated impact.
Include deployment/storage mode, relevant reverse proxy, a minimal synthetic
reproduction and any suggested mitigation. Use synthetic media and redact passwords,
tokens, cookies, presigned URLs and personal data. Do not post exploit details or
affected guest links publicly before coordination. Maintainers will acknowledge
and discuss reproduction, impact and coordinated disclosure when available; no
fixed response-time guarantee or bug-bounty program is offered.
