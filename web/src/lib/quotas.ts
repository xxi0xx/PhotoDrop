import type { EventRecord } from './api';

export const quotaFields = {
  max_photos: { label: 'Maximum photos', unit: 1 },
  max_photo_file_bytes: { label: 'Maximum photo size (MiB)', unit: 1048576 },
  max_photo_storage_bytes: { label: 'Maximum photo storage (GiB)', unit: 1073741824 },
  max_videos: { label: 'Maximum videos', unit: 1 },
  max_video_file_bytes: { label: 'Maximum video size (MiB)', unit: 1048576 },
  max_video_storage_bytes: { label: 'Maximum video storage (GiB)', unit: 1073741824 },
  max_assets: { label: 'Maximum files overall', unit: 1 },
  max_bytes: { label: 'Maximum storage overall (GiB)', unit: 1073741824 },
};
export type QuotaKey = keyof typeof quotaFields;
export type QuotaDraft = Record<QuotaKey, number | undefined>;
const keys = Object.keys(quotaFields) as QuotaKey[];

export function quotaDraft(event: EventRecord | null): QuotaDraft {
  return Object.fromEntries(keys.map(key => [key, event?.[key] == null ? undefined : Number((event[key]! / quotaFields[key].unit).toFixed(9))])) as QuotaDraft;
}

export function quotaPayload(draft: QuotaDraft, event: EventRecord | null): Record<QuotaKey, number | null> {
  const original = quotaDraft(event);
  return Object.fromEntries(keys.map(key => [key, event && draft[key] === original[key] ? event[key] : draft[key] == null ? null : Math.round(draft[key]! * quotaFields[key].unit)])) as Record<QuotaKey, number | null>;
}

export function quotaStatuses(event: EventRecord): string[] {
  const reached: string[] = [];
  for (const kind of ['photo', 'video'] as const) {
    const usage = event.media[`${kind}s`];
    const count = event[`max_${kind}s`], bytes = event[`max_${kind}_storage_bytes`];
    const label = kind === 'photo' ? 'Photo' : 'Video';
    if (count != null && usage.ready_count + usage.pending_count >= count) reached.push(`${label} limit reached`);
    if (bytes != null && usage.bytes + usage.reserved_bytes >= bytes) reached.push(`${label} storage limit reached`);
  }
  if (event.max_assets != null && event.media.ready_count + (event.media.pending_count ?? 0) >= event.max_assets) reached.push('Overall file limit reached');
  if (event.max_bytes != null && event.media.storage_bytes + (event.media.reserved_bytes ?? 0) >= event.max_bytes) reached.push('Overall storage limit reached');
  return reached;
}
