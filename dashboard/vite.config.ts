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
        // Vite 8 (Rolldown) only accepts the function form.
        manualChunks(id) {
          if (/[\\/]node_modules[\\/](sigma|graphology|graphology-layout-forceatlas2)[\\/]/.test(id)) return 'sigma';
          if (/[\\/]node_modules[\\/]@cosmos\.gl[\\/]graph[\\/]/.test(id)) return 'cosmos';
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
