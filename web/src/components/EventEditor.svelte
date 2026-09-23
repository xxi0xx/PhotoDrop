<script lang="ts">
	import Immich from './Immich.svelte';
  import ShareEvent from './ShareEvent.svelte';
  import { onMount, tick } from 'svelte';
  import { request, message, publicURL, localDateTime, formatBytes, APIError, type EventRecord } from '../lib/api';
  let { id }: { id: string | null } = $props();
  let event = $state<EventRecord | null>(null);
  let name = $state(''); let description = $state(''); let eventDate = $state(''); let enabled = $state(true); let expiration = $state('');
  let maxPhotos = $state<number | undefined>(); let maxStorageGiB = $state<number | undefined>();
  let loadedStorageGiB: number | undefined;
  let loading = $state(true); let loadError = $state(''); let busy = $state(false); let error = $state(''); let notice = $state('');
  let fields = $state<Record<string, string>>({}); let confirming = $state(false); let confirmButton = $state<HTMLButtonElement>(); let deleteButton = $state<HTMLButtonElement>();
  async function keepEvent() { confirming = false; await tick(); deleteButton?.focus(); }
  function apply(saved: EventRecord) { event = saved; name = saved.name; description = saved.description; eventDate = saved.event_date ?? ''; enabled = saved.enabled; expiration = localDateTime(saved.expires_at); maxPhotos = saved.max_assets ?? undefined; loadedStorageGiB = saved.max_bytes == null ? undefined : Number((saved.max_bytes / 1073741824).toFixed(9)); maxStorageGiB = loadedStorageGiB; }
  async function load() {
    if (!id) { loading = false; return; }
    loading = true; loadError = '';
    try { apply((await request<{ event: EventRecord }>(`/api/admin/events/${id}`)).event); }
    catch (cause) { loadError = message(cause); } finally { loading = false; }
  }
  onMount(() => { void load(); });
  async function save(submit: SubmitEvent) {
    submit.preventDefault(); busy = true; error = ''; notice = ''; fields = {};
    try {
      // Keep exact persisted bytes when the rounded display has not been edited.
      const maxBytes = maxStorageGiB === loadedStorageGiB && event ? event.max_bytes : maxStorageGiB == null ? null : Math.round(maxStorageGiB * 1073741824);
      const data = { name, description, event_date: eventDate || null, enabled, expires_at: expiration ? new Date(expiration).toISOString() : null, max_assets: maxPhotos ?? null, max_bytes: maxBytes };
      const result = await request<{ event: EventRecord }>(id ? `/api/admin/events/${id}` : '/api/admin/events', id ? 'PUT' : 'POST', data);
      if (!id) { window.location.assign(`/admin/events/${result.event.id}`); return; }
      apply(result.event); notice = 'Event saved.';
    } catch (cause) { error = message(cause); if (cause instanceof APIError) fields = cause.fields; }
    finally { busy = false; }
  }
  async function copyExport() {
    if (!event) return;
    try { await navigator.clipboard.writeText(`photodrop export --event ${event.id} --output /export/event-${event.id}`); notice = 'Export command copied.'; }
    catch { notice = 'Select and copy the export command below.'; }
  }
  let refreshingStats = $state(false);
  async function refreshStats() {
    if (!event) return;
    refreshingStats = true;
    try { const latest = (await request<{ event: EventRecord }>(`/api/admin/events/${id}`)).event; event = { ...event, media: latest.media, contributors: latest.contributors }; }
    catch (cause) { error = message(cause); }
    finally { refreshingStats = false; }
  }
  async function confirmDelete() { confirming = true; await tick(); confirmButton?.focus(); }
  async function remove() {
    if (!id) return; busy = true; error = '';
    try { await request(`/api/admin/events/${id}`, 'DELETE'); window.location.assign('/admin'); }
    catch (cause) { const detail = message(cause); await load(); error = detail; busy = false; }
  }
