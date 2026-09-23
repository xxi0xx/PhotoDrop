<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { request, formatBytes, APIError, type Challenge } from '../lib/api';
  import Turnstile from './Turnstile.svelte';
  import { UploadBatch, type Grant } from '../lib/upload-batch.svelte';
  import { runDirect, recoverUploadGrant, type UploadPlan, type Prepared, type DirectAttempt } from '../lib/direct-upload';
  import { photoProblem, photoStatus, guestError, retryAfterAt, selectedForUpload, type PhotoState } from '../lib/upload-ux';
  let { publicID, maxFileSize, challenge }: { publicID: string; maxFileSize: number; challenge?: Challenge } = $props();
  type Item = { file: File; status: PhotoState; sent: number; error: string; validationError: string; attempt: DirectAttempt; grant?: Grant; batch: UploadBatch };
  const batchLimit = 100, concurrency = 3;
  let items = $state<Item[]>([]);
  let busy = $state(false), attempted = $state(false), adding = $state(false);
  let error = $state(''), contributorName = $state(''), nameError = $state('');
  let input = $state<HTMLInputElement>(), nameInput = $state<HTMLInputElement>(), completion = $state<HTMLHeadingElement>();
  let challengeToken = $state(''), challengeVersion = $state(0), cooling = $state(false);
  let retryNotBefore = 0, cooldownTimer: ReturnType<typeof setTimeout> | undefined;
  let canceled = false;
  const active = new Set<XMLHttpRequest>();
  const totalBytes = $derived(items.reduce((sum, item) => sum + item.file.size, 0));
  const sentBytes = $derived(items.reduce((sum, item) => sum + item.sent, 0));
  const percent = $derived(totalBytes ? Math.min(100, Math.floor(sentBytes / totalBytes * 100)) : 0);
  const completed = $derived(items.filter(item => item.status === 'ready').length);
  const failed = $derived(items.filter(item => item.status === 'failed').length);
  const retryable = $derived(selectedForUpload(items, true).length);
  const waiting = $derived(selectedForUpload(items, false).length);
  const allDone = $derived(items.length > 0 && completed === items.length);
  const needsChallenge = $derived(!!challenge && items.some(item => item.status !== 'ready' && !item.validationError && !item.grant && !item.batch.grant));
  function choose(event: Event) {
    const files = Array.from((event.currentTarget as HTMLInputElement).files ?? []);
    if (!files.length) return;
    if (files.length > batchLimit) { error = `Choose up to ${batchLimit} photos at a time.`; return; }
    const batch = new UploadBatch();
    const selected: Item[] = files.map(file => {
      const problem = photoProblem(file, maxFileSize);
      return { file, batch, status: problem ? 'failed' : 'waiting', sent: 0, error: problem, validationError: problem,
        attempt: { requestID: Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2,'0')).join('') } };
    });
    // Adding never discards a failed/uncertain attempt or a completed row.
    items = adding ? [...items, ...selected] : selected;
    error = ''; nameError = ''; challengeToken = ''; challengeVersion++;
  }
  function checkRate() {
    if (Date.now() < retryNotBefore) throw new APIError('Please wait a moment before trying again.', 429, {}, 'rate_limited', retryNotBefore);
  }
  function pauseUntil(instant: number) {
    retryNotBefore = Math.max(retryNotBefore, instant || Date.now() + 1000); cooling = true;
    if (cooldownTimer) clearTimeout(cooldownTimer);
    const resume = () => { const remaining = retryNotBefore - Date.now(); if (remaining > 0) cooldownTimer = setTimeout(resume, Math.min(remaining, 2147483647)); else cooling = false; };
    resume();
  }
  async function getGrant(batch: UploadBatch): Promise<Grant> {
    if (batch.grant) return batch.grant;
    if (batch.creating) return batch.creating;
    checkRate();
    if (challenge && !challengeToken) throw new Error('Please complete verification before uploading.');
    const token = challengeToken; challengeToken = '';
    batch.creating = (async () => {
      try {
        const result = await request<{ upload_session: { id: string; expires_at: string }; upload_strategy: string }>(`/api/public/events/${encodeURIComponent(publicID)}/upload-sessions`, 'POST', { contributor_name: batch.name ?? '', ...(challenge ? { turnstile_token: token } : {}) });
        batch.grant = { ...result.upload_session, strategy: result.upload_strategy }; return batch.grant;
      } finally { challengeVersion++; batch.creating = null; }
    })();
    return batch.creating;
  }
  function transfer(item: Item, plan?: UploadPlan): Promise<void> {
    checkRate();
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      const finish = (cause?: Error) => { active.delete(xhr); cause ? reject(cause) : resolve(); };
      xhr.open(plan?.method ?? 'POST', plan?.url ?? `/api/public/events/${encodeURIComponent(publicID)}/upload-sessions/${item.grant!.id}/assets`);
      xhr.timeout = 10 * 60 * 1000 + 15000;
      if (plan) { for (const [name,value] of Object.entries(plan.headers)) xhr.setRequestHeader(name,value); }
      else {
        xhr.setRequestHeader('Content-Type', item.file.type || 'application/octet-stream');
        const filename = encodeURIComponent(item.file.name).replace(/['()*]/g, character => `%${character.charCodeAt(0).toString(16).toUpperCase()}`);
        xhr.setRequestHeader('Content-Disposition', `attachment; filename*=UTF-8''${filename}`);
      }
      xhr.upload.onprogress = event => { item.sent = Math.min(event.loaded, item.file.size); };
      xhr.onload = () => {
        if (plan) { finish(xhr.status >= 200 && xhr.status < 300 ? undefined : new Error('Upload did not finish. Please retry this photo.')); return; }
        let body: { asset?: { status?: string }; error?: { message?: string; code?: string } } = {};
        try { body = JSON.parse(xhr.responseText); } catch { /* Never expose raw provider or HTML responses. */ }
        if (xhr.status === 201 && body.asset?.status === 'ready') finish();
        else finish(new APIError(body.error?.message || 'Upload failed. Please try again.', xhr.status, {}, body.error?.code, retryAfterAt(xhr.getResponseHeader('Retry-After'))));
      };
      xhr.onerror = () => finish(new Error('Connection lost. Please try again.'));
      xhr.ontimeout = () => finish(new Error('Upload timed out. Please try again.'));
      xhr.onabort = () => finish(new Error('Upload canceled. You can retry this photo.'));
      active.add(xhr);
      // Send the original File directly, including direct-to-S3 PUTs.
      try { xhr.send(item.file); } catch { finish(new Error('Could not send this photo. Please try again.')); }
    });
  }
  async function start(retry = false) {
    if (busy || Date.now() < retryNotBefore) return;
    const selected = selectedForUpload(items, retry);
    if (!selected.length) return;
    if (selected.some(item => item.batch.name === null) && ([...contributorName.trim()].length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(contributorName.trim()))) {
      nameError = 'Use up to 100 characters without control characters.'; nameInput?.focus(); return;
    }
    for (const item of selected) item.batch.name ??= contributorName.trim();
    busy = true; canceled = false; attempted = true; adding = false; error = ''; nameError = '';
    for (const item of selected) { item.status = 'waiting'; item.error = ''; item.sent = 0; }
    try {
      let next = 0;
      async function worker() {
        while (!canceled && Date.now() >= retryNotBefore && next < selected.length) {
          const item = selected[next++]; item.status = 'preparing';
          try {
            item.grant ??= await getGrant(item.batch);
            if (canceled) throw new Error('Upload canceled. You can retry this photo.');
            if (item.grant.strategy === 'direct') {
              const base = `/api/public/events/${encodeURIComponent(publicID)}/upload-sessions/${item.grant.id}/assets`;
              await runDirect(item.attempt, {
                prepare: () => { checkRate(); return request<Prepared>(`${base}/prepare`, 'POST', { filename: item.file.name, size: item.file.size, content_type: item.file.type || 'application/octet-stream', request_id: item.attempt.requestID }); },
                authorize: id => { checkRate(); return request<Prepared>(`${base}/${id}/authorize`, 'POST', {}); },
                complete: id => { checkRate(); return request(`${base}/${id}/complete`, 'POST', {}); },
                put: plan => { item.sent = 0; return transfer(item,plan); },
                stage: stage => { item.status = stage; }, canceled: () => canceled,
              });
            } else { item.status = 'uploading'; await transfer(item); }
            item.status = 'ready'; item.sent = item.file.size;
          } catch (cause) {
            const stage = item.status;
            item.status = 'failed'; item.sent = 0; item.error = guestError(cause);
            if (cause instanceof APIError && cause.status === 429) pauseUntil(cause.retryAt);
            if (cause instanceof APIError && cause.code === 'contributor_name') nameError = cause.message;
            if (recoverUploadGrant(item, cause, stage)) item.batch.grant = null;
          }
        }
      }
      await Promise.all(Array.from({ length: Math.min(concurrency, selected.length) }, worker));
    } finally {
      for (const item of selected) if (item.status === 'waiting') { item.status = 'failed'; item.error = cooling ? 'Please wait a moment and retry this photo.' : 'Upload canceled. You can retry this photo.'; }
      busy = false;
      if (allDone) { await tick(); completion?.focus(); }
    }
  }
  function cancel() { canceled = true; for (const xhr of active) xhr.abort(); }
  async function addMore() {
    if (allDone) { items = []; attempted = false; }
    adding = true; challengeToken = ''; error = ''; nameError = ''; challengeVersion++;
    await tick(); if (input) { input.value = ''; input.focus(); }
  }
  function warnBeforeLeaving(event: BeforeUnloadEvent) { if (busy) { event.preventDefault(); event.returnValue = ''; } }
  onDestroy(() => { cancel(); if (cooldownTimer) clearTimeout(cooldownTimer); });
