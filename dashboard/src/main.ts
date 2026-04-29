import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';

const app = mount(App, {
  target: document.getElementById('app')!,
});

export default app;

// Register service worker for graph data caching
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/graph-sw.js').catch(() => {
    // SW registration failure is non-critical
  });
}
