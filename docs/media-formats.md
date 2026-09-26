# Media formats (unreleased v1.1 work)

The development branch accepts JPEG, PNG, WebP, GIF, HEIC, HEIF, MP4
(`video/mp4`) and QuickTime MOV (`video/quicktime`). v1.0.0 remains image-only.
Other containers, including WebM, AVI, 3GP and audio-only M4A, are unsupported.
AVIF remains unsupported. Legacy MOV without a leading `ftyp` is unsupported.

Local and direct S3 finalization use the same signature classifier. Filenames
and declared MIME types do not establish the final stored MIME type. An empty
or `application/octet-stream` declaration is allowed; the bytes must still pass
validation. The public `unsupported_image` error code remains for client
compatibility, but its message describes media. `photo_count` and the export
manifest's `photos/` directory also remain unchanged and include video.

The classifier reads at most 512 bytes. It checks complete, aligned `ftyp` box
bounds before examining brands. AVIF is rejected before considering generic
MP4 compatibility; HEIC/HEIF are identified before video. QuickTime uses `qt  `;
MP4 uses a conservative major-brand allowlist (`isom`, `iso2`–`iso9`, `mp41`,
`mp42`, `avc1`, `M4V `, `MSNV`). Unknown, audio and 3GP major brands do not qualify
merely by listing `isom`. Video must also have a following box header; visibly
invalid/truncated box headers fail. Oversized, extended-size or incomplete
`ftyp` boxes outside this bounded profile are rejected.

Brand references: [MP4 Registration Authority](https://mp4ra.org/registered-types/brands)
and [Apple's file type compatibility atom](https://developer.apple.com/documentation/quicktime-file-format/file_type_compatibility_atom).
This is bounded signature validation, not a full container/codec integrity check
or antivirus scan. Corruption outside the inspected prefix may only be detected
by the consuming application. PhotoDrop does not decode, transcode, extract
metadata, generate thumbnails, or offer playback. Immich processes imported media.

`PHOTODROP_MAX_FILE_SIZE` applies equally to images and videos. Event and session
asset/byte quotas count both. There are no new settings or migrations. Existing
v1.0 data and historical backend records work unchanged. Local transfers pass
through PhotoDrop and any reverse proxy's body/time limits; direct S3 sends media
bytes directly to object storage. Finalization keeps exact-size, MIME metadata,
ETag and bounded Range checks. Failed transfers retain the existing retry and
finalize-first recovery behavior. See [storage](storage.md) and
[reverse proxy guidance](reverse-proxy.md).

Export and Immich import preserve the original filenames, sizes and bytes.
Tests share two tiny original H.264 fixtures and check MP4/MOV original-byte
hashes after export and real Immich import. The existing multipart client needs
no additional MIME parameter: the tested Immich server recognizes the files.
