<script lang="ts">
  // Small 2D preview of Universe for the Overview: the top pages by PageRank
  // and their link tree, drawn once on a canvas. A click opens the full map.
  import { onMount } from 'svelte';
  import { GRAPH_PALETTE, STATUS_LINK_COLORS } from '../../palette';

  let { crawlId, onOpen }: { crawlId: string; onOpen: () => void } = $props();

  const MAX_NODES = 4000;
  let canvas = $state<HTMLCanvasElement | null>(null);
  let info = $state<{ total: number; clusters: number; layout: string; errors: number; redirects: number; blocked: number } | null>(null);
  let failed = $state(false);

  async function getOk(url: string): Promise<Response> {
    // Right after a restart the pages load in the background (503 until ready).
    for (let tries = 0; ; tries++) {
      const res = await fetch(url);
      if (res.status !== 503 || tries >= 40) {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res;
      }
      await new Promise((r) => setTimeout(r, 1500));
    }
  }

  onMount(() => {
    let cancelled = false;
    (async () => {
      const base = `/api/crawl/${encodeURIComponent(crawlId)}/universe`;
      const meta = await (await getOk(`${base}?dim=auto`)).json();
      const q = `dim=${meta.dim}&layout=${meta.layout}`;
      const n = Math.min(meta.total, MAX_NODES);
      const nodes = await (await getOk(`${base}/nodes?${q}&offset=0&limit=${n}`)).arrayBuffer();
      let pairs: Uint32Array | null = null;
      if (meta.hasGraph && meta.layout === 'links') {
        const buf = await (await getOk(`${base}/edges?${q}&n=${n}&offset=0&limit=${n}&per=1&sitewide=1&tree=1`)).arrayBuffer();
        pairs = new Uint32Array(buf, 16, new DataView(buf).getUint32(8, true) * 2);
      }
      if (cancelled) return;
      const h = meta.hidden ?? { errors: 0, redirects: 0, blocked: 0 };
      info = { total: meta.total, clusters: meta.clusters.length, layout: meta.layout, errors: h.errors, redirects: h.redirects, blocked: h.blocked };
      draw(nodes, pairs);
    })().catch(() => { if (!cancelled) failed = true; });
    return () => { cancelled = true; };
  });

  function draw(buf: ArrayBuffer, pairs: Uint32Array | null) {
    if (!canvas) return;
    const dv = new DataView(buf);
    const count = dv.getUint32(8, true);
    const pos = new Int16Array(buf, 16, count * 3);
    const cl = new Uint16Array(buf, 16 + count * 6, count);
    const rank = new Uint8Array(buf, 16 + count * 8, count);
    const dpr = window.devicePixelRatio || 1;
    const w = canvas.clientWidth, hgt = canvas.clientHeight;
    canvas.width = w * dpr;
    canvas.height = hgt * dpr;
    const ctx = canvas.getContext('2d')!;
    ctx.scale(dpr, dpr);
    // Fit x/y (the view Universe opens with) into the canvas.
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (let i = 0; i < count; i++) {
      minX = Math.min(minX, pos[i * 3]); maxX = Math.max(maxX, pos[i * 3]);
      minY = Math.min(minY, pos[i * 3 + 1]); maxY = Math.max(maxY, pos[i * 3 + 1]);
    }
    const pad = 10;
    const s = Math.min((w - 2 * pad) / Math.max(1, maxX - minX), (hgt - 2 * pad) / Math.max(1, maxY - minY));
    const ox = (w - (maxX - minX) * s) / 2, oy = (hgt - (maxY - minY) * s) / 2;
    const px = (i: number) => ox + (pos[i * 3] - minX) * s;
    const py = (i: number) => hgt - (oy + (pos[i * 3 + 1] - minY) * s); // y up, as in the 3D view
    const color = (i: number) => GRAPH_PALETTE[cl[i] % GRAPH_PALETTE.length];
    if (pairs) {
      ctx.lineWidth = 0.6;
      ctx.globalAlpha = count > 1500 ? 0.18 : 0.3;
      for (let k = 0; k < pairs.length; k += 2) {
        const a = pairs[k], b = pairs[k + 1];
        if (a >= count || b >= count) continue;
        ctx.strokeStyle = color(b);
        ctx.beginPath();
        ctx.moveTo(px(a), py(a));
        ctx.lineTo(px(b), py(b));
        ctx.stroke();
      }
    }
    ctx.globalAlpha = 0.9;
    for (let i = count - 1; i >= 0; i--) { // biggest pages last, on top
      ctx.fillStyle = color(i);
      ctx.beginPath();
      ctx.arc(px(i), py(i), 0.9 + (rank[i] / 255) * 3, 0, Math.PI * 2);
      ctx.fill();
    }
  }
</script>

<button type="button" onclick={onOpen}
  class="group flex w-full flex-col gap-4 rounded-md border border-border bg-surface-2 p-5 text-left transition-colors hover:border-fg-2/40 md:flex-row"
  aria-label="Open the Universe map of the site's architecture">
  <div class="relative h-48 w-full shrink-0 overflow-hidden rounded bg-[#111214] md:w-80">
    <canvas bind:this={canvas} class="h-full w-full"></canvas>
    {#if failed}
      <div class="absolute inset-0 grid place-items-center text-xs text-fg-2">Map unavailable</div>
    {:else if !info}
      <div class="absolute inset-0 grid place-items-center text-xs text-fg-2">Drawing the map…</div>
    {/if}
  </div>
  <div class="flex min-w-0 flex-1 flex-col justify-between gap-3">
    <div>
      <div class="text-xs uppercase tracking-wider text-fg-2">Site architecture</div>
      <div class="mt-1 text-sm text-fg-1">
        {#if info}
          {info.total.toLocaleString()} pages in {info.clusters} clusters{info.layout === 'links' ? ', each hanging from the page that links to it' : ''}.
        {:else}
          How the site's pages link together.
        {/if}
      </div>
      {#if info && info.errors + info.redirects + info.blocked > 0}
        <div class="mt-3 flex flex-wrap gap-3 text-xs text-fg-2">
          {#if info.errors}<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{STATUS_LINK_COLORS.error}"></span>{info.errors.toLocaleString()} errors</span>{/if}
          {#if info.redirects}<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{STATUS_LINK_COLORS.redirect}"></span>{info.redirects.toLocaleString()} off-site redirects</span>{/if}
          {#if info.blocked}<span class="flex items-center gap-1.5"><span class="h-2 w-2 rounded-full" style="background:{STATUS_LINK_COLORS.blocked}"></span>{info.blocked.toLocaleString()} blocked by robots.txt</span>{/if}
          <span>not drawn by default</span>
        </div>
      {/if}
    </div>
    <span class="text-sm font-medium text-accent group-hover:underline">Open Universe →</span>
  </div>
</button>
