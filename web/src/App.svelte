<script lang="ts">
  import Login from './components/Login.svelte';
  import Admin from './components/Admin.svelte';
  import Guest from './components/Guest.svelte';
  const path = window.location.pathname;
</script>

<div class="page">
  <header class="site-header"><a class="brand" href="/" aria-label="PhotoDrop home"><img src="/favicon.svg" width="32" height="32" alt="" /><span>PhotoDrop</span></a>{#if path.startsWith('/admin')}<span class="muted">Administration</span>{/if}</header>
  {#if path === '/admin/login'}<Login />
  {:else if path === '/admin' || path === '/admin/' || /^\/admin\/events\/[^/]+$/.test(path)}<Admin {path} />
  {:else if /^\/e\/[^/]+$/.test(path)}<Guest publicID={path.split('/')[2]} />
  {:else if path === '/'}
    <main class="home-content">
      <img class="project-mark" src="/favicon.svg" width="64" height="64" alt="" />
      <p class="eyebrow">A place for shared moments</p><h1>PhotoDrop</h1>
      <p class="description">Self-hosted event media collection.</p>
      <p class="status"><span class="status-dot" aria-hidden="true"></span>Your event. Everyone’s memories.</p>
      <p class="note">Create an event and invite guests to share their files.</p>
      <a class="button" href="/admin">Manage events</a>
    </main>
  {:else}<main class="guest-page"><h1>Page not found</h1><a href="/">Return to PhotoDrop</a></main>{/if}
  <footer><span>PhotoDrop · Shared moments</span>{#if path === '/'}<a href="/healthz">Health endpoint ↗</a>{/if}</footer>
</div>
