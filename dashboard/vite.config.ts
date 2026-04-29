import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte(),
  ],
  build: {
    outDir: '../dist/dashboard',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          sigma: ['sigma', 'graphology', 'graphology-layout-forceatlas2'],
          cosmos: ['@cosmos.gl/graph'],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:3000',
      '/ws': {
        target: 'ws://localhost:3000',
        ws: true,
      },
    },
  },
});
