<script lang="ts">
  import { onMount } from 'svelte';
  import type { UniverseEngine, UniverseMeta, UniverseCard, NodeLinks, Progress, ColorMode } from './universe/engine.js';

  let { crawlId, onOpenExplorer }: { crawlId: string; onOpenExplorer?: (url: string) => void } = $props();

  const PALETTE = ['#75a6ff', '#e5b66e', '#73d9ac', '#f2a3c7', '#b09aff', '#7fd3e6', '#c9b48a', '#aabddd'];
  const OUT_COLOR = '#f2c46d';
  const IN_COLOR = '#73d9ac';
  const DIM_LABELS: Record<string, string> = { section: 'Section', dir1: 'Folder', dir2: 'Subfolder', template: 'Template' };
  const PAGE_SIZE = 50;

  let host: HTMLDivElement;
  let engine: UniverseEngine | null = null;
  let meta = $state<UniverseMeta | null>(null);
  let dim = $state('auto');
  let error = $state('');
  let progress = $state<Progress>({ loaded: 0, target: 0, edges: 0, edgeTarget: 0, phase: 'pages', reason: '' });
  let focused = $state<number | null>(null);
  let detail = $state<NodeLinks | null>(null);
  let detailThumb = $state<string | null>(null);
  let sitewide = $state(false);
  let layout = $state<'links' | 'folders'>('links');
  let crossLinks = $state(false);
  let loadingPages = $state(false);
  let colorMode = $state<ColorMode>('cluster');
  const METRICS: [ColorMode, string][] = [['depth', 'Depth'], ['pagerank', 'PageRank'], ['authority', 'Authority'], ['hub', 'Hub']];
  const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#7d828a'];
  // Page search
  let query = $state('');
  let hits = $state<UniverseCard[]>([]);
  let searchSeq = 0;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  function onSearch() {
    clearTimeout(searchTimer);
    const q = query.trim();
    if (!q) { hits = []; return; }
    searchTimer = setTimeout(async () => {
      const seq = ++searchSeq;
      try {
        const res = await fetch(`${base}/search?dim=${dim}&layout=${layout}&q=${encodeURIComponent(q)}&limit=12`);
        const data = await res.json();
        if (seq === searchSeq) hits = data.rows ?? [];
      } catch { /* keep previous hits */ }
    }, 200);
  }
  function pickHit(h: UniverseCard) {
    query = '';
    hits = [];
    engine?.select(h.i, true);
  }
  function setColour(mode: ColorMode) {
    colorMode = mode;
    engine?.setColorMode(mode);
  }

  // Overview table (client-side sort)
  let cSort = $state<'label' | 'count' | 'prSum' | 'inAvg' | 'outAvg'>('prSum');
  let cDir = $state(-1);
  // Cluster table (server-side sort + paging)
  let pSort = $state<'pr' | 'in' | 'out' | 'title'>('pr');
  let pDir = $state<'desc' | 'asc'>('desc');
  let rows = $state<UniverseCard[]>([]);
  let rowsTotal = $state(0);
  let rowsLoading = $state(false);

  const base = $derived(`/api/crawl/${encodeURIComponent(crawlId)}/universe`);
  const sortedClusters = $derived.by(() => {
    if (!meta) return [];
    return meta.clusters.map((c, i) => ({ c, i })).sort((a, b) => {
      const x = a.c[cSort], y = b.c[cSort];
      return (typeof x === 'string' ? x.localeCompare(y as string) : (x as number) - (y as number)) * cDir;
    });
  });
  const maxPrSum = $derived(meta ? Math.max(...meta.clusters.map((c) => c.prSum), 1e-9) : 1);

  // Thumbnail of the selected page only, captured on demand by the server.
  const thumbCache = new Map<number, Promise<string | null>>();
  const objectUrls: string[] = [];
  function thumbUrl(i: number): Promise<string | null> {
    let p = thumbCache.get(i);
    if (!p) {
      p = fetch(`${base}/thumb?i=${i}&dim=${dim}&layout=${layout}`)
        .then(async (res) => {
          if (!res.ok) return null;
          const url = URL.createObjectURL(await res.blob());
          objectUrls.push(url);
          return url;
        })
        .catch(() => null);
      thumbCache.set(i, p);
    }
    return p;
  }

  // Each start/list request gets a number; answers to older ones are dropped.
  let startSeq = 0;
  let rowsSeq = 0;

  async function start(d: string) {
    const seq = ++startSeq;
    engine?.dispose();
    engine = null;
    meta = null; focused = null; detail = null; error = ''; sitewide = false; crossLinks = false; colorMode = 'cluster'; query = ''; hits = [];
    progress = { loaded: 0, target: 0, edges: 0, edgeTarget: 0, phase: 'pages', reason: '' };
    try {
      // Right after a restart the server loads the crawl's pages in the
      // background and answers 503 until they are ready: wait and retry.
      let res = await fetch(`${base}?dim=${d}&layout=${layout}`);
      for (let tries = 0; res.status === 503 && tries < 120 && seq === startSeq; tries++) {
        loadingPages = true;
        await new Promise((r) => setTimeout(r, 1500));
        res = await fetch(`${base}?dim=${d}&layout=${layout}`);
      }
      loadingPages = false;
      if (seq !== startSeq) return;
      if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || `HTTP ${res.status}`);
      const m: UniverseMeta = await res.json();
      const { UniverseEngine } = await import('./universe/engine.js');
      if (!host || seq !== startSeq) return;
      meta = m;
      dim = m.dim;
      layout = m.layout;
      const q = `dim=${m.dim}&layout=${m.layout}`;
      engine = new UniverseEngine(host, m, {
        nodes: (o, l) => `${base}/nodes?${q}&offset=${o}&limit=${l}`,
        metric: (name, n) => `${base}/metric?${q}&name=${name}&n=${n}`,
        edges: (e) => `${base}/edges?${q}&n=${e.n}&offset=${e.offset}&limit=${e.limit}&per=${e.per}${e.c !== undefined ? `&c=${e.c}` : ''}${e.sitewide ? '&sitewide=1' : ''}${e.tree ? '&tree=1' : ''}`,
        nodeLinks: (i) => `${base}/node-links?${q}&i=${i}`,
      }, {
        onFocus: (i) => { focused = i; if (i !== null) loadRows(true); },
        onSelect: (d) => openDetail(d),
        onProgress: (p) => { progress = p; },
      });
      await engine.load();
    } catch (e) {
      if ((e as Error).name !== 'AbortError' && seq === startSeq) error = (e as Error).message;
    }
  }

  async function loadRows(reset: boolean) {
    if (focused === null) return;
    const seq = ++rowsSeq;
    if (reset) { rows = []; rowsTotal = 0; }
    rowsLoading = true;
    const offset = reset ? 0 : rows.length;
    try {
      const res = await fetch(`${base}/cluster?dim=${dim}&layout=${layout}&c=${focused}&sort=${pSort}&dir=${pDir}&offset=${offset}&limit=${PAGE_SIZE}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      if (seq !== rowsSeq) return; // a newer list request won
      rows = reset ? data.rows : [...rows, ...data.rows];
      rowsTotal = data.total;
    } catch {
      if (seq === rowsSeq) rowsTotal = rows.length; // hide "Load more" after a failed page
    } finally {
      if (seq === rowsSeq) rowsLoading = false;
    }
  }

  async function openDetail(d: NodeLinks | null) {
    detail = d;
    detailThumb = null;
    if (!d) return;
    const url = await thumbUrl(d.page.i);
    if (detail?.page.i === d.page.i) detailThumb = url;
  }

  function sortClusters(k: typeof cSort) {
    if (cSort === k) cDir = -cDir; else { cSort = k; cDir = k === 'label' ? 1 : -1; }
  }
  function sortPages(k: typeof pSort) {
    if (pSort === k) pDir = pDir === 'desc' ? 'asc' : 'desc'; else { pSort = k; pDir = k === 'title' ? 'asc' : 'desc'; }
    loadRows(true);
  }
  const arrow = (on: boolean, desc: boolean) => (on ? (desc ? ' ↓' : ' ↑') : '');
  const fmt = (v: number) => (v >= 100 ? Math.round(v).toLocaleString() : v >= 10 ? v.toFixed(1) : v.toFixed(2));
  const compact = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(n >= 1e7 ? 0 : 1) + 'M' : n >= 1e4 ? Math.round(n / 1e3) + 'K' : n.toLocaleString());

  function onKey(e: KeyboardEvent) {
    if (e.key !== 'Escape') return;
    if (detail) engine?.select(null);
    else if (focused !== null) engine?.focus(null);
  }

  // Restart when the dashboard switches to another crawl without remounting.
  let startedFor = '';
  $effect(() => {
    if (crawlId === startedFor) return;
    startedFor = crawlId;
    thumbCache.clear();
    layout = 'links';
    start('auto');
  });

  onMount(() => {
    return () => {
      engine?.dispose();
      objectUrls.forEach((u) => URL.revokeObjectURL(u));
    };
  });
</script>

<svelte:window onkeydown={onKey} />

<div class="uv-root relative overflow-hidden rounded-[10px] border border-border">
  <!-- isolate: keeps the CSS2D cards and chips below the panels -->
  <div bind:this={host} class="absolute inset-0 isolate z-0"></div>

  <!-- Heading + metrics -->
  <div class="pointer-events-none absolute left-6 top-5 z-10 max-w-[46%]">
    <div class="text-[10px] font-medium uppercase tracking-[1.7px] text-[#92969e]">Link graph · Universe</div>
    {#if meta}
      <dl class="uv-metrics mt-3 {focused !== null ? 'uv-fade' : ''}">
        <div><dt>Pages</dt><dd>{compact(meta.total)}</dd></div>
        <div><dt>Shown</dt><dd>{compact(progress.loaded)}</dd></div>
        <div><dt>Clusters</dt><dd>{meta.clusters.length}</dd></div>
        <div><dt>Errors</dt><dd>{compact(meta.errors)}</dd></div>
      </dl>
      <div class="mt-3 w-[260px] text-[11px] text-fg-2 {focused !== null ? 'uv-fade' : ''}">
        {#if progress.phase === 'tree'}
          <div class="mb-1.5 flex justify-between"><span>Loading link tree…</span><span class="tabular-nums">{compact(progress.treeEdges ?? 0)}</span></div>
          <div class="h-[3px] overflow-hidden rounded bg-[#292b2e]"><div class="h-full bg-fg-1 transition-all" style="width:{progress.loaded ? ((progress.treeEdges ?? 0) / progress.loaded) * 100 : 0}%"></div></div>
        {:else if progress.phase === 'pages'}
          <div class="mb-1.5 flex justify-between"><span>Loading pages by PageRank…</span><span class="tabular-nums">{compact(progress.loaded)} / {compact(progress.target || meta.total)}</span></div>
          <div class="h-[3px] overflow-hidden rounded bg-[#292b2e]"><div class="h-full bg-fg-1 transition-all" style="width:{progress.target ? (progress.loaded / progress.target) * 100 : 0}%"></div></div>
        {:else if progress.phase === 'links'}
          <div class="mb-1.5 flex justify-between"><span>Loading real links…</span><span class="tabular-nums">{compact(progress.edges)} / {compact(progress.edgeTarget)}</span></div>
          <div class="h-[3px] overflow-hidden rounded bg-[#292b2e]"><div class="h-full bg-fg-1 transition-all" style="width:{progress.edgeTarget ? (progress.edges / progress.edgeTarget) * 100 : 0}%"></div></div>
        {:else}
          {#if progress.linkError}
            <span class="text-danger">Links could not be loaded ({progress.linkError}).</span>
          {:else if meta.hasGraph}
            {#if meta.layout === 'links'}
              Each page hangs from the page that links to it on its shortest click path ({meta.maxLevel} levels{meta.unreached ? `, ${compact(meta.unreached)} pages not reachable from the start page sit on the outer shell` : ''}).
            {/if}
            {@const avail = sitewide ? meta.edgesTotal : meta.edgesContextual}
            {#if meta.layout !== 'links' || crossLinks}
              {compact(progress.edges)} of {compact(avail)} {sitewide ? '' : 'structural '}links drawn{progress.edges < avail ? ', sampled evenly per page' : ''}.
            {/if}
          {:else}
            Link graph not available for this crawl.
          {/if}
          {#if progress.loaded < meta.total} Top {compact(progress.loaded)} pages by PageRank shown. Limited by {progress.reason}.{/if}
        {/if}
      </div>
      {#if colorMode !== 'cluster'}
        <div class="mt-2.5 flex items-center gap-2 text-[11px] text-fg-2 {focused !== null ? 'uv-fade' : ''}">
          {#if colorMode === 'depth'}
            {#each DEPTH_COLORS as c, k}
              <span class="flex items-center gap-1"><span class="uv-dot" style="background:{c}"></span>{k === 5 ? '5+' : k}</span>
            {/each}
            <span class="ml-1">clicks deep</span>
          {:else}
            <span>Low</span><span class="uv-ramp"></span><span>High {METRICS.find(([m]) => m === colorMode)?.[1]}{colorMode !== 'pagerank' ? ' (percentile)' : ''}</span>
          {/if}
        </div>
      {/if}
      {#if meta.hasGraph && meta.layout === 'links'}
        <button class="pointer-events-auto mt-2.5 flex items-center gap-2 rounded-md border border-border bg-[#171819cc] px-2.5 py-1.5 text-[11px] text-fg-2 hover:text-fg {focused !== null ? 'uv-fade' : ''}"
          title="Links that are not part of the link tree: a page's links beyond the one that first reaches each target"
          onclick={() => { crossLinks = !crossLinks; engine?.setCrossLinks(crossLinks); }}>
          <span class="uv-switch {crossLinks ? 'on' : ''}"></span>
          Cross links · {compact(Math.max(0, meta.edgesContextual - (progress.treeEdges ?? 0)))}
        </button>
      {/if}
      {#if meta.hasGraph && meta.sitewideTargets > 0 && (meta.layout !== 'links' || crossLinks)}
        <button class="pointer-events-auto mt-2.5 flex items-center gap-2 rounded-md border border-border bg-[#171819cc] px-2.5 py-1.5 text-[11px] text-fg-2 hover:text-fg {focused !== null ? 'uv-fade' : ''}"
          title="Links to the {meta.sitewideTargets.toLocaleString()} navigation pages: linked from 40%+ of the site (menus, footers) or from half of their own section (section menus, sidebars)"
          onclick={() => { sitewide = !sitewide; engine?.setSitewide(sitewide); }}>
          <span class="uv-switch {sitewide ? 'on' : ''}"></span>
          Menu, footer &amp; section nav links · {compact(meta.edgesTotal - meta.edgesContextual)}
        </button>
      {/if}
    {/if}
  </div>

  {#if error}
    <div class="absolute inset-0 z-20 grid place-items-center text-sm text-fg-2">Universe unavailable: {error}</div>
  {:else if !meta}
    <div class="absolute inset-0 z-20 grid place-items-center text-sm text-fg-2">{loadingPages ? 'Loading crawl pages…' : 'Building layout…'}</div>
  {/if}

  <!-- Side panel -->
  {#if meta}
    <aside class="uv-panel absolute bottom-4 right-4 top-4 z-10 flex w-[440px] flex-col">
      <div class="border-b border-border px-[18px] pb-3.5 pt-4">
        {#if focused === null}
          <div class="text-[10px] font-medium uppercase tracking-[1.7px] text-[#92969e]">Clusters by {DIM_LABELS[meta.dim]?.toLowerCase()}</div>
          <h2 class="mt-1.5 text-sm font-[550] tracking-[-0.2px]">{meta.clusters.length} clusters</h2>
        {:else}
          <div class="truncate text-[10px] font-medium uppercase tracking-[1.7px] text-[#92969e]">{meta.clusters[focused].key === '__other__' ? 'Smaller clusters merged' : meta.clusters[focused].key}</div>
          <h2 class="mt-1.5 flex items-center gap-2 text-sm font-[550] tracking-[-0.2px]">
            <button class="font-normal text-fg-2 hover:text-fg" onclick={() => engine?.focus(null)}>Clusters</button>
            <span class="text-[#5a5f66]">/</span><span class="truncate">{meta.clusters[focused].label}</span>
          </h2>
        {/if}
        <div class="mt-3 flex items-center gap-1">
          {#if meta.hasGraph}
            <span class="mr-1 text-[10px] uppercase tracking-[1.2px] text-[#7d828a]">Layout</span>
            {#each [['links', 'Link structure'], ['folders', 'Grouped']] as [k, label]}
              <button class="rounded-md px-[11px] py-1.5 text-xs {meta.layout === k ? 'bg-[#292b2e] text-fg' : 'text-fg-2 hover:text-fg'}"
                title={k === 'links' ? 'Each page sits next to the page that links to it on its shortest click path from the start page' : 'Pages grouped around their cluster centre'}
                onclick={() => { if (meta?.layout !== k) { layout = k as 'links' | 'folders'; start(meta!.dim); } }}>{label}</button>
            {/each}
          {/if}
        </div>
        <div class="mt-1.5 flex items-center gap-1">
          <span class="mr-1 text-[10px] uppercase tracking-[1.2px] text-[#7d828a]">{meta.layout === 'links' ? 'Colour' : 'Group'}</span>
          {#each meta.dims as d}
            {@const on = meta.dim === d && (colorMode === 'cluster' || meta.layout !== 'links')}
            <button class="rounded-md px-[11px] py-1.5 text-xs {on ? 'bg-[#292b2e] text-fg' : 'text-fg-2 hover:text-fg'}" onclick={() => (meta?.dim !== d ? start(d) : setColour('cluster'))}>{DIM_LABELS[d] ?? d}</button>
          {/each}
        </div>
        <div class="mt-1.5 flex items-center gap-1">
          <span class="mr-1 text-[10px] uppercase tracking-[1.2px] text-[#7d828a]">Metric</span>
          {#each METRICS as [m, label]}
            <button class="rounded-md px-[11px] py-1.5 text-xs {colorMode === m ? 'bg-[#292b2e] text-fg' : 'text-fg-2 hover:text-fg'}"
              onclick={() => setColour(colorMode === m ? 'cluster' : m)}>{label}</button>
          {/each}
        </div>
        <div class="relative mt-3">
          <input class="w-full rounded-md border border-border bg-[#101112] px-3 py-1.5 text-xs text-fg placeholder:text-[#6e737a] focus:border-[#5a5f66] focus:outline-none"
            placeholder="Search pages by title or URL" bind:value={query} oninput={onSearch}
            onkeydown={(e) => { if (e.key === 'Enter' && hits[0]) pickHit(hits[0]); if (e.key === 'Escape') { query = ''; hits = []; e.stopPropagation(); } }} />
          {#if hits.length}
            <div class="absolute left-0 right-0 top-full z-20 mt-1 max-h-72 overflow-auto rounded-md border border-border bg-[#171819] shadow-xl">
              {#each hits as h (h.i)}
                <button class="block w-full px-3 py-2 text-left hover:bg-[#232527]" onclick={() => pickHit(h)}>
                  <div class="truncate text-xs text-fg">{h.title}</div>
                  <div class="truncate text-[10.5px] text-fg-2">{h.url} · PR {h.pr.toFixed(2)}</div>
                </button>
              {/each}
            </div>
          {/if}
        </div>
      </div>

      <div class="min-h-0 flex-1 overflow-auto">
        {#if focused === null}
          <table class="uv-table">
            <thead><tr>
              <th class="l" onclick={() => sortClusters('label')}>Cluster{arrow(cSort === 'label', cDir < 0)}</th>
              <th class="w-[56px]" onclick={() => sortClusters('count')}>Pages{arrow(cSort === 'count', cDir < 0)}</th>
              <th class="w-[100px]" onclick={() => sortClusters('prSum')}>PR total{arrow(cSort === 'prSum', cDir < 0)}</th>
              <th class="w-[52px]" onclick={() => sortClusters('inAvg')}>In ø{arrow(cSort === 'inAvg', cDir < 0)}</th>
              <th class="w-[52px]" onclick={() => sortClusters('outAvg')}>Out ø{arrow(cSort === 'outAvg', cDir < 0)}</th>
            </tr></thead>
            <tbody>
              {#each sortedClusters as { c, i } (c.key)}
                <tr onclick={() => engine?.focus(i)}>
                  <td class="l"><div><span class="uv-dot" style="background:{PALETTE[i % 8]}"></span><span>{c.label}</span></div></td>
                  <td>{c.count.toLocaleString()}</td>
                  <td><div class="uv-prc"><span>{fmt(c.prSum)}</span><span class="uv-bar"><i style="width:{Math.max(4, (c.prSum / maxPrSum) * 100)}%;background:{PALETTE[i % 8]}"></i></span></div></td>
                  <td>{fmt(c.inAvg)}</td>
                  <td>{fmt(c.outAvg)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          {@const c = meta.clusters[focused]}
          {@const maxPr = c.top[0]?.pr || 1}
          <table class="uv-table">
            <thead><tr>
              <th class="w-[44px]" onclick={() => sortPages('pr')}>#{arrow(pSort === 'pr', pDir === 'desc')}</th>
              <th class="l" onclick={() => sortPages('title')}>Page{arrow(pSort === 'title', pDir === 'desc')}</th>
              <th class="w-[96px]" onclick={() => sortPages('pr')}>PR{arrow(pSort === 'pr', pDir === 'desc')}</th>
              <th class="w-[52px]" onclick={() => sortPages('in')}>In{arrow(pSort === 'in', pDir === 'desc')}</th>
              <th class="w-[52px]" onclick={() => sortPages('out')}>Out{arrow(pSort === 'out', pDir === 'desc')}</th>
            </tr></thead>
            <tbody>
              {#each rows as r (r.i)}
                {@const rank = c.top.findIndex((t) => t.i === r.i)}
                <tr onclick={() => engine?.select(r.i, true)} class={detail?.page.i === r.i ? 'uv-row-sel' : ''}>
                  <td class="rk {rank >= 0 ? 'top' : ''}">{rank >= 0 ? rank + 1 : '·'}</td>
                  <td class="l"><div><span class="truncate" title={r.url}>{r.title}</span></div></td>
                  <td><div class="uv-prc"><span>{r.pr.toFixed(2)}</span><span class="uv-bar"><i style="width:{Math.max(3, Math.min(100, (r.pr / maxPr) * 100))}%;background:{PALETTE[focused % 8]}"></i></span></div></td>
                  <td>{r.in.toLocaleString()}</td>
                  <td>{r.out.toLocaleString()}</td>
                </tr>
              {/each}
            </tbody>
          </table>
          {#if rows.length < rowsTotal}
            <button class="m-3 w-[calc(100%-24px)] rounded-md border border-border py-2 text-xs text-fg-2 hover:text-fg disabled:opacity-50" disabled={rowsLoading} onclick={() => loadRows(false)}>
              {rowsLoading ? 'Loading…' : `Load more (${(rowsTotal - rows.length).toLocaleString()} left)`}
            </button>
          {/if}
        {/if}
      </div>
    </aside>
  {/if}

  <!-- Page detail -->
  {#if detail && meta}
    {@const d = detail.page}
    <div class="uv-panel absolute bottom-4 left-4 z-10 w-[380px] p-4">
      <button class="absolute right-3.5 top-3 text-fg-2 hover:text-fg" aria-label="Close" onclick={() => engine?.select(null)}>✕</button>
      <div class="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-[1.7px] text-[#92969e]">
        <span class="uv-dot" style="background:{PALETTE[detail.cluster % 8]}"></span>{meta.clusters[detail.cluster].label}
      </div>
      {#if detailThumb}<img class="mt-2.5 h-[170px] w-full rounded-[7px] border border-border object-cover object-top" src={detailThumb} alt="" />{/if}
      <div class="mt-3 text-[15px] font-[550] leading-snug tracking-[-0.2px]">{d.title}</div>
      <a class="mt-1 block break-all text-[11.5px] text-fg-2 hover:text-fg" href={d.url} target="_blank" rel="noreferrer">{d.url}</a>
      <dl class="uv-dl mt-3.5">
        <div><dt>Status</dt><dd class={d.status >= 200 && d.status < 300 ? 'text-success' : 'text-danger'}>{d.status || '—'}</dd></div>
        <div><dt>PageRank</dt><dd>{d.pr.toFixed(2)}</dd></div>
        <div><dt>Inlinks</dt><dd>{d.in.toLocaleString()}</dd></div>
        <div><dt>Outlinks</dt><dd>{d.out.toLocaleString()}</dd></div>
        <div><dt>Depth</dt><dd>{d.depth}</dd></div>
      </dl>
      {#if detail.parent || detail.level === 0 || detail.orphan}
        <div class="mt-3 rounded-md border border-border px-3 py-2 text-[11.5px]">
          {#if detail.orphan}
            <span class="text-warning">Not reachable from the start page by links</span>
            {#if detail.parent}
              <span class="text-fg-2">· linked from</span>
              <button class="block w-full truncate text-left text-fg hover:underline" title={detail.parent.url} onclick={() => engine?.select(detail!.parent!.i, true)}>{detail.parent.title}</button>
            {/if}
          {:else if detail.level === 0}
            <span class="text-fg-2">Start page of the link tree</span>
          {:else if detail.parent}
            <span class="text-fg-2">Reached in {detail.level} {detail.level === 1 ? 'click' : 'clicks'} via</span>
            <button class="block w-full truncate text-left text-fg hover:underline" title={detail.parent.url} onclick={() => engine?.select(detail!.parent!.i, true)}>{detail.parent.title}</button>
          {/if}
        </div>
      {/if}
      <div class="mt-3 flex items-center gap-4 text-[11px] text-fg-2">
        {#if onOpenExplorer}
          <button class="order-last ml-auto rounded-md border border-border px-2.5 py-1 text-[11px] text-fg-1 hover:border-[#5a5f66] hover:text-fg" onclick={() => onOpenExplorer?.(d.url)}>Open in Explorer →</button>
        {/if}
        <span class="flex items-center gap-1.5"><span class="uv-line" style="background:{IN_COLOR}"></span>Inlinks {detail.in.length < detail.inTotal ? `(${detail.in.length.toLocaleString()} drawn)` : ''}</span>
        <span class="flex items-center gap-1.5"><span class="uv-line" style="background:{OUT_COLOR}"></span>Outlinks {detail.out.length < detail.outTotal ? `(${detail.out.length.toLocaleString()} drawn)` : ''}</span>
      </div>
    </div>
  {/if}

  <div class="pointer-events-none absolute bottom-5 right-[476px] z-10 text-[11px] text-[#7d828a]">Click a page · Drag to orbit · Scroll to zoom · Esc to go back</div>
</div>

<style>
  .uv-root { height: clamp(620px, 78vh, 920px); background: radial-gradient(ellipse at 40% 45%, #17191b 0%, #101112 60%, #0d0e0f 100%); }
  .uv-panel { background: #171819f2; border: 1px solid var(--color-border); border-radius: 10px; backdrop-filter: blur(10px); box-shadow: 0 20px 50px #0007; }
  .uv-metrics { display: flex; width: max-content; border: 1px solid var(--color-border); border-radius: 10px; overflow: hidden; background: #171819cc; backdrop-filter: blur(8px); }
  .uv-metrics div { padding: 10px 16px; border-right: 1px solid var(--color-border); }
  .uv-metrics div:last-child { border-right: 0; }
  .uv-metrics dt { font-size: 11px; color: var(--color-fg-2); }
  .uv-metrics dd { margin: 2px 0 0; font-size: 19px; font-weight: 550; letter-spacing: -0.5px; font-variant-numeric: tabular-nums; }
  .uv-fade { opacity: 0; transition: opacity 0.3s; }
  .uv-table { width: 100%; table-layout: fixed; border-collapse: collapse; font-size: 12px; font-variant-numeric: tabular-nums; }
  .uv-table thead th { position: sticky; top: 0; background: #1a1c1e; font-size: 10px; letter-spacing: 0.6px; color: var(--color-fg-2); font-weight: 500; text-align: right; padding: 9px 10px; cursor: pointer; white-space: nowrap; border-bottom: 1px solid var(--color-border); user-select: none; }
  .uv-table thead th:hover { color: #fff; }
  .uv-table .l { text-align: left; }
  .uv-table tbody td { padding: 8px 10px; border-bottom: 1px solid #292b2e; text-align: right; color: var(--color-fg-1); white-space: nowrap; }
  .uv-table tbody tr { cursor: pointer; }
  .uv-table tbody tr:hover td, .uv-table tbody tr.uv-row-sel td { background: #ffffff08; color: #fff; }
  .uv-table td.l > div { display: flex; align-items: center; gap: 8px; min-width: 0; overflow: hidden; }
  .uv-table td.l > div > span:last-child { overflow: hidden; text-overflow: ellipsis; }
  .uv-table td.rk { color: #7d828a; }
  .uv-table td.rk.top { color: #fff; font-weight: 550; }
  .uv-prc { display: flex; align-items: center; justify-content: flex-end; gap: 7px; }
  .uv-bar { width: 38px; height: 3px; border-radius: 2px; background: #292b2e; overflow: hidden; flex: none; }
  .uv-bar i { display: block; height: 100%; }
  .uv-dl { display: grid; grid-template-columns: repeat(5, 1fr); border: 1px solid var(--color-border); border-radius: 8px; overflow: hidden; }
  .uv-dl div { padding: 8px 10px; border-right: 1px solid var(--color-border); }
  .uv-dl div:last-child { border: 0; }
  .uv-dl dt { font-size: 10px; color: var(--color-fg-2); }
  .uv-dl dd { margin: 2px 0 0; font-size: 14px; font-weight: 550; font-variant-numeric: tabular-nums; }
  :global(.uv-dot) { width: 8px; height: 8px; border-radius: 50%; flex: none; display: inline-block; }
  :global(.uv-chip) { display: flex; align-items: center; gap: 7px; padding: 5px 10px 5px 8px; border-radius: 999px; background: #171819e6; border: 1px solid var(--color-border); font-size: 11.5px; color: var(--color-fg-1); white-space: nowrap; cursor: pointer; pointer-events: auto; transition: border-color 0.15s, color 0.15s, opacity 0.2s; }
  :global(.uv-chip b) { font-weight: 500; color: var(--color-fg-2); font-variant-numeric: tabular-nums; }
  :global(.uv-chip:hover), :global(.uv-chip.uv-on) { border-color: #5a5f66; color: #fff; }
  :global(.uv-chip.uv-hide) { opacity: 0; pointer-events: none; }
  :global(.uv-marker) { width: 22px; height: 22px; border-radius: 50%; border: 2px solid #fff; box-shadow: 0 0 0 4px #ffffff22, 0 0 18px #ffffff55; pointer-events: none; }
  .uv-switch { position: relative; width: 24px; height: 14px; border-radius: 999px; background: #3a3d41; transition: background 0.15s; }
  .uv-switch::after { content: ''; position: absolute; top: 2px; left: 2px; width: 10px; height: 10px; border-radius: 50%; background: #d2d4d7; transition: transform 0.15s; }
  .uv-switch.on { background: #75a6ff; }
  .uv-switch.on::after { transform: translateX(10px); }
  .uv-ramp { width: 72px; height: 6px; border-radius: 3px; background: linear-gradient(90deg, #4a5468, #75a6ff, #f2c46d); }
  :global(.uv-chip.uv-mono .uv-dot) { visibility: hidden; }
  .uv-line { display: inline-block; width: 14px; height: 2px; border-radius: 1px; }
</style>
