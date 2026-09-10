<script lang="ts">
  import { request, message } from '../lib/api';
  let password = $state(''); let busy = $state(false); let error = $state('');
  async function signIn(event: SubmitEvent) {
    event.preventDefault(); busy = true; error = '';
    try { await request('/api/admin/login', 'POST', { password }); password = ''; window.location.assign('/admin'); }
    catch (cause) { error = message(cause); password = ''; busy = false; }
  }
</script>
<svelte:head><title>Sign in · PhotoDrop</title></svelte:head>
<main class="login-panel">
  <p class="eyebrow">Administration</p><h1>PhotoDrop Admin</h1><p class="muted">Sign in to manage your events.</p>
  <form onsubmit={signIn}>
    <label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required bind:value={password} disabled={busy} />
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign In'}</button>
  </form>
</main>
