<script lang="ts">
  import { onMount } from 'svelte';
  import { request, message } from '../lib/api';
  import { loginError, type AuthMethods } from '../lib/auth';
  let password = $state(''); let busy = $state(false); let error = $state('');
  let methods = $state<AuthMethods>(); let loading = $state(true);
  async function loadMethods() {
    loading = true; error = loginError(window.location.search);
    try { methods = await request<AuthMethods>('/api/admin/auth/methods'); }
    catch { error = 'Sign-in options could not be loaded. Please retry.'; }
    finally { loading = false; }
  }
  onMount(loadMethods);
  async function signIn(event: SubmitEvent) {
    event.preventDefault(); busy = true; error = '';
    try { await request('/api/admin/login', 'POST', { password }); password = ''; window.location.assign('/admin'); }
    catch (cause) { error = message(cause); password = ''; busy = false; }
  }
</script>
<svelte:head><title>Sign in · PhotoDrop</title></svelte:head>
<main class="login-panel">
  <p class="eyebrow">Administration</p><h1>PhotoDrop Admin</h1><p class="muted">Sign in to manage your events.</p>
  {#if error}<p id="login-error" class="error" role="alert">{error}</p>{/if}
  {#if loading}<p role="status">Loading sign-in options…</p>
  {:else if !methods}<button onclick={loadMethods}>Retry</button>
  {:else}
  {#if methods.oidc}<a class="button" href="/api/admin/oidc/login">Sign in with SSO</a>{/if}
  {#if methods.password}<form onsubmit={signIn}>
    <label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required bind:value={password} disabled={busy} aria-invalid={!!error} aria-describedby={error ? 'login-error' : undefined} />
    <button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign In'}</button>
  </form>{/if}
  {/if}
</main>
