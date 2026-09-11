<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { request, message, publicURL, localDateTime, formatBytes, APIError, type EventRecord } from '../lib/api';
  let { id }: { id: string | null } = $props();
  let event = $state<EventRecord | null>(null);
  let name = $state(''); let description = $state(''); let eventDate = $state(''); let enabled = $state(true); let expiration = $state('');
  let loading = $state(true); let loadError = $state(''); let busy = $state(false); let error = $state(''); let notice = $state('');
  let fields = $state<Record<string, string>>({}); let confirming = $state(false); let confirmButton = $state<HTMLButtonElement>();
  function apply(saved: EventRecord) { event = saved; name = saved.name; description = saved.description; eventDate = saved.event_date ?? ''; enabled = saved.enabled; expiration = localDateTime(saved.expires_at); }
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
      const data = { name, description, event_date: eventDate || null, enabled, expires_at: expiration ? new Date(expiration).toISOString() : null };
      const result = await request<{ event: EventRecord }>(id ? `/api/admin/events/${id}` : '/api/admin/events', id ? 'PUT' : 'POST', data);
      if (!id) { window.location.assign(`/admin/events/${result.event.id}`); return; }
      apply(result.event); notice = 'Event saved.';
    } catch (cause) { error = message(cause); if (cause instanceof APIError) fields = cause.fields; }
    finally { busy = false; }
  }
  async function copyLink() {
    if (!event) return;
    try { await navigator.clipboard.writeText(publicURL(event)); notice = 'Public link copied.'; }
    catch { notice = 'Select and copy the public URL below.'; }
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
    {#if event}<section class="link-panel" aria-label="Public event link"><label for="public-url">Public URL</label><input id="public-url" readonly value={publicURL(event)} /><div class="actions"><button type="button" class="secondary" onclick={copyLink}>Copy link</button><a href={event.public_url} target="_blank" rel="noopener noreferrer">Visit guest page ↗</a></div></section>{/if}
    {#if notice}<p class="notice" role="status">{notice}</p>{/if}{#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if event}<p class="media-stats">Photos: {event.media.photo_count} · Storage: {formatBytes(event.media.storage_bytes)}</p>{/if}
    {#if event?.deleting}<p class="error" role="alert">This event is closed while media cleanup is pending. Retry deletion below.</p>{/if}
    <form onsubmit={save}><fieldset disabled={busy || event?.deleting}>
      <div class="field"><label for="event-name">Name <span class="muted">(required)</span></label><input id="event-name" name="name" required maxlength="200" bind:value={name} aria-invalid={!!fields.name} aria-describedby={fields.name ? 'name-error' : undefined} />{#if fields.name}<p id="name-error" class="error">{fields.name}</p>{/if}</div>
      <div class="field"><label for="description">Description</label><textarea id="description" name="description" maxlength="4000" rows="4" bind:value={description} aria-invalid={!!fields.description} aria-describedby={fields.description ? 'description-error' : undefined}></textarea>{#if fields.description}<p id="description-error" class="error">{fields.description}</p>{/if}<p class="hint">Optional guest-facing text. Up to 4,000 characters.</p></div>
      <div class="field"><label for="event-date">Event date</label><input id="event-date" name="event_date" type="date" bind:value={eventDate} aria-invalid={!!fields.event_date} aria-describedby={fields.event_date ? 'date-error' : undefined} />{#if fields.event_date}<p id="date-error" class="error">{fields.event_date}</p>{/if}<p class="hint">Optional calendar date for the event.</p></div>
      <div class="field"><label class="checkbox" for="enabled"><input id="enabled" name="enabled" type="checkbox" bind:checked={enabled} />Enabled</label><p class="hint">Guests can view this event while it is enabled and has not expired.</p>{#if fields.enabled}<p class="error">{fields.enabled}</p>{/if}</div>
      <div class="field"><label for="expiration">Expiration <span class="muted">(your local time)</span></label><input id="expiration" name="expires_at" type="datetime-local" step="1" bind:value={expiration} aria-invalid={!!fields.expires_at} aria-describedby={fields.expires_at ? 'expiry-error' : undefined} />{#if fields.expires_at}<p id="expiry-error" class="error">{fields.expires_at}</p>{/if}<p class="hint">Leave empty for no expiration. Expiration closes the guest page; it does not delete the event.</p></div>
      <div class="actions"><button type="submit">{busy ? 'Saving…' : id ? 'Save changes' : 'Create event'}</button><a href="/admin">Cancel</a></div>
    </fieldset></form>
    {#if event}<section class="delete-panel" aria-labelledby="delete-heading"><h2 id="delete-heading">Delete event</h2>
      {#if confirming}<p>Delete “{event.name}” and all its uploaded photos permanently? Its public link will stop working. This cannot be undone.</p><div class="actions"><button class="danger" disabled={busy} bind:this={confirmButton} onclick={remove}>Delete event permanently</button><button class="secondary" disabled={busy} onclick={() => confirming = false}>Keep event</button></div>
      {:else}<p>Remove this event, its public page, and all its uploaded photos.</p><button class="danger secondary" disabled={busy} onclick={confirmDelete}>Delete event…</button>{/if}
    </section>{/if}
  {/if}
</main>
