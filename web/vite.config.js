import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: {
    // Preserve the original Host so same-origin checks work through Vite.
    proxy: { '/healthz': 'http://127.0.0.1:8080', '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
});
