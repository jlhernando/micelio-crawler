<script lang="ts">
  import { Monitor, Laptop, Tablet, Smartphone, Bot, RotateCw, SlidersHorizontal, Globe } from 'lucide-svelte';
  import { DEVICES, deviceViewport, type DeviceChoice } from '../devices.js';

  let {
    device = $bindable(null),
    compareDevice = $bindable(null),
    jsRendering = false,
    uaOverride = '',
  }: { device: DeviceChoice | null; compareDevice?: DeviceChoice | null; jsRendering?: boolean; uaOverride?: string } = $props();

  // The other side of a comparison: desktop for a phone or tablet, else a phone.
  function counterpart(d: DeviceChoice | null): string {
    const kind = d ? DEVICES.find((x) => x.id === d.id)?.kind ?? (d.mobile ? 'mobile' : 'desktop') : 'desktop';
    if (d?.id === 'googlebot-smartphone') return 'googlebot-desktop';
    if (d?.id === 'googlebot-desktop') return 'googlebot-smartphone';
    return kind === 'desktop' ? 'mobile' : 'desktop';
  }
  function toggleCompare(on: boolean) {
    compareDevice = on ? { id: counterpart(device) } : null;
  }

  const ICONS = {
    desktop: Monitor, laptop: Laptop, tablet: Tablet, mobile: Smartphone, iphone: Smartphone,
    'googlebot-smartphone': Bot, 'googlebot-desktop': Bot, bingbot: Bot,
  } as Record<string, typeof Monitor>;
  const ROTATABLE = new Set(['tablet', 'mobile', 'iphone', 'googlebot-smartphone']);

  let custom = $state({ width: 412, height: 915, scale: 2, mobile: true });

  function pick(id: string | null) {
    if (id === null) device = null;
    else if (id === 'custom') device = { id: 'custom', ...custom, touch: custom.mobile };
    else device = { id, landscape: device?.id === id ? device.landscape : false };
  }
  function rotate(e: Event, id: string) {
    e.stopPropagation();
    device = { id, landscape: !(device?.id === id && device.landscape) };
  }
  function setCustom() {
    custom.width = Math.max(200, Math.min(7680, Math.round(custom.width || 0)));
    custom.height = Math.max(200, Math.min(7680, Math.round(custom.height || 0)));
    custom.scale = Math.max(1, Math.min(4, custom.scale || 1));
    device = { id: 'custom', ...custom, touch: custom.mobile };
  }

  let selected = $derived(device?.id ? DEVICES.find((d) => d.id === device!.id) : undefined);
  let ua = $derived(uaOverride || (device?.id === 'custom' ? '' : selected?.userAgent) || 'Micelio/1.0');
  let size = $derived(device ? deviceViewport(device) : { width: 1920, height: 1080 });
</script>

