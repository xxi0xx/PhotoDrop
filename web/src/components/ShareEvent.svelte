<script lang="ts">
  import { eventQR } from '../lib/qr';
  let { url, publicID }: { url: string; publicID: string } = $props();
  let qr = $state(''); let error = $state(''); let notice = $state('');
  let canvas = $state<HTMLCanvasElement>();
  $effect(() => {
    let current = true; qr = ''; error = '';
    if (canvas) void eventQR(url, canvas).then(value => { if (current) qr = value; }).catch(() => { if (current) error = 'QR could not be generated. You can still copy the public link.'; });
    return () => { current = false; };
  });
  async function copy() {
    try { await navigator.clipboard.writeText(url); notice = 'Public link copied.'; }
    catch { notice = 'Select the public link below and copy it.'; }
  }
</script>
<section class="section-card share-panel" aria-labelledby="share-heading">
  <h2 id="share-heading">Share event</h2>
  <p class="section-intro">Send this link to guests, or print the QR code so they can scan and share.</p>
  <div class="share-grid"><div class="share-copy">
    <label for="public-url">Public link</label><input id="public-url" readonly value={url} />
    <div class="actions"><button type="button" class="secondary" onclick={copy}>Copy link</button><a href={url} target="_blank" rel="noopener noreferrer">Visit guest page ↗</a></div>
    {#if notice}<p role="status" class="notice">{notice}</p>{/if}
    <p class="hint">Closing the event keeps this link and QR code unchanged.</p>
  </div><div class="qr-panel">
    <div role="img" aria-label="Scan this QR code to open this event's public media-sharing page"><canvas class="event-qr" bind:this={canvas} aria-hidden="true"></canvas></div>
    {#if qr}
      <a class="button secondary" href={qr} download={`photodrop-${publicID}-qr.png`}>Download QR</a>
    {:else if error}<p class="error" role="alert">{error}</p>{:else}<p role="status">Creating QR…</p>{/if}
  </div></div>
</section>
