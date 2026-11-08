import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

const apiTarget = process.env.VITE_API_PROXY_TARGET || 'http://localhost:3000';

export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte(),
  ],
  build: {
    outDir: '../dist/dashboard',
    emptyOutDir: true,
  },
  server: {
    // changeOrigin: false keeps the browser's Host (this dev server), so
    // the Go server sees Origin and Host match and treats the request as
    // its own origin. It refuses pages from other ports of this machine.
    proxy: {
      '/api': { target: apiTarget, changeOrigin: false },
      '/login': { target: apiTarget, changeOrigin: false },
      '/ws': {
        target: apiTarget.replace(/^http/, 'ws'),
        ws: true,
        changeOrigin: false,
      },
    },
  },
});
