import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';
import { Chart } from 'chart.js';

Chart.defaults.color = '#a1a5ad';
Chart.defaults.borderColor = '#303235';
Chart.defaults.font.family = '-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif';
Chart.defaults.font.size = 11;

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
