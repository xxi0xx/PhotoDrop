export type Session = { csrf_token: string; expires_at: string };
export type EventRecord = {
  id: number; public_id: string; name: string; description: string;
  event_date: string | null; enabled: boolean; expires_at: string | null;
  created_at: string; updated_at: string; status: 'open' | 'disabled' | 'expired'; public_url: string;
  deleting: boolean; media: { photo_count: number; storage_bytes: number };
};
export type GuestEvent = { name: string; status: 'open' | 'closed'; description?: string; event_date?: string; max_file_size?: number };
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}
export class APIError extends Error {
  constructor(message: string, public status: number, public fields: Record<string, string> = {}) { super(message); }
}
// Only the CSRF token is held in memory. Authentication uses the HttpOnly cookie.
let csrfToken = '';
export async function request<T>(path: string, method = 'GET', data?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (data !== undefined) headers['Content-Type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken;
  const response = await fetch(path, { method, headers, credentials: 'same-origin', body: data === undefined ? undefined : JSON.stringify(data) });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    if (response.status === 401 && window.location.pathname.startsWith('/admin') && window.location.pathname !== '/admin/login') window.location.assign('/admin/login');
    throw new APIError(body?.error?.message ?? 'Unable to complete the request. Please try again.', response.status, body?.error?.fields);
  }
  return response.status === 204 ? undefined as T : response.json();
}
export async function loadSession(): Promise<void> { csrfToken = (await request<Session>('/api/admin/session')).csrf_token; }
export function message(error: unknown): string { return error instanceof Error ? error.message : 'Something went wrong. Please try again.'; }
export function publicURL(event: EventRecord): string { return new URL(event.public_url, window.location.origin).href; }
export function displayDate(value?: string | null): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'long' }).format(new Date(`${value}T12:00:00`)) : 'No event date'; }
export function localDateTime(iso: string | null): string {
  if (!iso) return '';
  const date = new Date(iso); const pad = (n: number) => String(n).padStart(2, '0');
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}
