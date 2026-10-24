import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';

// Chart.js defaults live in lib/chartjs.ts, loaded on demand by chart components.

const app = mount(App, {
  target: document.getElementById('app')!,
});

export default app;

// Older builds registered /graph-sw.js to cache the removed Full Graph
// endpoint. Unregister any leftover registration so its stale cache goes away.
if ('serviceWorker' in navigator) {
  navigator.serviceWorker
    .getRegistrations()
    .then((regs) => {
      for (const r of regs) {
        const script = (r.active || r.waiting || r.installing)?.scriptURL || '';
        if (script.endsWith('/graph-sw.js')) r.unregister();
      }
    })
    .catch(() => {
      // Cleanup failure is non-critical
    });
}
