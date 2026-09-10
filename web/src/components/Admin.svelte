<script lang="ts">
  import { onMount } from 'svelte';
  import EventList from './EventList.svelte'; import EventEditor from './EventEditor.svelte';
  import { loadSession, request, message } from '../lib/api';
  let { path }: { path: string } = $props();
  let ready = $state(false); let error = $state(''); let signingOut = $state(false);
  onMount(() => { loadSession().then(() => ready = true).catch(cause => error = message(cause)); });
  async function logout() {
    signingOut = true;
    try { await request('/api/admin/logout', 'POST'); window.location.assign('/admin/login'); }
    catch (cause) { error = message(cause); signingOut = false; }
  }
</script>
{#if ready}
  <nav class="admin-nav" aria-label="Administrator"><a href="/admin">Events</a><button class="text-button" disabled={signingOut} onclick={logout}>{signingOut ? 'Signing out…' : 'Sign out'}</button></nav>
  {#if error}<p class="error workspace" role="alert">{error}</p>{/if}
  {#if path === '/admin' || path === '/admin/'}<EventList />{:else}<EventEditor id={path === '/admin/events/new' ? null : path.split('/').pop() ?? null} />{/if}
{:else}<main class="workspace">{#if error}<p class="error" role="alert">{error}</p><a href="/admin/login">Sign in</a>{:else}<p role="status">Checking session…</p>{/if}</main>{/if}