</script>
<svelte:head><title>{id ? 'Manage event' : 'New event'} · PhotoDrop Admin</title></svelte:head>
<main class="workspace editor">
  <a class="back-link" href="/admin">← All events</a>
  <div class="page-heading"><h1>{id ? 'Manage event' : 'New event'}</h1>{#if event}<span class:open={event.status === 'open'} class="badge">{event.status === 'open' ? 'Open' : event.status === 'disabled' ? 'Disabled' : 'Expired'}</span>{/if}</div>
  {#if loading}<p role="status">Loading event…</p>
  {:else if loadError}<p class="error" role="alert">{loadError}</p><button class="secondary" onclick={load}>Try again</button>
  {:else}
    {#if notice}<p class="notice" role="status">{notice}</p>{/if}{#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if event?.deleting}<p class="error" role="alert">This event is closed while media cleanup is pending. Retry deletion below.</p>{/if}
    <form onsubmit={save} class="event-form"><section class="section-card" aria-labelledby="event-heading"><h2 id="event-heading">Event</h2><p class="section-intro">The details your guests will see.</p><fieldset disabled={busy || event?.deleting}>
      <div class="field"><label for="event-name">Name <span class="muted">(required)</span></label><input id="event-name" name="name" required maxlength="200" bind:value={name} aria-invalid={!!fields.name} aria-describedby={fields.name ? 'name-error' : undefined} />{#if fields.name}<p id="name-error" class="error">{fields.name}</p>{/if}</div>
      <div class="field"><label for="description">Description</label><textarea id="description" name="description" maxlength="4000" rows="4" bind:value={description} aria-invalid={!!fields.description} aria-describedby={fields.description ? 'description-error' : undefined}></textarea>{#if fields.description}<p id="description-error" class="error">{fields.description}</p>{/if}<p class="hint">Optional guest-facing text. Up to 4,000 characters.</p></div>
      <div class="field"><label for="event-date">Event date</label><input id="event-date" name="event_date" type="date" bind:value={eventDate} aria-invalid={!!fields.event_date} aria-describedby={fields.event_date ? 'date-error' : undefined} />{#if fields.event_date}<p id="date-error" class="error">{fields.event_date}</p>{/if}<p class="hint">Optional calendar date for the event.</p></div>
      <div class="field"><label class="checkbox" for="enabled"><input id="enabled" name="enabled" type="checkbox" bind:checked={enabled} />Enabled</label><p class="hint">Guests can view this event while it is enabled and has not expired.</p>{#if fields.enabled}<p class="error">{fields.enabled}</p>{/if}</div>
      <div class="field"><label for="expiration">Expiration <span class="muted">(your local time)</span></label><input id="expiration" name="expires_at" type="datetime-local" step="1" bind:value={expiration} aria-invalid={!!fields.expires_at} aria-describedby={fields.expires_at ? 'expiry-error' : undefined} />{#if fields.expires_at}<p id="expiry-error" class="error">{fields.expires_at}</p>{/if}<p class="hint">Leave empty for no expiration. Expiration closes the guest page; it does not delete the event.</p></div>
    </fieldset></section>
    {#if event}<ShareEvent url={publicURL(event)} publicID={event.public_id} />{/if}
    <section class="section-card" aria-labelledby="uploads-heading"><h2 id="uploads-heading">Guest uploads</h2>
      {#if event}<dl class="stat-grid"><div><dt>Photos</dt><dd>{event.media.photo_count}{#if event.max_assets !== null}<small> / {event.max_assets}</small>{/if}</dd></div><div><dt>Storage used</dt><dd>{formatBytes(event.media.storage_bytes)}{#if event.max_bytes !== null}<small> / {formatBytes(event.max_bytes)}</small>{/if}</dd></div></dl>
        {#if event.media.pending_count}<p class="hint">{event.media.pending_count} pending photos reserve {formatBytes(event.media.reserved_bytes ?? 0)}.</p>{/if}
        {#if !event.media.photo_count}<p class="hint">No photos yet. Share the link or QR code to invite your guests.</p>{/if}
        {#if (event.max_assets !== null && event.media.photo_count + (event.media.pending_count ?? 0) >= event.max_assets) || (event.max_bytes !== null && event.media.storage_bytes + (event.media.reserved_bytes ?? 0) >= event.max_bytes)}<p class="notice" role="status">Photo or storage limit reached. Existing photos are safe. Raise or remove the limit to accept more.</p>{/if}
        {#if event.contributors?.length}<h3>Shared by</h3><ul class="contributor-list">{#each event.contributors as contributor}<li><span>{contributor.name ?? 'Anonymous'}</span><strong>{contributor.photo_count} {contributor.photo_count === 1 ? 'photo' : 'photos'}</strong></li>{/each}</ul><p class="hint">Optional names supplied by guests, not verified identities. Up to 100 names shown.</p>{/if}
        <button type="button" class="text-button" disabled={refreshingStats} onclick={refreshStats}>{refreshingStats ? 'Refreshing…' : 'Refresh photo counts'}</button>
      {/if}
      <fieldset disabled={busy || event?.deleting}>
        <div class="field"><label for="max-photos">Maximum photos</label><input id="max-photos" type="number" min="1" max="1000000" step="1" bind:value={maxPhotos} aria-invalid={!!fields.max_assets} aria-describedby="photos-quota-hint photos-quota-error" /><p id="photos-quota-hint" class="hint">Leave empty for no limit. Completed and pending photos both count.</p><div id="photos-quota-error">{#if fields.max_assets}<p class="error">{fields.max_assets}</p>{/if}</div></div>
        <div class="field"><label for="max-storage">Maximum storage (GiB)</label><input id="max-storage" type="number" min="0.000000001" max="1048576" step="any" bind:value={maxStorageGiB} aria-invalid={!!fields.max_bytes} aria-describedby="storage-quota-hint storage-quota-error" /><p id="storage-quota-hint" class="hint">Leave empty for no limit. 1 GiB = 1,073,741,824 bytes. Lowering limits never deletes photos.</p><div id="storage-quota-error">{#if fields.max_bytes}<p class="error">{fields.max_bytes}</p>{/if}</div></div>
      </fieldset>
    </section>
    <div class="actions save-actions"><button type="submit" disabled={busy || event?.deleting}>{busy ? 'Saving…' : id ? 'Save changes' : 'Create event'}</button><a href="/admin">Cancel</a></div></form>
    {#if event}<Immich eventID={event.id} deleting={event.deleting} />{/if}
    {#if event}<section class="section-card" aria-labelledby="export-heading"><h2 id="export-heading">Export</h2><p>Keep original files and a portable manifest with the PhotoDrop CLI.</p><code class="export-command">photodrop export --event {event.id} --output /export/event-{event.id}</code><button type="button" class="secondary" onclick={copyExport}>Copy export command</button><p class="hint">Run on your PhotoDrop host. Mount a writable /export directory for Docker, and choose a new output directory for each export.</p></section>{/if}
    {#if event}<section class="delete-panel" aria-labelledby="delete-heading"><h2 id="delete-heading">Danger zone</h2>
      {#if confirming}<p>Delete “{event.name}” and all its uploaded photos permanently? Its public link will stop working. This cannot be undone.</p><div class="actions"><button class="danger" disabled={busy} bind:this={confirmButton} onclick={remove}>Delete event permanently</button><button class="secondary" disabled={busy} onclick={keepEvent}>Keep event</button></div>
      {:else}<p>Remove this event, its public page, and all its uploaded photos.</p><button class="danger secondary" disabled={busy} bind:this={deleteButton} onclick={confirmDelete}>Delete event…</button>{/if}
    </section>{/if}
  {/if}
</main>
