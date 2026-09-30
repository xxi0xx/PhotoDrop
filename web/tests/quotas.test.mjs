import test from 'node:test';
import assert from 'node:assert/strict';
import { quotaDraft, quotaPayload, quotaStatuses, quotaFields, quotaModes, editQuota, calculatedQuota, QuotaCalculationError, maxQuotaBytes } from '../src/lib/quotas.ts';
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

function editor(event = null) {
  let state = { draft: quotaDraft(event), modes: quotaModes(event) };
  return {
    get modes() { return state.modes; },
    change(key, value) { state = editQuota(state.draft, state.modes, key, value); },
    link(kind) { state.modes = { ...state.modes, [kind]: 'automatic' }; },
    payload() { return quotaPayload(state.draft, event, state.modes); },
    calculation(kind) { return calculatedQuota(state.draft, event, kind); },
  };
}

test('new-event automatic totals follow exact inputs independently and clear/resume with missing inputs', () => {
  const e = editor();
  assert.deepEqual(e.modes, { photo: 'automatic', video: 'automatic' });
  assert.equal(e.payload().max_photo_storage_bytes, null);
  e.change('max_photos', 500);
  assert.equal(e.payload().max_photo_storage_bytes, null);
  e.change('max_video_file_bytes', 500);
  assert.equal(e.payload().max_video_storage_bytes, null);
  e.change('max_photo_file_bytes', 25);
  e.change('max_videos', 20);
  assert.equal(e.payload().max_photo_storage_bytes, 13107200000);
  assert.equal(e.payload().max_video_storage_bytes, 10485760000);
  e.change('max_photos', 1000);
  assert.equal(e.payload().max_photo_storage_bytes, 26214400000);
  e.change('max_photo_file_bytes', 12.5);
  assert.equal(e.payload().max_photo_storage_bytes, 13107200000);
  for (const [key, value] of [['max_photos', 1000], ['max_photo_file_bytes', 12.5]]) {
    e.change(key, undefined);
    assert.equal(e.payload().max_photo_storage_bytes, null);
    assert.equal(e.modes.photo, 'automatic');
    e.change(key, value);
    assert.equal(e.payload().max_photo_storage_bytes, 13107200000);
  }
  assert.equal(e.payload().max_video_storage_bytes, 10485760000);
  assert.equal(e.payload().max_bytes, null);
  assert.deepEqual(Object.keys(e.payload()).sort(), Object.keys(quotaFields).sort());
});

test('manual override and intentional unlimited stay custom until explicit relink', () => {
  const e = editor();
  e.change('max_photos', 500); e.change('max_photo_file_bytes', 25);
  e.change('max_photo_storage_bytes', 5); e.change('max_bytes', 100);
  e.change('max_photos', 600); e.change('max_photo_file_bytes', 30);
  assert.equal(e.modes.photo, 'custom');
  assert.equal(e.payload().max_photo_storage_bytes, 5 * 1073741824);
  e.change('max_photo_storage_bytes', undefined);
  e.change('max_photos', 700);
  assert.equal(e.payload().max_photo_storage_bytes, null);
  e.link('photo');
  assert.equal(e.payload().max_photo_storage_bytes, 700 * 30 * 1048576);
  assert.equal(e.payload().max_bytes, 100 * 1073741824);
  e.change('max_photo_storage_bytes', 1000); // Redundant, but valid explicit policy.
  assert.equal(e.payload().max_photo_storage_bytes, 1000 * 1073741824);
});

test('existing NULL/custom/equal totals initialize conservatively and round-trip exact bytes', () => {
  const empty = quotaPayload(quotaDraft(null), null);
  for (const total of [null, 1234567890123, 1234567 * 500]) {
    const event = { ...empty, max_photos: 500, max_photo_file_bytes: 1234567, max_photo_storage_bytes: total, max_bytes: 9876543210123 };
    const e = editor(event);
    assert.equal(e.modes.photo, total === 1234567 * 500 ? 'automatic' : 'custom');
    assert.equal(e.modes.video, 'custom');
    assert.deepEqual(e.payload(), event); // Unrelated fields do not participate.
    e.change('max_photos', 501);
    assert.equal(e.payload().max_photo_storage_bytes, e.modes.photo === 'automatic' ? 1234567 * 501 : total);
  }
  const event = { ...empty, max_photos: 2, max_photo_file_bytes: 1049, max_photo_storage_bytes: 2000 };
  const e = editor(event);
  e.change('max_photo_storage_bytes', 2098 / 1073741824);
  assert.equal(e.modes.photo, 'custom'); // Equality is not an implicit opt-in.
  e.change('max_photos', 3);
  assert.equal(e.payload().max_photo_storage_bytes, 2098);
  e.link('photo');
  assert.equal(e.payload().max_photo_storage_bytes, 3147); // Uses untouched 1049 B, not display text.
});

test('automatic integer products are exact at the quota boundary and reject overflow without clamping', () => {
  const empty = quotaPayload(quotaDraft(null), null);
  const e = editor({ ...empty, max_photos: 1000000, max_photo_file_bytes: Math.floor(maxQuotaBytes / 1000000), max_photo_storage_bytes: null });
  e.link('photo');
  assert.equal(BigInt(e.payload().max_photo_storage_bytes), 1000000n * BigInt(Math.floor(maxQuotaBytes / 1000000)));
  e.change('max_photo_file_bytes', 1024); // Maximum accepted server ceiling, in MiB.
  assert.equal(e.payload().max_photo_storage_bytes, 1073741824000000);
  e.change('max_photos', 1); e.change('max_photo_file_bytes', maxQuotaBytes / 1048576);
  assert.equal(e.payload().max_photo_storage_bytes, maxQuotaBytes);
  e.change('max_photos', 1000000);
  assert.match(e.calculation('photo').error, /exceeds 1 PiB/);
  assert.throws(() => e.payload(), QuotaCalculationError);
  e.change('max_photo_storage_bytes', 10); // A custom cap can resolve the excessive theoretical product.
  assert.equal(e.payload().max_photo_storage_bytes, 10 * 1073741824);
  e.link('photo'); e.change('max_photos', 1.5);
  assert.match(e.calculation('photo').error, /valid maximum count/);
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
