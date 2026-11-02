<script lang="ts">
  import { Monitor, Tablet, Smartphone, Bot } from 'lucide-svelte';
  import { deviceKind, deviceSummary, type DeviceChoice } from '../devices.js';

  let { device, compare = null }: { device: unknown; compare?: unknown } = $props();
  let c = $derived(compare && typeof compare === 'object' && 'id' in compare ? (compare as DeviceChoice) : null);
  let d = $derived(device && typeof device === 'object' && 'id' in device ? (device as DeviceChoice) : null);
  let kind = $derived(deviceKind(d));
  let bot = $derived(!!d && (d.id.startsWith('googlebot') || d.id === 'bingbot'));
</script>

{#if d}
  <span class="inline-flex items-center gap-1 rounded-md border border-border bg-surface-3 px-1.5 py-0.5 text-[11px] text-fg-2 whitespace-nowrap" title="Crawled as {deviceSummary(d)}">
    {#if bot}<Bot size={12} />{:else if kind === 'mobile'}<Smartphone size={12} />{:else if kind === 'tablet'}<Tablet size={12} />{:else}<Monitor size={12} />{/if}
    {deviceSummary(d)}{#if c}<span class="text-fg-2/70">vs</span> {deviceSummary(c)}{/if}
  </span>
{:else if c}
  <span class="inline-flex items-center gap-1 rounded-md border border-border bg-surface-3 px-1.5 py-0.5 text-[11px] text-fg-2 whitespace-nowrap">Micelio bot <span class="text-fg-2/70">vs</span> {deviceSummary(c)}</span>
{/if}
