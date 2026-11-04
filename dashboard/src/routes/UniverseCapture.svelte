<script lang="ts">
  // Bare Universe map for the PDF report: headless Chrome opens
  // #/capture/universe/{id}?layout=&color=&highlight=&all= and takes a
  // screenshot once <body data-capture> is "ready" (or "error").
  import { onMount } from 'svelte';
  import type { ColorMode, UniverseMeta } from '../lib/components/results/universe/engine.js';

  let { id, params }: { id: string; params: Record<string, string> } = $props();
  let host: HTMLDivElement;
  const noChips = $derived(params.chips === '0');

  function done(state: 'ready' | 'error', msg = '') {
    document.body.dataset.capture = state;
    if (msg) document.body.dataset.captureError = msg;
  }

  onMount(() => {
    let engine: { dispose(): void } | null = null;
    (async () => {
      try {
        const layout = params.layout === 'folders' ? 'folders' : 'links';
        const all = params.all === '1' ? '&all=1' : '';
        const base = `/api/crawl/${encodeURIComponent(id)}/universe`;
        let res = await fetch(`${base}?dim=auto&layout=${layout}${all}`);
        for (let tries = 0; res.status === 503 && tries < 60; tries++) {
          await new Promise((r) => setTimeout(r, 1500));
          res = await fetch(`${base}?dim=auto&layout=${layout}${all}`);
        }
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const m: UniverseMeta = await res.json();
        const { UniverseEngine, clusterColor } = await import('../lib/components/results/universe/engine.js');
        // What the report prints next to the map.
        document.body.dataset.captureMeta = JSON.stringify({
          pages: m.total, clusters: m.clusters.length, links: m.edgesTotal, unreached: m.unreached, dim: m.dim,
          top: m.clusters.slice(0, 6).map((c, i) => ({ label: c.label, count: c.count, color: clusterColor(i) })),
        });
        const q = `dim=${m.dim}&layout=${m.layout}${m.all ? '&all=1' : ''}`;
        let finished: () => void = () => {};
        const loaded = new Promise<void>((r) => (finished = r));
        const e = new UniverseEngine(host, m, {
          nodes: (o, l) => `${base}/nodes?${q}&offset=${o}&limit=${l}`,
          metric: (name, n) => `${base}/metric?${q}&name=${name}&n=${n}`,
          edges: (x) => `${base}/edges?${q}&n=${x.n}&offset=${x.offset}&limit=${x.limit}&per=${x.per}${x.c !== undefined ? `&c=${x.c}` : ''}${x.sitewide ? '&sitewide=1' : ''}${x.tree ? '&tree=1' : ''}`,
          nodeLinks: (i) => `${base}/node-links?${q}&i=${i}`,
        }, {
          onFocus: () => {},
          onSelect: () => {},
          onProgress: (p) => { if (p.phase === 'done') finished(); },
        }, { panelPx: 0, zoom: Number(params.zoom) || 1 });
        engine = e;
        const hl = Number(params.highlight) || 0;
        if (hl >= 1 && hl <= 4) e.setHighlight(hl as 1 | 2 | 3 | 4);
        await e.load();
        await loaded;
        const color = params.color as ColorMode;
        if (color && color !== 'cluster') await e.setColorMode(color);
        e.request();
        // Two frames so the last colour and labels are on screen.
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
        await new Promise((r) => setTimeout(r, 400));
        done('ready');
      } catch (err) {
        done('error', (err as Error).message);
      }
    })();
    return () => engine?.dispose();
  });
</script>

<div bind:this={host} class="capture" class:no-chips={noChips}></div>

<style>
  :global(body) { background: #101112; overflow: hidden; }
  .capture { position: fixed; inset: 0; isolation: isolate; }
  :global(.uv-dot) { width: 8px; height: 8px; border-radius: 50%; flex: none; display: inline-block; }
  :global(.uv-chip) { display: flex; align-items: center; gap: 7px; padding: 5px 10px 5px 8px; border-radius: 999px; background: #171819e6; border: 1px solid var(--color-border); font-size: 11.5px; color: var(--color-fg-1); white-space: nowrap; pointer-events: none; }
  :global(.uv-chip b) { font-weight: 500; color: var(--color-fg-2); font-variant-numeric: tabular-nums; }
  :global(.uv-chip.uv-hide) { opacity: 0; }
  .no-chips :global(.uv-chip) { display: none; }
  :global(.uv-chip.uv-mono .uv-dot) { visibility: hidden; }
</style>
