<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../../api.js';

  interface TreeNode {
    name: string;
    path: string;
    pages: number;
    depth: number;
    isPage: boolean;
    statusCodes: Record<number, number>;
    avgResponseTime: number;
    totalPageRank: number;
    avgPageRank: number;
    indexable: number;
    nonIndexable: number;
    totalInlinks: number;
    avgInternalLinks: number;
    children?: TreeNode[];
  }

  interface Rect {
    node: TreeNode;
    x: number;
    y: number;
    w: number;
    h: number;
  }

  let { crawlId }: { crawlId: string } = $props();

  let canvas = $state<HTMLCanvasElement>()!;
  let tree = $state<TreeNode | null>(null);
  let totalPages = $state(0);
  let message = $state('');
  let loaded = $state(false);
  let loading = $state(false);
  let colorMode = $state<'depth' | 'indexability' | 'pagerank' | 'status'>('depth');
  let zoomStack = $state<TreeNode[]>([]);
  let hoveredRect = $state<Rect | null>(null);
  let rects: Rect[] = [];
  let destroyed = false;
  let animProgress = 0; // 0-1 entrance animation
  let animStartTime = 0;
  let animating = false;
  const ANIM_DURATION = 800; // ms

  const DEPTH_COLORS = [
    '#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6',
    '#1abc9c', '#e67e22', '#e74c3c', '#3498db', '#2ecc71',
  ];
  const GAP = 2;

  let currentNode = $derived(zoomStack.length > 0 ? zoomStack[zoomStack.length - 1] : tree);
  let dirCount = $derived(tree ? countDirs(tree) : 0);
  let hasPageRank = $derived(tree ? tree.avgPageRank > 0 || (tree.children ?? []).some(c => c.avgPageRank > 0) : false);

  function countDirs(node: TreeNode): number {
    let count = node.children && node.children.length > 0 ? 1 : 0;
    for (const c of node.children ?? []) count += countDirs(c);
    return count;
  }

  function getColor(node: TreeNode, mode: string): string {
    if (mode === 'depth') {
      return DEPTH_COLORS[node.depth % DEPTH_COLORS.length];
    }
    if (mode === 'indexability') {
      const total = node.indexable + node.nonIndexable;
      if (total === 0) return '#555';
      const pct = node.indexable / total;
      const r = Math.round(pct < 0.5 ? 255 : 255 - (pct - 0.5) * 2 * 200);
      const g = Math.round(pct > 0.5 ? 200 : pct * 2 * 200);
      return `rgb(${r},${g},60)`;
    }
    if (mode === 'pagerank') {
      const pr = Math.min(node.avgPageRank, 10);
      const t = pr / 10;
      return `rgb(${Math.round(80 + t * 175)},${Math.round(80 - t * 30)},${Math.round(200 - t * 150)})`;
    }
    if (mode === 'status') {
      const codes = node.statusCodes;
      const total = Object.values(codes).reduce((a, b) => a + b, 0);
      if (total === 0) return '#555';
      const ok = (codes[200] || 0) + (codes[201] || 0) + (codes[204] || 0);
      const pct = ok / total;
      const r = Math.round(255 - pct * 200);
      const g = Math.round(pct * 200);
      return `rgb(${r},${g},60)`;
    }
    return '#666';
  }

  // Simple squarified treemap: lay out items into a rectangle
  function squarify(items: TreeNode[], x: number, y: number, w: number, h: number): Rect[] {
    const result: Rect[] = [];
    if (items.length === 0 || w < 1 || h < 1) return result;

    const totalVal = items.reduce((s, n) => s + n.pages, 0);
    if (totalVal === 0) return result;

    layoutRow(items, 0, items.length, totalVal, x, y, w, h, result);
    return result;
  }

  function layoutRow(items: TreeNode[], start: number, end: number, totalVal: number, x: number, y: number, w: number, h: number, result: Rect[]) {
    if (start >= end || w < 1 || h < 1) return;
    if (end - start === 1) {
      result.push({ node: items[start], x: x + GAP/2, y: y + GAP/2, w: Math.max(1, w - GAP), h: Math.max(1, h - GAP) });
      return;
    }

    const sumVal = items.slice(start, end).reduce((s, n) => s + n.pages, 0);
    if (sumVal === 0) return;

    // Find split point that gives best aspect ratio
    const horizontal = w >= h;
    let halfVal = 0;
    let mid = start;
    const target = sumVal / 2;
    for (let i = start; i < end - 1; i++) {
      halfVal += items[i].pages;
      if (halfVal >= target) {
        mid = i + 1;
        break;
      }
      mid = i + 1;
    }
    if (mid === start) mid = start + 1;
    if (mid === end) mid = end - 1;

    const leftVal = items.slice(start, mid).reduce((s, n) => s + n.pages, 0);
    const frac = leftVal / sumVal;

    if (horizontal) {
      const splitX = x + w * frac;
      layoutRow(items, start, mid, totalVal, x, y, w * frac, h, result);
      layoutRow(items, mid, end, totalVal, splitX, y, w * (1 - frac), h, result);
    } else {
      const splitY = y + h * frac;
      layoutRow(items, start, mid, totalVal, x, y, w, h * frac, result);
      layoutRow(items, mid, end, totalVal, x, splitY, w, h * (1 - frac), result);
    }
  }

  function startAnimation() {
    animStartTime = performance.now();
    animProgress = 0;
    animating = true;
    requestAnimationFrame(render);
  }

  function render() {
    if (!canvas || !currentNode) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    // Update animation progress
    if (animating) {
      const elapsed = performance.now() - animStartTime;
      animProgress = Math.min(1, elapsed / ANIM_DURATION);
      if (animProgress >= 1) animating = false;
    }

    const dpr = window.devicePixelRatio || 1;
    const rect = canvas.getBoundingClientRect();
    canvas.width = rect.width * dpr;
    canvas.height = rect.height * dpr;
    ctx.scale(dpr, dpr);

    const w = rect.width;
    const h = rect.height;

    ctx.fillStyle = '#1a1a2e';
    ctx.fillRect(0, 0, w, h);

    // Get children to display (or the node itself if leaf)
    const children = currentNode!.children?.filter(c => c.pages > 0).sort((a, b) => b.pages - a.pages) ?? [];
    if (children.length === 0) {
      rects = [{ node: currentNode!, x: GAP, y: GAP, w: w - GAP * 2, h: h - GAP * 2 }];
    } else {
      rects = squarify(children, 0, 0, w, h);
    }

    // Draw rectangles with staggered entrance animation
    for (let idx = 0; idx < rects.length; idx++) {
      const r = rects[idx];
      if (r.w < 1 || r.h < 1) continue;

      // Stagger: each rect starts its animation slightly later
      const stagger = rects.length > 1 ? idx / rects.length : 0;
      const localProgress = animProgress < 1
        ? Math.max(0, Math.min(1, (animProgress - stagger * 0.5) / 0.5))
        : 1;
      // Ease out cubic
      const t = 1 - Math.pow(1 - localProgress, 3);

      const color = getColor(r.node, colorMode);
      const isHovered = hoveredRect?.node === r.node;

      // Animated dimensions: grow from center
      const cx = r.x + r.w / 2;
      const cy = r.y + r.h / 2;
      const aw = r.w * t;
      const ah = r.h * t;
      const ax = cx - aw / 2;
      const ay = cy - ah / 2;

      // Fill with animated opacity
      const alpha = isHovered ? 'ee' : Math.round(0xcc * t).toString(16).padStart(2, '0');
      ctx.fillStyle = color + alpha;
      ctx.fillRect(ax, ay, aw, ah);

      // Border
      if (t > 0.3) {
        ctx.strokeStyle = isHovered ? '#ffffff' : '#1a1a2e';
        ctx.lineWidth = isHovered ? 2 : 1;
        ctx.strokeRect(ax, ay, aw, ah);
      }

      // Labels (only show after animation mostly done)
      if (t > 0.7 && aw > 30 && ah > 16) {
        const labelAlpha = Math.min(1, (t - 0.7) / 0.3);
        ctx.fillStyle = `rgba(255,255,255,${(0.87 * labelAlpha).toFixed(2)})`;
        const fontSize = aw > 120 && ah > 40 ? 12 : aw > 60 ? 10 : 8;
        ctx.font = `${fontSize}px ui-monospace, monospace`;

        const name = r.node.name || '/';
        const maxChars = Math.floor((aw - 8) / (fontSize * 0.6));
        const label = name.length > maxChars ? name.substring(0, maxChars - 1) + '..' : name;
        ctx.fillText(label, ax + 4, ay + fontSize + 2);

        // Page count
        if (ah > 30 && aw > 50) {
          ctx.fillStyle = `rgba(255,255,255,${(0.53 * labelAlpha).toFixed(2)})`;
          ctx.font = `${Math.max(8, fontSize - 2)}px ui-monospace, monospace`;
          ctx.fillText(r.node.pages.toLocaleString() + ' pages', ax + 4, ay + fontSize + 14);
        }

        // Indexable %
        if (ah > 44 && aw > 70) {
          const idxTotal = r.node.indexable + r.node.nonIndexable;
          if (idxTotal > 0) {
            const idxPct = ((r.node.indexable / idxTotal) * 100).toFixed(0);
            ctx.fillStyle = `rgba(255,255,255,${(0.4 * labelAlpha).toFixed(2)})`;
            ctx.fillText(`${idxPct}% indexable`, ax + 4, ay + fontSize + 26);
          }
        }
      }
    }

    // Continue animation
    if (animProgress < 1 && animating) {
      requestAnimationFrame(render);
    }
  }

  function handleMouseMove(e: MouseEvent) {
    const canvasRect = canvas.getBoundingClientRect();
    const x = e.clientX - canvasRect.left;
    const y = e.clientY - canvasRect.top;

    let found: Rect | null = null;
    for (const r of rects) {
      if (x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h) {
        found = r;
      }
    }
    if (found?.node !== hoveredRect?.node) {
      hoveredRect = found;
      render();
    }
  }

  function handleClick() {
    if (!hoveredRect) return;
    const node = hoveredRect.node;
    if (!node.children || node.children.length === 0) return;
    zoomStack = [...zoomStack, node];
    hoveredRect = null;
    startAnimation();
  }

  function zoomOut() {
    if (zoomStack.length === 0) return;
    zoomStack = zoomStack.slice(0, -1);
    hoveredRect = null;
    startAnimation();
  }

  function resetZoom() {
    zoomStack = [];
    hoveredRect = null;
    startAnimation();
  }

  $effect(() => {
    colorMode;
    requestAnimationFrame(render);
  });

  async function loadTreemap() {
    loaded = true;
    loading = true;
    message = 'Loading treemap...';
    try {
      const data = await api.getDirectoryTree(crawlId);
      if (destroyed) return;
      if (!data.tree) {
        message = 'No directory tree data available.';
        loading = false;
        return;
      }
      tree = data.tree as unknown as TreeNode;
      totalPages = data.totalPages;
      message = '';
      await tick();
      startAnimation();
    } catch (err: any) {
      if (!destroyed) message = `Failed to load treemap: ${err.message}`;
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    const handleResize = () => requestAnimationFrame(render);
    window.addEventListener('resize', handleResize);
    return () => {
      destroyed = true;
      window.removeEventListener('resize', handleResize);
    };
  });

  function indexPct(node: TreeNode): string {
    const total = node.indexable + node.nonIndexable;
    if (total === 0) return 'N/A';
    return ((node.indexable / total) * 100).toFixed(0) + '%';
  }

  function statusSummary(codes: Record<number, number>): string {
    return Object.entries(codes)
      .sort((a, b) => Number(a[0]) - Number(b[0]))
      .map(([code, count]) => `${code}: ${count}`)
      .join(', ');
  }
