<script lang="ts">
  import { onMount } from 'svelte';
  import { loadTurnstile, type TurnstileAPI } from '../lib/turnstile';
  import { message } from '../lib/api';
  let { siteKey, action, onToken }: { siteKey: string; action: string; onToken: (token: string) => void } = $props();
  let container: HTMLDivElement;
  let error = $state('');
  let api: TurnstileAPI | undefined;
  let widget: string | undefined;
  let disposed = false;
  async function mount() {
    error = ''; onToken('');
    try {
      api = await loadTurnstile(); if (disposed) return;
      if (widget !== undefined) api.remove(widget);
      widget = api.render(container, { sitekey: siteKey, action, size: 'flexible',
        callback: (token: string) => { error = ''; onToken(token); },
        'expired-callback': () => { onToken(''); error = 'Verification expired. Please verify again.'; },
        'error-callback': () => { onToken(''); error = 'Verification could not complete. Please try again.'; },
      });
    } catch (cause) { if (!disposed) error = message(cause); }
  }
  onMount(() => { void mount(); return () => { disposed = true; if (widget !== undefined) api?.remove(widget); onToken(''); }; });
</script>
<div class="verification-panel">
  <p class="hint">Verify once to start this batch of uploads.</p>
  <div bind:this={container}></div>
  {#if error}<p class="error" role="alert">{error}</p><button type="button" class="secondary" onclick={mount}>Retry verification</button>{/if}
</div>
