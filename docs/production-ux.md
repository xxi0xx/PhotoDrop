# Guests, sharing, and event management

## Share an event

Open an event in administration and use **Share event** to copy its public link
or download its QR code. Both point to the exact same public event URL. The QR
is generated in the browser using the bundled QR encoder; the event URL is not
sent to a QR service. The PNG is 1024 × 1024 with a four-module white quiet zone
and medium error correction. Keep that white border when printing or placing it
on signage, and scan a sample at the intended printed size before distribution.

The QR contains only the public link: no administrator credentials, upload
secrets, verification tokens, or contributor names. Treat it like sharing the
link itself. Disabling or expiring an event closes photo sharing without changing
its public ID, link, or QR. Re-enabling the same event reuses that link. Changing
the deployment's public base URL requires sharing a newly generated QR.

## Guest uploads and names

Guests need no account. **Your name** is optional and helps the host recognize
who supplied photos. It is an unverified label, not authenticated identity or
public profile information. The host sees attribution in the protected event
summary; other guests cannot browse contributor names or photos.

Names accept Unicode and ordinary accents. Surrounding whitespace is trimmed;
blank input is anonymous. The server rejects internal control characters and
names longer than 100 Unicode characters. HTML-looking names remain plain text.
The name is kept only in the current page's memory, where it can be edited or
cleared for another batch. PhotoDrop does not store the name, upload grants, or
verification tokens in localStorage/sessionStorage.

Attribution is fixed when a batch creates its upload session and copied into
each asset record. Changing the name for a later batch never rewrites earlier
photos. Cleanup of expired sessions does not erase asset attribution. Existing
photos from before Gate 7 remain anonymous. The admin summary groups completed
photos by their supplied name and shows at most 100 groups; equal names do not
prove the same person. Technical logs do not include contributor names.

## Selection, progress, and recovery

Choose up to 100 photos per selection. The page shows filenames, total size,
unsupported/oversize files, overall progress, and individual photo status.
The server still enforces file validation, session limits, event availability,
and quotas. The supported formats and configured per-photo limit appear beside
the chooser. No thumbnails or gallery are generated.

Keep the page open while sending photos. A supported browser warns before
leaving during active uploads. Success moves to **Thanks for sharing**, without
leaving progress bars at 100%. **Add more photos** starts another selection and
keeps the entered name available for editing.

If some files fail, the successful ones stay complete. **Retry failed** operates
only on failed photos. Adding a new selection during partial success also keeps
the existing completed and uncertain attempts. A temporary rate limit disables
upload/retry until `Retry-After` permits another attempt; there is no retry loop.
Quota messages direct guests to the host rather than exposing storage details.

For direct S3 uploads, an uncertain PUT or finalization retains its asset and
session IDs and attempts finalization first. Only authoritative expiration,
exhaustion, or confirmed cleanup permits a fresh grant. When a new grant needs
verification, complete it and retry; the batch retains its original name.
Verification covers the batch, not every file. Media bytes still travel directly
from the browser to object storage. Reloading/closing the page loses its
in-memory selection/recovery state; this is not a resumable-upload feature.

## Administration

The event page separates **Event**, **Share event**, **Guest uploads**,
**Immich**, **Export**, and **Danger zone**. Guest uploads shows completed counts,
storage, pending reservations when present, optional quotas, and attribution.
Refresh photo counts updates those summaries without discarding unsaved edits.

Immich keeps the existing connection, new-photo import, failed retry, cancellation,
and recovery operations. Export remains CLI-first with a copyable command;
choose a new writable output directory each time. See the existing
[export/Immich guide](export-immich.md). Deleting a PhotoDrop event does not delete
independent Immich copies. There is no public gallery or browser ZIP export.

Migration `009_contributors.sql` adds nullable names to upload sessions/assets.
Migrations 001–008 are unchanged. Normal deployment still uses one PhotoDrop
container plus `/data`, with optional external storage/Immich configured as before.
