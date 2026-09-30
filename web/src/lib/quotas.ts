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
export type MediaClass = 'photo' | 'video';
export type QuotaModes = Record<MediaClass, 'automatic' | 'custom'>;
export const maxQuotaBytes = 1125899906842624;
const keys = Object.keys(quotaFields) as QuotaKey[];

export function quotaDraft(event: EventRecord | null): QuotaDraft {
  return Object.fromEntries(keys.map(key => [key, event?.[key] == null ? undefined : Number((event[key]! / quotaFields[key].unit).toFixed(9))])) as QuotaDraft;
}

function explicitPayload(draft: QuotaDraft, event: EventRecord | null): Record<QuotaKey, number | null> {
  const original = quotaDraft(event);
  return Object.fromEntries(keys.map(key => [key, event && draft[key] === original[key] ? event[key] : draft[key] == null ? null : quotaFields[key].unit === 1 ? draft[key] : Math.round(draft[key]! * quotaFields[key].unit)])) as Record<QuotaKey, number | null>;
}

export function quotaModes(event: EventRecord | null): QuotaModes {
  const modes: QuotaModes = { photo: 'automatic', video: 'automatic' };
  if (event) for (const kind of ['photo', 'video'] as const) {
    const count = event[`max_${kind}s`], size = event[`max_${kind}_file_bytes`], total = event[`max_${kind}_storage_bytes`];
    modes[kind] = count != null && size != null && total != null && BigInt(count) * BigInt(size) === BigInt(total) ? 'automatic' : 'custom';
  }
  return modes;
}

export function calculatedQuota(draft: QuotaDraft, event: EventRecord | null, kind: MediaClass): { bytes: number | null; error?: string } {
  const values = explicitPayload(draft, event);
  const count = values[`max_${kind}s`], size = values[`max_${kind}_file_bytes`];
  if (count == null || size == null) return { bytes: null };
  if (!Number.isSafeInteger(count) || count < 1 || count > 1000000 || !Number.isSafeInteger(size) || size < 1 || size > maxQuotaBytes) {
    return { bytes: null, error: 'Enter a valid maximum count and individual file size to calculate storage.' };
  }
  // Individual inputs fit Number, but their product need not. Check in integers
  // before conversion; never round or clamp an over-limit calculated quota.
  const total = BigInt(count) * BigInt(size);
  if (total > BigInt(maxQuotaBytes)) return { bytes: null, error: 'Calculated storage exceeds 1 PiB. Reduce the count or file size, or enter a custom total.' };
  return { bytes: Number(total) };
}

export class QuotaCalculationError extends Error {
  fields: Record<string, string>;
  constructor(fields: Record<string, string>) {
    super('Check the calculated storage limits');
    this.fields = fields;
  }
}

export function quotaPayload(draft: QuotaDraft, event: EventRecord | null, modes?: QuotaModes): Record<QuotaKey, number | null> {
  const values = explicitPayload(draft, event), errors: Record<string, string> = {};
  for (const kind of ['photo', 'video'] as const) if (modes?.[kind] === 'automatic') {
    const result = calculatedQuota(draft, event, kind), key = `max_${kind}_storage_bytes` as const;
    values[key] = result.bytes;
    if (result.error) errors[key] = result.error;
  }
  if (Object.keys(errors).length) throw new QuotaCalculationError(errors);
  return values;
}

export function editQuota(draft: QuotaDraft, modes: QuotaModes, key: QuotaKey, value: number | undefined): { draft: QuotaDraft; modes: QuotaModes } {
  const nextModes = { ...modes };
  for (const kind of ['photo', 'video'] as const) if (key === `max_${kind}_storage_bytes`) nextModes[kind] = 'custom';
  return { draft: { ...draft, [key]: value }, modes: nextModes };
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
