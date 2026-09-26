export type PhotoState = 'waiting' | 'preparing' | 'uploading' | 'verifying' | 'ready' | 'failed';
export function selectedForUpload<T extends { status: PhotoState; validationError?: string }>(items: T[], retry: boolean): T[] {
  return items.filter(item => item.status === (retry ? 'failed' : 'waiting') && !item.validationError);
}
export function photoProblem(file: { size: number; type: string; name: string }, max: number): string {
  if (!file.size) return 'This file is empty. Choose another file.';
  if (file.size > max) return 'This file is too large. Choose a smaller file.';
  const kinds = ['image/jpeg','image/png','image/webp','image/gif','image/heic','image/heif','video/mp4','video/quicktime'];
  if (file.type && file.type !== 'application/octet-stream' ? !kinds.includes(file.type.toLowerCase()) : !/\.(jpe?g|png|webp|gif|heic|heif|mp4|mov)$/i.test(file.name)) return 'Choose a JPEG, PNG, WebP, GIF, HEIC, HEIF, MP4 or MOV file.';
  return '';
}
export function photoStatus(status: PhotoState, sent: number, size: number): string {
  if (status === 'ready') return '✓ Uploaded';
  if (status === 'failed') return 'Failed';
  if (status === 'waiting') return 'Waiting';
  if (status === 'uploading' && sent < size) return `Uploading · ${Math.floor(sent / Math.max(1,size) * 100)}%`;
  return 'Processing…';
}
export function retryAfterAt(value: string | null, now = Date.now()): number {
  if (!value) return 0;
  const seconds = Number(value);
  const instant = Number.isFinite(seconds) ? now + Math.max(0, seconds) * 1000 : Date.parse(value);
  return Number.isFinite(instant) ? Math.max(now, instant) : 0;
}
export function guestError(error: unknown): string {
  const e = error as { code?: string; status?: number; message?: string } | null;
  if (e?.status === 429) return "You're uploading a little too quickly. Please wait a moment and try again.";
  const messages: Record<string,string> = {
    event_quota: 'This event has reached its file or storage limit. Please contact the host.',
    event_closed: 'This event is no longer accepting files. Your completed files are safe.',
    session_expired: 'Please try these files again. You may need to complete verification again.',
    session_quota: 'Please try the remaining files again. Your completed files are safe.',
    upload_session_not_found: 'Please try these files again. You may need to complete verification again.',
    asset_not_found: 'Please try this file again. Your completed files are safe.',
    verification_failed: 'Verification did not succeed. Please complete it again and retry.',
    verification_unavailable: 'Verification is temporarily unavailable. Please try again in a moment.',
    unsupported_image: 'This file is not supported media. Please choose another file.',
    file_too_large: 'This file is too large. Please choose a smaller file.',
  };
  if (e?.code && messages[e.code]) return messages[e.code];
  if (error instanceof TypeError) return 'Connection lost. Check your connection and try again.';
  return error instanceof Error ? error.message : 'We could not upload this file. Please try again.';
}