<div class="space-y-3" data-testid="device-picker">
  <div class="flex items-baseline justify-between">
    <h2 class="text-sm font-medium">Device</h2>
    <span class="text-xs text-fg-2">What the crawler pretends to be</span>
  </div>
  <div class="grid grid-cols-2 gap-2 sm:grid-cols-5">
    <button type="button" aria-pressed={device === null} onclick={() => pick(null)}
      class="dp-card {device === null ? 'on' : ''}">
      <Globe size={20} strokeWidth={1.5} />
      <span class="dp-name">Micelio bot</span>
      <span class="dp-size">default</span>
    </button>
    {#each DEVICES as d (d.id)}
      {@const Icon = ICONS[d.id] ?? Monitor}
      {@const on = device?.id === d.id}
      {@const land = on && !!device?.landscape}
      <div class="relative">
        <button type="button" aria-pressed={on} onclick={() => pick(d.id)} class="dp-card w-full {on ? 'on' : ''}" title={d.userAgent}>
          <span class="relative inline-flex">
            <span class="inline-flex transition-transform {land ? 'rotate-90' : ''}"><Icon size={20} strokeWidth={1.5} /></span>
            {#if d.id.startsWith('googlebot') || d.id === 'bingbot'}
              <span class="dp-kind">{d.kind === 'mobile' ? 'M' : 'D'}</span>
            {/if}
          </span>
          <span class="dp-name">{d.label}</span>
          <span class="dp-size">{on ? `${size.width}×${size.height}` : `${d.width}×${d.height}`}</span>
        </button>
        {#if ROTATABLE.has(d.id) && on}
          <button type="button" aria-label={land ? 'Turn to portrait' : 'Turn to landscape'} title={land ? 'Turn to portrait' : 'Turn to landscape'}
            class="dp-rotate" onclick={(e) => rotate(e, d.id)}>
            <RotateCw size={13} />
          </button>
        {/if}
      </div>
    {/each}
    <button type="button" aria-pressed={device?.id === 'custom'} onclick={() => pick('custom')}
      class="dp-card {device?.id === 'custom' ? 'on' : ''}">
      <SlidersHorizontal size={20} strokeWidth={1.5} />
      <span class="dp-name">Custom</span>
      <span class="dp-size">{device?.id === 'custom' ? `${size.width}×${size.height}` : 'your size'}</span>
    </button>
  </div>

  {#if device?.id === 'custom'}
    <div class="flex flex-wrap items-end gap-3 text-xs">
      <label class="flex flex-col gap-1 text-fg-2">Width
        <input type="number" min="200" max="7680" bind:value={custom.width} onchange={setCustom} class="dp-input w-24" /></label>
      <label class="flex flex-col gap-1 text-fg-2">Height
        <input type="number" min="200" max="7680" bind:value={custom.height} onchange={setCustom} class="dp-input w-24" /></label>
      <label class="flex flex-col gap-1 text-fg-2">Pixel ratio
        <input type="number" min="1" max="4" step="0.125" bind:value={custom.scale} onchange={setCustom} class="dp-input w-20" /></label>
      <label class="flex items-center gap-2 pb-2 text-fg-2"><input type="checkbox" bind:checked={custom.mobile} onchange={setCustom} /> Mobile + touch</label>
    </div>
  {/if}

  <div class="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-surface px-3 py-2 text-sm">
    <label class="flex items-center gap-2 cursor-pointer">
      <input type="checkbox" checked={!!compareDevice} onchange={(e) => toggleCompare((e.target as HTMLInputElement).checked)} class="rounded accent-accent" />
      Compare with another device
    </label>
    {#if compareDevice}
      <select aria-label="Compare device" value={compareDevice.id} onchange={(e) => (compareDevice = { id: (e.target as HTMLSelectElement).value })}
        class="px-2 py-1 rounded-md border border-border bg-surface-2 text-fg text-sm">
        {#each DEVICES.filter((d) => d.id !== device?.id) as d}
          <option value={d.id}>{d.label} · {d.width}×{d.height}</option>
        {/each}
      </select>
      <span class="text-xs text-fg-2">Every page is fetched as both devices{jsRendering ? ' and rendered twice' : ''}: about 2× requests and time. Results get a Devices tab.</span>
    {/if}
  </div>

  <div class="text-xs text-fg-2 space-y-1">
    <div class="truncate"><span class="text-fg-2/70">User-Agent:</span> <span class="font-mono" title={ua}>{ua}</span>
      {#if uaOverride}<span class="text-warning"> (set in HTTP & Authentication)</span>{/if}</div>
    {#if device && !jsRendering}
      <div>Without JavaScript Rendering only the User-Agent and client hints change. Turn it on to render the page on a {size.width}×{size.height} screen.</div>
    {/if}
  </div>
</div>

<style>
  .dp-card {
    position: relative; display: flex; flex-direction: column; align-items: flex-start; gap: 6px;
    padding: 12px; border-radius: 10px; border: 1px solid var(--color-border); background: var(--color-surface-2);
    color: var(--color-fg-2); text-align: left; cursor: pointer; transition: border-color .15s, background .15s;
  }
  .dp-card:hover { border-color: var(--color-fg-2); background: var(--color-surface-3); }
  .dp-card.on { border-color: var(--color-accent); background: var(--color-surface-3); color: var(--color-fg); }
  .dp-name { font-size: 13px; font-weight: 500; color: var(--color-fg); line-height: 1.2; }
  .dp-size { font-size: 11px; font-variant-numeric: tabular-nums; }
  .dp-kind { position: absolute; right: -8px; bottom: -4px; font-size: 9px; font-weight: 700; padding: 0 3px; border-radius: 4px; background: var(--color-surface-3); border: 1px solid var(--color-border); }
  .dp-rotate { cursor: pointer; position: absolute; top: 8px; right: 8px; padding: 4px; border-radius: 6px; border: 1px solid var(--color-border); background: var(--color-surface-2); color: var(--color-fg); }
  .dp-rotate:hover { border-color: var(--color-accent); }
  .dp-input { padding: 6px 8px; border-radius: 8px; border: 1px solid var(--color-border); background: var(--color-surface); color: var(--color-fg); }
</style>
