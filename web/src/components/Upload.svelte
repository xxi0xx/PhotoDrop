<script lang="ts">
  import { onDestroy } from 'svelte';
  import { request, message, formatBytes, APIError, type Challenge } from '../lib/api';
  import Turnstile from './Turnstile.svelte';
  import { runDirect, recoverUploadGrant, type UploadPlan, type Prepared, type DirectAttempt } from '../lib/direct-upload';

  let { publicID, maxFileSize, challenge }: { publicID: string; maxFileSize: number; challenge?: Challenge } = $props();
  type Grant = { id: string; expires_at: string; strategy: string };
  type Item = { file: File; status: 'waiting' | 'preparing' | 'uploading' | 'verifying' | 'ready' | 'failed'; sent: number; error: string; attempt: DirectAttempt; grant?: Grant };
  const batchLimit = 100;
  const concurrency = 3;
  let items = $state<Item[]>([]);
  let busy = $state(false);
  let error = $state('');
  let attempted = $state(false);
  let input = $state<HTMLInputElement>();
  let grant = $state<Grant | null>(null);
  let challengeToken = $state('');
  let challengeVersion = $state(0);
  let creating: Promise<Grant> | null = null;
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
    items = files.map(file => ({ file, status: 'waiting', sent: 0, error: '', attempt: { requestID: Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2,'0')).join('') } }));
    grant = null; error = ''; attempted = false; challengeToken = ''; challengeVersion++;
  }

  async function getGrant(): Promise<Grant> {
    if (grant) return grant;
    if (creating) return creating;
    if (challenge && !challengeToken) throw new Error('Please complete verification before uploading.');
    const token = challengeToken;
    creating = (async () => {
      try {
        const result = await request<{ upload_session: { id: string; expires_at: string }; upload_strategy: string }>(`/api/public/events/${encodeURIComponent(publicID)}/upload-sessions`, 'POST', challenge ? { turnstile_token: token } : {});
        grant = { ...result.upload_session, strategy: result.upload_strategy };
        return grant;
      } finally { challengeToken = ''; challengeVersion++; creating = null; }
    })();
    return creating;
  }

  function transfer(item: Item, plan?: UploadPlan): Promise<void> {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const finish = (cause?: Error) => { active.delete(xhr); cause ? reject(cause) : resolve(); };
      xhr.open(plan?.method ?? 'POST', plan?.url ?? `/api/public/events/${encodeURIComponent(publicID)}/upload-sessions/${item.grant!.id}/assets`);
      xhr.timeout = 10 * 60 * 1000 + 15000;
      if (plan) {
        for (const [name,value] of Object.entries(plan.headers)) xhr.setRequestHeader(name,value);
      } else {
        xhr.setRequestHeader('Content-Type', item.file.type || 'application/octet-stream');
        const filename = encodeURIComponent(item.file.name).replace(/['()*]/g, character => `%${character.charCodeAt(0).toString(16).toUpperCase()}`);
        xhr.setRequestHeader('Content-Disposition', `attachment; filename*=UTF-8''${filename}`);
      }
      xhr.upload.onprogress = event => { item.sent = Math.min(event.loaded, item.file.size); };
      xhr.onload = () => {
        if (plan) { finish(xhr.status >= 200 && xhr.status < 300 ? undefined : new Error('Upload did not finish. Please retry this photo.')); return; }
        let body: { asset?: { status?: string }; error?: { message?: string; code?: string } } = {};
        try { body = JSON.parse(xhr.responseText); } catch { /* Use a safe generic error. */ }
        if (xhr.status === 201 && body.asset?.status === 'ready') finish();
        else finish(new APIError(body.error?.message || 'Upload failed. Please try again.', xhr.status, {}, body.error?.code));
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
      let batchGrant: Promise<Grant> | undefined;
      let next = 0;
      async function worker() {
        while (!canceled && next < selected.length) {
          const item = selected[next++];
          item.status = 'uploading';
          try {
            if (!item.file.size) throw new Error('This file is empty.');
            if (item.file.size > maxFileSize) throw new Error(`This file exceeds the ${formatBytes(maxFileSize)} limit.`);
            item.grant ??= await (batchGrant ??= getGrant());
            if (item.grant.strategy === 'direct') {
              const base = `/api/public/events/${encodeURIComponent(publicID)}/upload-sessions/${item.grant.id}/assets`;
              await runDirect(item.attempt, {
                prepare: () => request<Prepared>(`${base}/prepare`, 'POST', { filename: item.file.name, size: item.file.size, content_type: item.file.type || 'application/octet-stream', request_id: item.attempt.requestID }),
                authorize: id => request<Prepared>(`${base}/${id}/authorize`, 'POST', {}),
                complete: id => request(`${base}/${id}/complete`, 'POST', {}),
                put: plan => { item.sent = 0; return transfer(item,plan); },
                stage: stage => { item.status = stage; }, canceled: () => canceled,
              });
            } else await transfer(item);
            item.status = 'ready'; item.sent = item.file.size;
          } catch (cause) {
            const stage = item.status;
            item.status = 'failed'; item.sent = 0; item.error = message(cause);
            if (recoverUploadGrant(item, cause, stage)) grant = null;
          }
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
  function addMore() { items = []; attempted = false; grant = null; challengeToken = ''; error = ''; if (input) input.value = ''; }
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
          <div class="upload-row"><span class="filename">{item.file.name}</span><span class:upload-success={item.status === 'ready'}>{item.status === 'ready' ? '✓ Uploaded' : item.status === 'failed' ? 'Failed' : item.status === 'waiting' ? 'Waiting' : item.status === 'preparing' ? 'Preparing…' : item.status === 'verifying' ? 'Verifying…' : item.sent >= item.file.size ? 'Finishing…' : `${Math.floor(item.sent / Math.max(item.file.size, 1) * 100)}%`}</span></div>
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
    {#if challenge && !grant && completed < items.length}
      {#key challengeVersion}<Turnstile siteKey={challenge.site_key} action={challenge.action} onToken={token => challengeToken = token} />{/key}
    {/if}
  {/if}
</section>
