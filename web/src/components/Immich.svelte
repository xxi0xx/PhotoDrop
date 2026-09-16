<script lang="ts">
  import { onMount } from 'svelte';
  import { message, request } from '../lib/api';
  import { importView, type ImmichStatus } from '../lib/immich';
  let { eventID, deleting = false }: { eventID: number; deleting?: boolean } = $props();
  let status = $state<ImmichStatus>();
  let target = $state(''); let albumName = $state(''); let error = $state('');
  let pollError = $state('');
  let busy = $state(false); let testing = $state(false); let connection = $state('');
  let stopped = false; let timer: ReturnType<typeof setTimeout> | undefined;
  let generation = 0;
  const path = $derived(`/api/admin/events/${eventID}/immich`);
  const view = $derived(status ? importView(status, busy, deleting) : undefined);
  async function refresh(reset = false) {
    const ownGeneration = ++generation;
    if (timer) clearTimeout(timer);
    try {
      const next = await request<ImmichStatus>(`${path}?target=${encodeURIComponent(target)}`);
      if (stopped || ownGeneration !== generation) return;
      status = next;
      if (!target && next.target) target = next.target.key;
      if (reset || !albumName) albumName = next.album_name;
      pollError = '';
    } catch (cause) { if (!stopped && ownGeneration === generation) pollError = message(cause); }
    finally { if (!stopped && ownGeneration === generation) timer = setTimeout(() => void refresh(), 2000); }
  }
  onMount(() => { void refresh(true); return () => { stopped = true; generation++; if (timer) clearTimeout(timer); }; });
  async function changeTarget() {
    busy = true; connection = ''; albumName = ''; error = '';
    try { await refresh(true); } finally { busy = false; }
  }
  async function testConnection() {
    busy = true; testing = true; connection = ''; error = '';
    try { const result = await request<{ version: string }>(`${path}/test`, 'POST', { target }); connection = `Connected · Immich ${result.version}`; }
    catch (cause) { connection = message(cause); }
    finally { busy = false; testing = false; }
  }
  async function send(mode: 'new' | 'retry') {
    busy = true; error = '';
    try { await request(`${path}/jobs`, 'POST', { target, album_name: albumName, mode }); await refresh(); }
    catch (cause) { error = message(cause); }
    finally { busy = false; }
  }
  async function cancel() {
    busy = true;
    try { await request(`${path}/cancel`, 'POST', { target }); await refresh(); }
    catch (cause) { error = message(cause); }
    finally { busy = false; }
  }
</script>
<section class="link-panel" aria-labelledby="immich-heading">
  <h2 id="immich-heading">Immich</h2>
  <p class="hint">Send independent copies to Immich. Deleting this event leaves the Immich album and photos untouched. Cancel an active import before deleting the event.</p>
  {#if error || pollError}<p class="error" role="alert">{error || pollError}</p>{/if}
  {#if !status}<p role="status">Loading integration…</p>
  {:else if status.targets.length === 0}<p class="hint">Optional: configure a named Immich target in your deployment to enable imports.</p>
  {:else}
    <div class="field"><label for="immich-target">Target</label><select id="immich-target" bind:value={target} disabled={busy || view?.active} onchange={changeTarget}>
      {#if !target}<option value="">Choose a target</option>{/if}
      {#each status.targets as one}<option value={one.key}>{one.key}{one.available ? '' : ' (credentials unavailable)'}</option>{/each}
    </select></div>
    {#if status.target}
      <div class="actions"><button class="secondary" disabled={busy || !status.target.available} onclick={testConnection}>{testing ? 'Testing connection…' : 'Test Immich connection'}</button></div>
      {#if connection}<p role="status">{connection}</p>{/if}
      <div class="field"><label for="immich-album">Album name</label><input id="immich-album" maxlength="200" bind:value={albumName} disabled={!!status.album_id || busy || view?.active} /><p class="hint">Choose a name before the first import. Later imports use the same album, even if you rename it in Immich.</p></div>
      <p class="media-stats">{status.total} photos total · {status.imported} imported · {status.duplicate} duplicates accounted for · {status.failed} failed · {status.new + status.pending} not yet imported</p>
      {#if view}<p role="status">{view.label} · {view.accounted} of {status.total} accounted for</p>{/if}
      {#if status.job?.error}<p class="error">{status.job.error}</p>{/if}
      <div class="actions">
        <button disabled={!view?.canSend} onclick={() => send('new')}>Send {view?.sendCount ?? 0} photos to Immich</button>
        <button class="secondary" disabled={!view?.canRetry} onclick={() => send('retry')}>Retry {status.failed} failed photos</button>
        {#if view?.active}<button class="secondary" disabled={busy || status.job?.cancel_requested} onclick={cancel}>Cancel import</button>{/if}
      </div>
    {/if}
  {/if}
</section>