</script>

<svelte:window onbeforeunload={warnBeforeLeaving} />
<section class="upload-panel" aria-labelledby="upload-heading">
  {#if allDone}
    <div class="upload-thanks" role="status"><span class="success-mark" aria-hidden="true">✓</span><h2 id="upload-heading" tabindex="-1" bind:this={completion}>Thanks for sharing!</h2><p>{completed} {completed === 1 ? 'photo uploaded' : 'photos uploaded'} successfully.</p><p class="hint">Your photos are with the event host.</p></div>
    <button type="button" onclick={addMore}>Add more photos</button>
  {:else}
    <h2 id="upload-heading">Share your photos</h2><p class="section-intro">A moment you captured. A memory to keep.</p>
    {#if !attempted || adding}
      <div class="field"><label for="contributor-name">Your name <span class="muted">(optional)</span></label><input id="contributor-name" autocomplete="off" bind:value={contributorName} bind:this={nameInput} disabled={busy} aria-invalid={!!nameError} aria-describedby={nameError ? 'contributor-hint contributor-error' : 'contributor-hint'} />
        <p id="contributor-hint" class="hint">Only the host sees this name with your photos. No account needed. Up to 100 characters.</p>{#if nameError}<p id="contributor-error" class="error">{nameError}</p>{/if}</div>
      <label for="photos">Choose photos</label><input id="photos" type="file" accept="image/jpeg,image/png,image/webp,image/gif,image/heic,image/heif,.heic,.heif" multiple disabled={busy} bind:this={input} onchange={choose} aria-describedby="batch-hint selection-error" />
      <p id="batch-hint" class="hint">JPEG, PNG, WebP, GIF, HEIC or HEIF. Up to {batchLimit} photos at a time, {formatBytes(maxFileSize)} each.</p>
    {/if}
    <div id="selection-error">{#if error}<p class="error" role="alert">{error}</p>{/if}</div>
    {#if items.length}
      {#if busy}<div class="progress-summary"><h3>Uploading photos</h3><p role="status">{completed} of {items.length} complete</p><label for="overall-progress">Overall progress · {percent}%</label><progress id="overall-progress" max="100" value={percent}></progress><p class="hint">Keep this page open until your photos finish.</p></div>
      {:else if attempted && failed}<div class="batch-result" role="status"><h3>{completed ? `${completed} ${completed === 1 ? 'photo uploaded' : 'photos uploaded'}` : "We couldn't upload these photos"}</h3><p>{failed} {failed === 1 ? 'photo needs' : 'photos need'} another try. {#if completed}Your uploaded photos are safe and will not be sent again.{/if}</p></div>{/if}
      <p class="selection-summary">{items.length} {items.length === 1 ? 'photo selected' : 'photos selected'} <span>· {formatBytes(totalBytes)}</span></p>
      <ul class="upload-list" aria-label="Selected photos">
        {#each items as item, i}<li aria-describedby={item.error ? `upload-error-${i}` : undefined}>
          <div class="upload-row"><span class="filename" title={item.file.name}>{item.file.name}</span><span class:upload-success={item.status === 'ready'}>{photoStatus(item.status, item.sent, item.file.size)}</span></div>
          <span class="hint">{formatBytes(item.file.size)}</span>
          {#if item.status === 'uploading'}<progress aria-label={`Upload progress for ${item.file.name}`} max={Math.max(item.file.size, 1)} value={item.sent}></progress>{/if}
          {#if item.error}<p class="error" id={`upload-error-${i}`}>{item.error}</p>{/if}
          {#if item.validationError && !busy}<button type="button" class="text-button" aria-label={`Remove unsupported photo ${item.file.name}`} onclick={() => items = items.filter(one => one !== item)}>Remove from selection</button>{/if}
        </li>{/each}
      </ul>
      {#if needsChallenge && challenge}{#key challengeVersion}<Turnstile siteKey={challenge.site_key} action={challenge.action} onToken={token => challengeToken = token} />{/key}{/if}
      {#if cooling}<p class="notice" role="status">Please wait a moment. You can retry when the button becomes available.</p>{/if}
      <div class="actions upload-actions">
        {#if busy}<button type="button" class="secondary" onclick={cancel}>Cancel uploads</button>
        {:else}
          {#if waiting}<button type="button" disabled={cooling} onclick={() => start()}>Upload {waiting} {waiting === 1 ? 'photo' : 'photos'}</button>{/if}
          {#if retryable}<button type="button" disabled={cooling} onclick={() => start(true)}>Retry {retryable} failed {retryable === 1 ? 'photo' : 'photos'}</button>{/if}
          {#if attempted && !adding}<button type="button" class="secondary" onclick={addMore}>Add more photos</button>{/if}
        {/if}
      </div>
    {/if}
  {/if}
</section>
