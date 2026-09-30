# Event photo and video limits

In **Manage event → Guest uploads**, set Photo limits and Video limits independently.
Expand **Overall limits** for optional ceilings shared by both types. Blank means
unlimited at that event scope, not unlimited server or upload-session capacity.
The editor displays individual sizes in MiB and storage in GiB, stores integer
bytes, and preserves exact stored bytes when a displayed value is unchanged.

## Calculated or custom storage

New events start each type in automatic mode: maximum count × individual file
size gives its total storage. File size is converted from binary MiB to integer
bytes before multiplication; the GiB display is never used as the formula input.
For example, 500 photos × 25 MiB gives 13,107,200,000 bytes, while 20 videos ×
500 MiB gives 10,485,760,000 bytes. The server ceiling must permit the individual
size (the default is 50 MiB). An invalid or above-1-PiB result cannot be saved.

Changing either prerequisite recalculates only that type. Clearing a prerequisite
clears its automatic total; filling both again resumes calculation. Editing or
clearing the total switches to custom mode. A custom blank is intentionally
unlimited and stays blank even when count/file size changes. Select **Use calculated
maximum** to explicitly restore the link. A custom total above the theoretical
product is valid and is not rewritten. Overall `max_bytes` is always independent.

Existing events initialize linked only when both prerequisites exist and the stored
total exactly equals their integer product. Any other stored total, including NULL,
starts custom. Unrelated edits preserve exact bytes and never impose a quota on an
unlimited event. The mode is editor-only, reconstructed when an event is opened;
the API still stores the same explicit byte fields. No new migration or backend
policy is needed. Server reservations enforce saved limits regardless of UI mode.

| Event API field | Meaning | Accepted value |
| --- | --- | --- |
| `max_photos` | Ready and pending photos | null or integer 1–1,000,000 |
| `max_videos` | Ready and pending videos | null or integer 1–1,000,000 |
| `max_photo_file_bytes` | Individual photo size | null or integer 1–1 PiB in bytes |
| `max_video_file_bytes` | Individual video size | null or integer 1–1 PiB in bytes |
| `max_photo_storage_bytes` | Photo actual and reserved storage | null or integer 1–1 PiB in bytes |
| `max_video_storage_bytes` | Video actual and reserved storage | null or integer 1–1 PiB in bytes |
| `max_assets` | Overall ready and pending files | null or integer 1–1,000,000 |
| `max_bytes` | Overall actual and reserved storage | null or integer 1–1 PiB in bytes |

These are SQLite event settings, not environment variables. POST/PUT event bodies
accept these nullable fields; PUT replaces event policy, so include limits that
must remain configured. Fractional and out-of-range values receive field errors.
The API rejects an individual event file limit above the current server maximum.

`PHOTODROP_MAX_FILE_SIZE` remains the absolute infrastructure ceiling. The effective
individual limit is the smaller of that value and the corresponding configured
event file limit. Reducing the server ceiling also reduces the effective limit
for future admissions. Temporary upload-session `max_assets` and `max_bytes` stay
generic anti-abuse bounds across both types, captured from
`PHOTODROP_UPLOAD_SESSION_MAX_ASSETS` and `PHOTODROP_UPLOAD_SESSION_MAX_BYTES` when
the grant is created. They are not persistent event policy; guests may obtain
multiple grants. Set event limits to bound the entire event.

The first applicable limit reached wins. For example, `max_assets=100`,
`max_photos=90`, `max_videos=20` allows at most 100 files altogether. Video bytes
never consume photo storage, and photo bytes never consume video storage, but
both consume overall and session storage. Counts/bytes include historical
backends, regardless of the currently active provider.

## Reservations and verification

The asset row is the durable reservation: pending assets count one slot and
`expected_size_bytes`; ready assets count one slot and actual `size_bytes`.
Quota checking and pending-row insertion use one IMMEDIATE SQLite write
transaction. No in-memory counters or separate quota ledger are involved.
Local uploads inspect at most 512 signature bytes before reserving the actual
class, then stream the rest without holding a database transaction. Unknown-length
local requests conservatively reserve the effective individual maximum; successful
completion replaces that reservation with actual bytes. Final EOF-aware signature
validation still rejects malformed objects ending at exactly byte 512.

Direct S3 preparation accepts `media_class: "photo" | "video"`. The browser sends
this hint from supported MIME metadata. It is untrusted and must agree with a
supported declared MIME. Older clients may omit it when their supported MIME
unambiguously identifies the class. Missing/generic MIME requires an explicit
class; filenames never choose the quota bucket. A browser with missing/generic
`File.type` can use local server sniffing, but direct upload reports an actionable
unknown-type error. Try another file or browser that provides supported MIME metadata.

Before S3 authorization, preparation checks the effective file size and reserves
the declared class. Completion verifies HEAD, size, object identity, bounded
signature and actual class. A mismatch rejects/deletes the invalid remote object
using existing cleanup behavior while retaining the immutable pending attempt;
it never transfers capacity between classes. Retry with valid bytes for that
reservation, or make a correctly classified new attempt. Stale cleanup releases
the old reservation only after safe removal. Uncertain network/finalization
failures retain identity and use finalize-first recovery.

Lowering limits never deletes media, Immich copies, or valid typed reservations.
An admitted typed upload can finish against its original reservation. New
reservations must fit current limits. Raising/clearing a limit permits new work.
Cleanup and deletion release capacity by deleting asset rows. Importing an
accepted file into Immich consumes no additional PhotoDrop quota.

## Upgrade and API compatibility

Migration 012 preserves existing overall limits and defaults all six new fields
to NULL. Supported ready MIME values backfill `media_class`; pending rows use
supported `expected_mime_type`. Ambiguous pending rows remain NULL, with no
filename guesses. They still consume overall/session capacity. When verified,
an unknown legacy row must satisfy the **current** typed count, storage and
individual-file limits; assignment and readiness are committed atomically.
It is not counted twice against overall/session limits. It remains pending when
typed capacity is unavailable, so the host can raise a limit or await cleanup.

Admin media statistics add `photos` and `videos`, each containing `ready_count`,
`bytes`, `pending_count`, `reserved_bytes`. New overall `ready_count` has the same
meaning as the retained legacy `photo_count`: **all ready files**, including videos.
Existing `storage_bytes`, `pending_count`, `reserved_bytes` totals retain their
meaning, including unknown legacy reservations. The UI shows typed and overall
limit-reached notices without relabeling video as photos.

Public event metadata keeps `max_file_size` and adds effective
`max_photo_file_size` and `max_video_file_size`. It exposes no private usage
statistics. Browser preflight is an early hint; the server remains authoritative.
Safe errors distinguish `event_file_count`, `event_storage`, `photo_count`,
`video_count`, `photo_storage`, `video_storage` (409), and
`photo_file_too_large`, `video_file_too_large` (413). Existing generic event,
session and global-file error families remain available as fallbacks.
