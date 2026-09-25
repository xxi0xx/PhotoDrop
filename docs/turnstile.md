# Optional Turnstile

Create a production widget for PhotoDrop's public hostname. Set the public
PHOTODROP_TURNSTILE_SITE_KEY, server-only PHOTODROP_TURNSTILE_SECRET_KEY and exact
PHOTODROP_BASE_URL; recreate the container. Both keys empty disables challenges;
partial configuration fails startup.

Siteverify must return success, action `photodrop_upload`, and the BASE_URL hostname.
Validation stays strict even if official test keys return dummy hostnames or omit
action. No production localhost bypass exists. Injected verifier/widget simulation
is only in Go test code.

Guests verify once per session batch, not for each file/finalization under a valid
grant. Provider errors fail new grants closed; valid grants and browsing continue.
Tokens are single-use and held in page memory. Ambiguous redemption needs a new
challenge. The server does not send guest IP to Siteverify; the widget still
connects the browser to Cloudflare. Review its privacy terms and your disclosures.

Test keys do not prove a production flow. The owner completed the real production-key
flow on 2026-09-24 (America/Chicago); see [live validation](live-validation.md)
and [security details](security.md). Never share tokens, response payloads or secrets.
