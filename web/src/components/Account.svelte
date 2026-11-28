<script lang="ts">
  import { onMount } from 'svelte';
  import { request, message } from '../lib/api';
  import { linkResultMessage, targetReasonMessage, type Destinations, type DestinationAccount } from '../lib/destinations';
  let { name }: { name: string } = $props();
  let data = $state<Destinations | null>(null); let loading = $state(true); let error = $state(''); let busy = $state('');
  let confirming = $state<{ account: DestinationAccount; action: 'unlink' | 'forget' } | null>(null);
  let forgotten = $state('');
  const result = linkResultMessage(new URLSearchParams(window.location.search).get('result'));
  async function load() {
    loading = true; error = '';
    try { data = await request<Destinations>('/api/admin/destinations'); }
    catch (cause) { error = message(cause); } finally { loading = false; }
  }
  async function link(target: string) {
    busy = target; error = '';
    try { window.location.assign((await request<{ location: string }>('/api/admin/destinations/immich/link', 'POST', { target })).location); }
    catch (cause) { error = message(cause); busy = ''; }
  }
  async function confirm() {
    if (!confirming) return;
    const { account, action } = confirming; busy = account.target; error = '';
    try {
      const out = await request<{ location?: string; key_name?: string }>(`/api/admin/destinations/${account.id}/${action}`, 'POST', {});
      if (out.location) { window.location.assign(out.location); return; }
      forgotten = out.key_name ?? ''; confirming = null; await load();
    } catch (cause) { error = message(cause); } finally { busy = ''; }
  }
  const accountFor = (target: string) => data?.accounts.find(one => one.target === target);
  onMount(() => { void load(); if (result) history.replaceState(null, '', '/admin/account'); });
</script>
<svelte:head><title>Account · PhotoDrop Admin</title></svelte:head>
<main class="workspace">
  <div class="page-heading"><div><p class="eyebrow">Account</p><h1>{name}</h1></div></div>
  {#if result}<p class={result.ok ? 'notice' : 'error'} role="status">{result.text}</p>{/if}
  {#if forgotten}<p class="notice" role="status">PhotoDrop forgot the key. Delete it in Immich under Account Settings → API Keys: <strong>{forgotten}</strong></p>{/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <section class="section-card" aria-labelledby="immich-accounts"><h2 id="immich-accounts">Immich</h2>
    <p class="section-intro">Your events' albums and photos go to your own Immich account.</p>
    {#if loading}<p role="status">Loading…</p>
    {:else if data && data.targets.length === 0}<p class="hint">This deployment has no Immich server configured.</p>
    {:else if data}
      <ul class="host-list">{#each data.targets as target (target.key)}
        {@const account = accountFor(target.key)}
        <li class="section-card"><div class="host-row"><div>
          <h3>{target.key}</h3>
          {#if account}
            <p class="muted">{account.source === 'config' ? 'API key from the deployment configuration' : `Linked${account.remote_email ? ` as ${account.remote_email}` : ''}`}{account.status === 'needs_relink' ? ' · needs relinking' : ''}</p>
            {#if account.status_reason}<p class="error">{account.status_reason}</p>{/if}
          {:else if !target.linkable}<p class="hint">{targetReasonMessage(target.reason)}</p>
          {:else}<p class="hint">You'll sign in to Immich. PhotoDrop creates an API key in your Immich account that can only create albums and upload photos.</p>{/if}
        </div><div class="host-actions">
          {#if account?.source === 'linked'}
            <button class="secondary" disabled={!!busy || !target.linkable} onclick={() => link(target.key)}>{busy === target.key ? 'Opening Immich…' : 'Relink'}</button>
            <button class="secondary danger" disabled={!!busy} onclick={() => confirming = { account, action: 'unlink' }}>Unlink</button>
          {:else if !account && target.linkable}
            <button disabled={!!busy} onclick={() => link(target.key)}>{busy === target.key ? 'Opening Immich…' : 'Link Immich'}</button>
          {/if}
        </div></div>
        {#if confirming && confirming.account.id === account?.id}
          <div class="delete-panel" role="group" aria-label="Confirm">
            {#if confirming.action === 'unlink'}
              <p>Unlink this Immich account? You'll sign in to Immich once more so PhotoDrop can delete its API key there. Events that already use it keep their albums, but can't import until you link again.</p>
              <div class="actions"><button class="danger" disabled={!!busy} onclick={confirm}>Unlink</button><button class="secondary" onclick={() => confirming = { account: confirming!.account, action: 'forget' }}>Can't sign in to Immich?</button><button class="secondary" onclick={() => confirming = null}>Keep linked</button></div>
            {:else}
              <p>Forget this account without contacting Immich? Its API key stays in Immich until you delete it there yourself.</p>
              <div class="actions"><button class="danger" disabled={!!busy} onclick={confirm}>Forget</button><button class="secondary" onclick={() => confirming = null}>Cancel</button></div>
            {/if}
          </div>
        {/if}
        </li>
      {/each}</ul>
      <button class="text-button" onclick={load}>Refresh</button>
    {/if}
  </section>
</main>
