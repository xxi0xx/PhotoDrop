<script lang="ts">
  import { onMount } from 'svelte';
  import { request, message, APIError, displayDate, type GuestEvent } from '../lib/api';
  let { publicID }: { publicID: string } = $props();
  let event = $state<GuestEvent | null>(null); let loading = $state(true); let missing = $state(false); let error = $state('');
  async function load() {
    loading = true; error = ''; missing = false;
    try { event = (await request<{ event: GuestEvent }>(`/api/public/events/${encodeURIComponent(publicID)}`)).event; }
    catch (cause) { if (cause instanceof APIError && cause.status === 404) missing = true; else error = message(cause); }
    finally { loading = false; }
  }
  onMount(() => { void load(); });
</script>
<svelte:head><title>{event?.name ?? 'Event'} · PhotoDrop</title></svelte:head>
<main class="guest-page">
  {#if loading}<p role="status">Loading event…</p>
  {:else if missing}<h1>Event not found</h1><p>This link may be incorrect, or the event may have been removed.</p>
  {:else if error}<h1>Unable to load this event</h1><p class="error" role="alert">{error}</p><button class="secondary" onclick={load}>Try again</button>
  {:else if event}<p class="eyebrow">You're invited</p><h1>{event.name}</h1>
    {#if event.status === 'closed'}<p class="closed-notice">Photo sharing for this event is currently closed.</p>
    {:else}{#if event.event_date}<p class="guest-date">{displayDate(event.event_date)}</p>{/if}{#if event.description}<p class="guest-description">{event.description}</p>{/if}<p class="coming-soon">Photo sharing will be available here soon.</p>{/if}
  {/if}
</main>
