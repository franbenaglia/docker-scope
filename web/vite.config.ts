import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// En desarrollo, Vite sirve el panel y reenvía /api al agente Go (make dev).
// changeOrigin: el agente valida Host (anti DNS rebinding) y sólo acepta el
// suyo; el Origin de Vite se permite con -allow-origin (ver Makefile).
export default defineConfig({
  plugins: [svelte()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
  },
});
