<script lang="ts">
  import { onMount } from 'svelte';
  import EventList from './EventList.svelte'; import EventEditor from './EventEditor.svelte'; import Hosts from './Hosts.svelte'; import Account from './Account.svelte';
  import { loadSession, request, message, type SessionHost } from '../lib/api';
  let { path }: { path: string } = $props();
  let ready = $state(false); let error = $state(''); let signingOut = $state(false);
  let host = $state<SessionHost | null>(null);
  onMount(() => { loadSession().then(session => { host = session.host; ready = true; }).catch(cause => error = message(cause)); });
  async function logout() {
    signingOut = true;
    try { await request('/api/admin/logout', 'POST'); window.location.assign('/admin/login'); }
    catch (cause) { error = message(cause); signingOut = false; }
  }
</script>
{#if ready}
  <nav class="admin-nav" aria-label="Administrator"><div class="nav-links"><a href="/admin">Events</a>{#if host?.role === 'admin'}<a href="/admin/hosts">Hosts</a>{/if}<a href="/admin/account">Account</a></div><div class="nav-account"><span class="muted">Signed in as <strong>{host?.display_name}</strong></span><button class="text-button" disabled={signingOut} onclick={logout}>{signingOut ? 'Signing out…' : 'Sign out'}</button></div></nav>
  {#if error}<p class="error workspace" role="alert">{error}</p>{/if}
  {#if path === '/admin' || path === '/admin/'}<EventList admin={host?.role === 'admin'} />
  {:else if path === '/admin/hosts'}<Hosts self={host?.id ?? 0} admin={host?.role === 'admin'} />
  {:else if path === '/admin/account'}<Account name={host?.display_name ?? ''} />
  {:else}<EventEditor id={path === '/admin/events/new' ? null : path.split('/').pop() ?? null} admin={host?.role === 'admin'} />{/if}
{:else}<main class="workspace">{#if error}<p class="error" role="alert">{error}</p><a href="/admin/login">Sign in</a>{:else}<p role="status">Checking session…</p>{/if}</main>{/if}
