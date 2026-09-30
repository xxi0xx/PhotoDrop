import test from 'node:test';
import assert from 'node:assert/strict';
import { quotaDraft, quotaPayload, quotaStatuses, quotaFields } from '../src/lib/quotas.ts';
import { photoProblem, mediaHint, directClass, guestError } from '../src/lib/upload-ux.ts';

test('blank quota fields remain unlimited; unchanged display retains exact bytes', () => {
  const blank = Object.fromEntries(Object.keys(quotaFields).map(key => [key, null]));
  assert.deepEqual(quotaPayload(quotaDraft(null), null), blank);
  const event = { ...blank, max_photo_file_bytes: 1234567, max_video_file_bytes: 87654321, max_photo_storage_bytes: 1125899906842619, max_video_storage_bytes: 9876543211, max_bytes: 1234567890123, max_photos: 5 };
  const draft = quotaDraft(event);
  assert.deepEqual(quotaPayload(draft, event), event);
  draft.max_photo_file_bytes = 2.5;
  draft.max_videos = 3;
  draft.max_photos = undefined;
  assert.deepEqual(quotaPayload(draft, event), { ...event, max_photos: null, max_videos: 3, max_photo_file_bytes: 2621440 });
});

test('typed preflight uses MIME hints, never filenames to choose a direct quota', () => {
  assert.match(photoProblem({ name: 'fake.mov', type: 'image/png', size: 11 }, 100, 10, 90), /photo.*too large/);
  assert.equal(photoProblem({ name: 'fake.png', type: 'video/mp4', size: 11 }, 100, 10, 90), '');
  assert.match(photoProblem({ name: 'a.mov', type: 'video/quicktime', size: 91 }, 100, 10, 90), /video.*too large/);
  assert.match(photoProblem({ name: 'a.mov', type: 'video/quicktime', size: 101 }, 100, 200, 200), /too large/);
  assert.equal(photoProblem({ name: 'a.mov', type: '', size: 80 }, 100, 10, 20), ''); // Local signature decides.
  for (const type of ['', 'application/octet-stream', 'video/webm']) {
    assert.equal(mediaHint(type), undefined);
    assert.throws(() => directClass(type), /browser/i);
  }
  assert.equal(directClass('image/heic'), 'photo');
  assert.equal(directClass('video/mp4'), 'video');
});

test('typed and overall statuses include reservations without conflating buckets', () => {
  const event = { ...quotaPayload(quotaDraft(null), null), max_photos: 2, max_video_storage_bytes: 100, max_assets: 4,
    media: { ready_count: 2, photo_count: 2, storage_bytes: 50, pending_count: 2, reserved_bytes: 150,
      photos: { ready_count: 1, bytes: 25, pending_count: 1, reserved_bytes: 75 },
      videos: { ready_count: 1, bytes: 25, pending_count: 1, reserved_bytes: 75 } } };
  assert.deepEqual(quotaStatuses(event), ['Photo limit reached', 'Video storage limit reached', 'Overall file limit reached']);
  for (const code of ['photo_count', 'video_count', 'photo_storage', 'video_storage', 'photo_file_too_large', 'video_file_too_large', 'event_file_count', 'event_storage']) assert.notEqual(guestError({ code }), guestError({ code: 'unknown' }));
});
