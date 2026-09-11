<script lang="ts">
  import { onDestroy } from 'svelte';
  import { request, message, formatBytes } from '../lib/api';

  let { publicID, maxFileSize }: { publicID: string; maxFileSize: number } = $props();
  type Item = { file: File; status: 'waiting' | 'uploading' | 'ready' | 'failed'; sent: number; error: string };
  const batchLimit = 100;
  const concurrency = 3;
  let items = $state<Item[]>([]);
  let busy = $state(false);
  let error = $state('');
  let attempted = $state(false);
  let input = $state<HTMLInputElement>();
  let sessionID = '';
  let canceled = false;
  const active = new Set<XMLHttpRequest>();
  const totalBytes = $derived(items.reduce((sum, item) => sum + item.file.size, 0));
  const sentBytes = $derived(items.reduce((sum, item) => sum + item.sent, 0));
  const percent = $derived(totalBytes ? Math.floor(sentBytes / totalBytes * 100) : 0);
  const completed = $derived(items.filter(item => item.status === 'ready').length);
  const failed = $derived(items.filter(item => item.status === 'failed').length);
  const waiting = $derived(items.filter(item => item.status === 'waiting').length);

  function choose(event: Event) {
    const files = Array.from((event.currentTarget as HTMLInputElement).files ?? []);
    if (!files.length) return;
    if (files.length > batchLimit) { error = `Choose up to ${batchLimit} photos at a time.`; return; }
    items = files.map(file => ({ file, status: 'waiting', sent: 0, error: '' }));
    sessionID = ''; error = ''; attempted = false;
  }

  function transfer(item: Item): Promise<void> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const finish = (cause?: Error) => { active.delete(xhr); cause ? reject(cause) : resolve(); };
      xhr.open('POST', `/api/public/events/${encodeURIComponent(publicID)}/upload-sessions/${sessionID}/assets`);
      xhr.timeout = 10 * 60 * 1000 + 15000;
      xhr.setRequestHeader('Content-Type', item.file.type || 'application/octet-stream');
      const filename = encodeURIComponent(item.file.name).replace(/['()*]/g, character => `%${character.charCodeAt(0).toString(16).toUpperCase()}`);
      xhr.setRequestHeader('Content-Disposition', `attachment; filename*=UTF-8''${filename}`);
      xhr.upload.onprogress = event => { item.sent = Math.min(event.loaded, item.file.size); };
      xhr.onload = () => {
        let body: { asset?: { status?: string }; error?: { message?: string } } = {};
        try { body = JSON.parse(xhr.responseText); } catch { /* Use a safe generic error. */ }
        if (xhr.status === 201 && body.asset?.status === 'ready') finish();
        else finish(new Error(body.error?.message || 'Upload failed. Please try again.'));
      };
      xhr.onerror = () => finish(new Error('Connection lost. Please try again.'));
      xhr.ontimeout = () => finish(new Error('Upload timed out. Please try again.'));
      xhr.onabort = () => finish(new Error('Upload canceled. You can retry this photo.'));
      // Send the File directly: no base64 conversion or whole-file JS reads.
      active.add(xhr);
      try { xhr.send(item.file); }
      catch (cause) { finish(new Error(message(cause))); }
    });
  }

  async function start(retry = false) {
    if (busy) return;
    const selected = items.filter(item => item.status === (retry ? 'failed' : 'waiting'));
    if (!selected.length) return;
    busy = true; canceled = false; attempted = true; error = '';
    for (const item of selected) { item.status = 'waiting'; item.error = ''; item.sent = 0; }
    try {
      if (!sessionID) sessionID = (await request<{ upload_session: { id: string } }>(`/api/public/events/${encodeURIComponent(publicID)}/upload-sessions`, 'POST', {})).upload_session.id;
      let next = 0;
      async function worker() {
        while (!canceled && next < selected.length) {
          const item = selected[next++];
          item.status = 'uploading';
          try {
            if (!item.file.size) throw new Error('This file is empty.');
            if (item.file.size > maxFileSize) throw new Error(`This file exceeds the ${formatBytes(maxFileSize)} limit.`);
            await transfer(item);
            item.status = 'ready'; item.sent = item.file.size;
          } catch (cause) { item.status = 'failed'; item.sent = 0; item.error = message(cause); }
        }
      }
      await Promise.all(Array.from({ length: Math.min(concurrency, selected.length) }, worker));
    } catch (cause) {
      for (const item of selected) { item.status = 'failed'; item.error = message(cause); }
    } finally {
      for (const item of selected) if (item.status === 'waiting') { item.status = 'failed'; item.error = 'Upload canceled. You can retry this photo.'; }
      busy = false;
    }
  }

  function cancel() { canceled = true; for (const xhr of active) xhr.abort(); }
  function addMore() { items = []; attempted = false; sessionID = ''; error = ''; if (input) input.value = ''; }
  onDestroy(cancel);
</script>

<section class="upload-panel" aria-labelledby="upload-heading">
  <h2 id="upload-heading">Share your photos</h2>
  <p class="muted">JPEG, PNG, WebP, GIF, HEIC or HEIF. Up to {formatBytes(maxFileSize)} per photo.</p>
  {#if completed === items.length && items.length > 0}
    <div class="upload-thanks" role="status"><h2>Thank you!</h2><p>{completed} {completed === 1 ? 'photo uploaded' : 'photos uploaded'} successfully.</p></div>
    <button type="button" onclick={addMore}>Add More Photos</button>
  {:else}
    <label for="photos">Choose Photos</label>
    <input id="photos" type="file" accept="image/*" multiple disabled={busy} bind:this={input} onchange={choose} aria-describedby="batch-hint" />
    <p id="batch-hint" class="hint">Choose up to {batchLimit} photos per batch. Keep this page open until uploads finish.</p>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if items.length}
    <p>{items.length} {items.length === 1 ? 'photo selected' : 'photos selected'} · {formatBytes(totalBytes)}</p>
    {#if attempted}
      <label for="overall-progress">Overall upload progress · {percent}%</label>
      <progress id="overall-progress" max="100" value={percent}></progress>
      <p role="status">{completed} of {items.length} complete{#if failed} · {failed} failed{/if}</p>
    {/if}
    <ul class="upload-list" aria-label="Selected photos">
      {#each items as item, i}
        <li>
          <div class="upload-row"><span class="filename">{item.file.name}</span><span class:upload-success={item.status === 'ready'}>{item.status === 'ready' ? '✓ Uploaded' : item.status === 'failed' ? 'Failed' : item.status === 'waiting' ? 'Waiting' : item.sent >= item.file.size ? 'Finishing…' : `${Math.floor(item.sent / Math.max(item.file.size, 1) * 100)}%`}</span></div>
          <span class="hint">{formatBytes(item.file.size)}</span>
          {#if item.status === 'uploading'}<progress aria-label={`Upload progress for ${item.file.name}`} max={Math.max(item.file.size, 1)} value={item.sent}></progress>{/if}
          {#if item.error}<p class="error" id={`upload-error-${i}`}>{item.error}</p>{/if}
        </li>
      {/each}
    </ul>
    <div class="actions">
      {#if busy}<button type="button" class="secondary" onclick={cancel}>Cancel uploads</button>
      {:else if failed}<button type="button" onclick={() => start(true)}>Retry Failed ({failed})</button>
      {:else if waiting}<button type="button" onclick={() => start()}>Upload {waiting} {waiting === 1 ? 'Photo' : 'Photos'}</button>{/if}
    </div>
  {/if}
</section>
