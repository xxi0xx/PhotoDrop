<script lang="ts">
  import { onMount } from 'svelte';
  import { request, message, displayDate, formatBytes, type EventRecord } from '../lib/api';
  let events = $state<EventRecord[]>([]); let loading = $state(true); let error = $state('');
  async function load() {
    loading = true; error = '';
    try { events = (await request<{ events: EventRecord[] }>('/api/admin/events')).events; }
    catch (cause) { error = message(cause); } finally { loading = false; }
  }
  onMount(() => { void load(); });
</script>
<svelte:head><title>Events · PhotoDrop Admin</title></svelte:head>
<main class="workspace">
  <div class="page-heading"><div><p class="eyebrow">Administration</p><h1>Your events</h1></div><a class="button" href="/admin/events/new">New Event</a></div>
  <p class="muted">Manage the details and availability of each event.</p>
  {#if loading}<p role="status">Loading events…</p>
  {:else if error}<p class="error" role="alert">{error}</p><button class="secondary" onclick={load}>Try again</button>
  {:else if events.length === 0}<section class="empty"><h2>No events yet</h2><p>Create your first event to give guests a page of their own.</p><a href="/admin/events/new">Create an event</a></section>
  {:else}
    <div class="event-list">{#each events as event (event.id)}
      <article class="event-card">
        <div class="event-card-top"><h2><a href={`/admin/events/${event.id}`}>{event.name}</a></h2><span class:open={event.status === 'open'} class="badge">{event.status === 'open' ? 'Open' : event.status === 'disabled' ? 'Disabled' : 'Expired'}</span></div>
        <p class="muted">{displayDate(event.event_date)}</p>
        <p class="media-stats">Files: {event.media.photo_count}{#if event.max_assets !== null} / {event.max_assets}{/if} · Storage: {formatBytes(event.media.storage_bytes)}{#if event.max_bytes !== null} / {formatBytes(event.max_bytes)}{/if}</p>
        {#if event.deleting}<p class="error">Media cleanup is pending. Open Manage and retry deletion.</p>{/if}
        <div class="event-card-bottom"><a href={event.public_url} target="_blank" rel="noopener noreferrer">Public event page ↗</a><a class="button secondary" href={`/admin/events/${event.id}`}>Manage<span class="sr-only"> {event.name}</span></a></div>
      </article>
    {/each}</div>
    <button class="text-button" onclick={load}>Refresh events</button>
  {/if}
</main>
