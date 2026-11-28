<script lang="ts">
  import { onMount } from 'svelte';
  import { request, message, type HostRecord } from '../lib/api';
  let { self, admin }: { self: number; admin: boolean } = $props();
  let hosts = $state<HostRecord[]>([]); let loading = $state(true); let error = $state(''); let busy = $state(0);
  async function load() {
    loading = true; error = '';
    try { hosts = (await request<{ hosts: HostRecord[] }>('/api/admin/hosts')).hosts; }
    catch (cause) { error = message(cause); } finally { loading = false; }
  }
  async function toggle(host: HostRecord) {
    busy = host.id; error = '';
    try { await request(`/api/admin/hosts/${host.id}`, 'PUT', { disabled: !host.disabled }); await load(); }
    catch (cause) { error = message(cause); } finally { busy = 0; }
  }
  const signIn = (host: HostRecord) => [host.oidc ? 'SSO' : '', host.password ? 'Password' : ''].filter(Boolean).join(' + ') || 'Not signed in yet';
  const details = (host: HostRecord) => [host.role === 'admin' ? 'Administrator' : 'Host', host.email, signIn(host)].filter(Boolean).join(' · ');
  const when = (value: string | null) => value ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) : 'Never';
  onMount(() => { if (admin) void load(); else loading = false; });
</script>
<svelte:head><title>Hosts · PhotoDrop Admin</title></svelte:head>
<main class="workspace">
  <div class="page-heading"><div><p class="eyebrow">Administration</p><h1>Hosts</h1></div></div>
  {#if !admin}<p class="error" role="alert">Only an administrator can manage hosts.</p>
  {:else}
    <p class="muted">Hosts come from your identity provider's groups. Disabling a host signs them out at once and blocks sign-in; their events stay as they are.</p>
    {#if loading}<p role="status">Loading hosts…</p>
    {:else}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <ul class="host-list">{#each hosts as host (host.id)}
        <li class="section-card">
          <div class="host-row"><div><h2>{host.display_name}{#if host.id === self}<span class="muted">{' (you)'}</span>{/if}</h2>
            <p class="muted">{details(host)}</p>
            <p class="hint">Last sign-in: {when(host.last_login_at)}</p></div>
            <div class="host-actions">{#if host.disabled}<span class="badge">Disabled</span>{/if}
              {#if host.id !== self}<button class="secondary" class:danger={!host.disabled} disabled={busy !== 0} onclick={() => toggle(host)}>{busy === host.id ? 'Saving…' : host.disabled ? 'Enable' : 'Disable'}<span class="sr-only"> {host.display_name}</span></button>{/if}</div>
          </div>
        </li>
      {/each}</ul>
      <button class="text-button" onclick={load}>Refresh hosts</button>
    {/if}
  {/if}
</main>