</script>

<div class="rounded-xl border border-border bg-surface-2 p-5">
  {#if !loaded}
    <div class="flex flex-col items-center justify-center py-8 text-center">
      <h3 class="text-sm font-medium text-fg-2 mb-2">Site Treemap</h3>
      <p class="text-xs text-fg-2/70 mb-4">Full-site overview as a zoomable treemap. Each rectangle is a directory, sized by page count.</p>
      <button
        type="button"
        class="px-5 py-2 rounded-lg bg-accent text-white font-medium hover:bg-accent/90 transition-colors cursor-pointer text-sm"
        onclick={loadTreemap}
      >
        Load Treemap
      </button>
    </div>
  {:else}
    <!-- Header -->
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-medium text-fg-2">Site Treemap</h3>
        {#if tree}
          <span class="text-[10px] text-fg-2/50 font-mono">
            {totalPages.toLocaleString()} pages across {dirCount} directories
          </span>
        {/if}
        {#if loading}
          <div class="w-3.5 h-3.5 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
        {/if}
      </div>
      {#if !message}
        <div class="flex items-center gap-2">
          <div class="flex rounded-lg border border-border overflow-hidden text-[10px]">
            <button class="px-2 py-0.5 {colorMode === 'depth' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'depth'}>Depth</button>
            <button class="px-2 py-0.5 {colorMode === 'indexability' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'indexability'}>Indexability</button>
            <button class="px-2 py-0.5 {colorMode === 'pagerank' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'} {!hasPageRank ? 'opacity-40 cursor-not-allowed' : ''}" onclick={() => { if (hasPageRank) colorMode = 'pagerank'; }} title={!hasPageRank ? 'PageRank data not available (requires Link Intelligence)' : ''}>PageRank</button>
            <button class="px-2 py-0.5 {colorMode === 'status' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'status'}>Status</button>
          </div>
          {#if zoomStack.length > 0}
            <button class="px-2 py-0.5 text-[10px] rounded-lg border border-border bg-surface-3 text-fg-2 hover:bg-surface-1" onclick={zoomOut}>Back</button>
            <button class="px-2 py-0.5 text-[10px] rounded-lg border border-border bg-surface-3 text-fg-2 hover:bg-surface-1" onclick={resetZoom}>Reset</button>
          {/if}
        </div>
      {/if}
    </div>

    <!-- Breadcrumbs -->
    {#if zoomStack.length > 0 && !message}
      <div class="flex items-center gap-1 mb-3 text-[10px] text-fg-2 overflow-x-auto">
        <button class="hover:text-accent font-mono" onclick={resetZoom}>/</button>
        {#each zoomStack as node, i}
          <span class="text-fg-2/40">/</span>
          <button
            class="hover:text-accent font-mono truncate max-w-[160px] {i === zoomStack.length - 1 ? 'text-fg-1 font-medium' : ''}"
            onclick={() => { zoomStack = zoomStack.slice(0, i + 1); hoveredRect = null; startAnimation(); }}
            title={node.path}
          >
            {node.name || '/'}
          </button>
        {/each}
        <span class="text-fg-2/40 ml-2">({currentNode?.pages.toLocaleString()} pages)</span>
      </div>
    {/if}

    {#if message}
      <p class="text-sm text-fg-2 text-center py-8">{message}</p>
    {:else}
      <div class="relative">
        <canvas
          bind:this={canvas}
          class="w-full rounded-lg cursor-pointer"
          style="height: 500px;"
          onmousemove={handleMouseMove}
          onmouseleave={() => { hoveredRect = null; render(); }}
          onclick={handleClick}
        ></canvas>

        <!-- Legend -->
        <div class="absolute top-2 right-2 rounded-lg border border-border bg-surface-3/90 p-2 text-[11px] text-fg-2 space-y-0.5 z-10">
          {#if colorMode === 'depth'}
            {#each Array.from({ length: Math.min(5, (currentNode?.depth ?? 0) + 3) }, (_, i) => (currentNode?.depth ?? 0) + i) as d}
              <div class="flex items-center gap-1.5">
                <span class="w-2.5 h-2.5 rounded-sm inline-block shrink-0" style="background: {DEPTH_COLORS[d % DEPTH_COLORS.length]}"></span>
                Depth {d}
              </div>
            {/each}
          {:else if colorMode === 'indexability'}
            <div class="w-20 h-2.5 rounded-sm" style="background: linear-gradient(to right, rgb(255,60,60), rgb(200,200,60), rgb(60,200,60))"></div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20"><span>0%</span><span>100%</span></div>
            <div class="text-[10px] text-fg-2/70">Indexable %</div>
          {:else if colorMode === 'pagerank'}
            <div class="w-20 h-2.5 rounded-sm" style="background: linear-gradient(to right, rgb(80,80,200), rgb(170,60,150), rgb(255,50,50))"></div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20"><span>Low</span><span>High</span></div>
            <div class="text-[10px] text-fg-2/70">Avg PageRank</div>
          {:else}
            <div class="w-20 h-2.5 rounded-sm" style="background: linear-gradient(to right, rgb(255,60,60), rgb(200,200,60), rgb(60,200,60))"></div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20"><span>Errors</span><span>200s</span></div>
            <div class="text-[10px] text-fg-2/70">Status Health</div>
          {/if}
          <div class="text-[10px] text-fg-2/60 mt-1 pt-1 border-t border-border/50">Click to zoom in</div>
        </div>
      </div>

      <!-- Tooltip -->
      {#if hoveredRect}
        <div class="mt-2 rounded-lg border border-border bg-surface-3 p-3 text-xs">
          <div class="font-mono text-fg-1 font-medium mb-1">{hoveredRect.node.path || '/'}</div>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-fg-2">
            <span>Pages: <strong class="text-fg-1">{hoveredRect.node.pages.toLocaleString()}</strong></span>
            <span>Depth: <strong class="text-fg-1">{hoveredRect.node.depth}</strong></span>
            <span>Indexable: <strong class="text-fg-1">{indexPct(hoveredRect.node)}</strong></span>
            <span>Avg PR: <strong class="text-fg-1">{hoveredRect.node.avgPageRank.toFixed(2)}</strong></span>
            <span>Avg Links: <strong class="text-fg-1">{hoveredRect.node.avgInternalLinks.toFixed(1)}</strong></span>
            <span>Inlinks: <strong class="text-fg-1">{hoveredRect.node.totalInlinks.toLocaleString()}</strong></span>
            {#if Object.keys(hoveredRect.node.statusCodes).length > 0}
              <span>Status: <strong class="text-fg-1">{statusSummary(hoveredRect.node.statusCodes)}</strong></span>
            {/if}
          </div>
          {#if hoveredRect.node.children && hoveredRect.node.children.length > 0}
            <div class="text-fg-2/60 mt-1 text-[10px]">Click to explore {hoveredRect.node.children.length} subdirectories</div>
          {/if}
        </div>
      {/if}
    {/if}
  {/if}
</div>
