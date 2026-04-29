<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api.js';
  import { createWsClient } from '../../ws.js';
  import SigmaGraph from './SigmaGraph.svelte';
  import type { Component } from 'svelte';
  import type { GraphNode, GraphEdge, GraphMeta, BundledEdge } from '../../types/graph.js';

  // Lazy-load CosmosGraph only when needed (50K+ nodes) to reduce initial bundle
  let CosmosGraph = $state<Component<any> | null>(null);
  let cosmosLoadError = $state('');

  let { crawlId }: { crawlId: string } = $props();

  // Graph data from API
  let graphNodes = $state<GraphNode[]>([]);
  let graphEdges = $state<GraphEdge[]>([]);
  let graphMeta = $state<GraphMeta | null>(null);

  // Shared UI state
  let graphMessage = $state('');
  let searchQuery = $state('');
  let searchMatchIds = $state<Set<string> | null>(null);
  let selectedNode = $state<GraphNode | null>(null);
  let hoveredNode = $state<GraphNode | null>(null);
  let colorMode = $state<'depth' | 'authority' | 'hub'>('depth');
  let edgeMode = $state<'normal' | 'bundled'>('normal');
  let hiddenDepths = $state(new Set<number | null>());

  // Worker-computed state
  let connectedPages = $state<{ inlinks: string[]; outlinks: string[] }>({ inlinks: [], outlinks: [] });
  let bundledEdges = $state<BundledEdge[]>([]);

  const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#1abc9c', '#e67e22', '#e74c3c', '#3498db', '#2ecc71'];
  const UNREACHABLE_COLOR = '#555';

  const COSMOS_THRESHOLD = 50_000;

  let renderer = $derived(graphNodes.length >= COSMOS_THRESHOLD ? 'cosmos' : 'sigma');
  let maxDepthVal = $derived(graphMeta?.maxDepth ?? 0);
  let hasUnreachable = $derived(graphNodes.some(n => n.depth === null));

  // Lazy-load Cosmos renderer when threshold is hit
  $effect(() => {
    if (renderer === 'cosmos' && !CosmosGraph && !cosmosLoadError) {
      import('./CosmosGraph.svelte').then((mod) => {
        CosmosGraph = mod.default;
      }).catch((err) => {
        cosmosLoadError = `Failed to load GPU renderer: ${err.message}`;
      });
    }
  });

  // Workers
  let dataWorker: Worker | null = null;
  let searchWorker: Worker | null = null;
  let neighborWorker: Worker | null = null;
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;
  let destroyed = false;
  let graphLoaded = $state(false);
  let workerError = $state<string | null>(null);

  // Debounced search via Worker #2
  $effect(() => {
    if (!searchWorker) return;
    const q = searchQuery;
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    if (!q) {
      searchMatchIds = null;
      return;
    }
    searchDebounceTimer = setTimeout(() => {
      searchWorker!.postMessage({ type: 'search', query: q });
    }, 150);
  });

  // Request neighbors via Worker #3 when node is selected
  $effect(() => {
    if (!neighborWorker || !selectedNode) {
      connectedPages = { inlinks: [], outlinks: [] };
      return;
    }
    neighborWorker.postMessage({ type: 'neighbors', nodeId: selectedNode.id, limit: 10 });
  });

  let analysisPendingGraph = $state(false);

  function initWorkers() {
    try {
      dataWorker = new Worker(new URL('../../../workers/graph-data-worker.ts', import.meta.url), { type: 'module' });
      searchWorker = new Worker(new URL('../../../workers/search-worker.ts', import.meta.url), { type: 'module' });
      neighborWorker = new Worker(new URL('../../../workers/neighbor-worker.ts', import.meta.url), { type: 'module' });
    } catch (e) {
      console.error('Worker init failed:', e);
      dataWorker?.terminate();
      searchWorker?.terminate();
      neighborWorker?.terminate();
      dataWorker = searchWorker = neighborWorker = null;
      workerError = 'Failed to initialize graph workers. Web Workers may be disabled in your browser.';
      return;
    }

    dataWorker.onmessage = ({ data }) => {
      if (destroyed) return;
      if (data.type === 'bundled') bundledEdges = data.bundles;
    };
    searchWorker.onmessage = ({ data }) => {
      if (destroyed) return;
      if (data.type === 'results') {
        searchMatchIds = data.matchIds.length > 0 ? new Set(data.matchIds) : null;
      }
    };
    neighborWorker.onmessage = ({ data }) => {
      if (destroyed) return;
      if (data.type === 'neighbors') {
        connectedPages = { inlinks: data.inlinks, outlinks: data.outlinks };
      }
    };
  }

  function processGraphData(data: Awaited<ReturnType<typeof api.getCrawlGraph>>) {
    if (!data.nodes || data.nodes.length === 0) {
      graphMessage = 'No pages to visualize.';
      return;
    }
    if (!data.edges || data.edges.length === 0) {
      graphMessage = 'No internal links found. The link graph requires links between pages.';
      return;
    }
    // Create workers only when we have real graph data
    if (!dataWorker) initWorkers();
    if (workerError || !dataWorker) return;

    graphNodes = data.nodes;
    graphEdges = data.edges || [];
    graphMeta = data.meta;
    graphMessage = '';
    analysisPendingGraph = false;

    const edges = data.edges || [];
    dataWorker!.postMessage({ type: 'process', nodes: data.nodes, edges });
    dataWorker!.postMessage({ type: 'bundle', edges, minCount: 3 });
    searchWorker!.postMessage({ type: 'build', nodes: data.nodes.map((n: GraphNode) => ({ id: n.id, title: n.title })) });
    neighborWorker!.postMessage({ type: 'buildAdjacency', edges });
  }

  function loadGraph() {
    graphLoaded = true;
    graphMessage = 'Loading graph data...';

    // Fetch graph data — defer worker creation until data confirmed
    api.getCrawlGraph(crawlId).then((data) => {
      if (destroyed) return;
      if (data.meta && (data.meta as any).pending) {
        graphMessage = 'Link analysis is still running. Graph will appear automatically when complete.';
        analysisPendingGraph = true;
        return;
      }
      processGraphData(data);
    }).catch((err) => {
      if (!destroyed) graphMessage = `Failed to load graph: ${err.message}`;
    });
  }

  onMount(() => {
    // Listen for analysis_complete to auto-reload graph when deferred analysis finishes
    const ws = createWsClient((msg) => {
      if (msg.type === 'analysis_complete' && analysisPendingGraph && graphLoaded) {
        graphMessage = 'Loading graph data...';
        api.getCrawlGraph(crawlId).then((data) => {
          if (destroyed) return;
          processGraphData(data);
        }).catch((err) => {
          if (!destroyed) graphMessage = `Failed to load graph: ${err.message}`;
        });
      }
    });

    return () => {
      destroyed = true;
      ws.close();
      if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
      dataWorker?.postMessage({ type: 'clear' });
      searchWorker?.postMessage({ type: 'clear' });
      neighborWorker?.postMessage({ type: 'clear' });
      dataWorker?.terminate();
      searchWorker?.terminate();
      neighborWorker?.terminate();
    };
  });

  function toggleDepth(depth: number | null) {
    const next = new Set(hiddenDepths);
    if (next.has(depth)) next.delete(depth); else next.add(depth);
    hiddenDepths = next;
  }

  function showAllDepths() { hiddenDepths = new Set(); }

  function pathShort(url: string): string {
    try { return new URL(url).pathname; } catch { return url; }
  }

  function handleSelectNode(node: GraphNode | null) {
    selectedNode = node;
  }

  function handleHoverNode(node: GraphNode | null) {
    hoveredNode = node;
  }
</script>

<div class="rounded-xl border border-border bg-surface-2 p-5">
  {#if !graphLoaded}
    <div class="flex flex-col items-center justify-center py-8 text-center">
      <h3 class="text-sm font-medium text-fg-2 mb-2">Internal Link Graph</h3>
      <p class="text-xs text-fg-2/70 mb-4">WebGL visualization of internal link structure. Loads on demand.</p>
      <button
        type="button"
        class="px-5 py-2 rounded-lg bg-accent text-white font-medium hover:bg-accent/90 transition-colors cursor-pointer text-sm"
        onclick={loadGraph}
      >
        Load Graph
      </button>
    </div>
  {:else}
  <div class="flex items-center justify-between mb-3">
    <div class="flex items-center gap-2">
      <h3 class="text-sm font-medium text-fg-2">Internal Link Graph</h3>
      {#if graphNodes.length > 0}
        <span class="text-[10px] text-fg-2/50 font-mono">
          {graphNodes.length.toLocaleString()} nodes
          {#if graphMeta?.nodeCapped}
            of {graphMeta.totalNodes?.toLocaleString()} (top by PageRank)
          {/if}
          {#if graphMeta?.samplingApplied}
            · edges sampled
          {/if}
        </span>
      {/if}
    </div>
    {#if !graphMessage}
      <div class="flex items-center gap-2">
        <!-- Color mode buttons -->
        <div class="flex rounded-lg border border-border overflow-hidden text-[10px]">
          <button class="px-2 py-0.5 {colorMode === 'depth' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'depth'}>Depth</button>
          <button class="px-2 py-0.5 {colorMode === 'authority' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'authority'}>Authority</button>
          <button class="px-2 py-0.5 {colorMode === 'hub' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'hub'}>Hub</button>
        </div>
        <!-- Edge mode -->
        {#if renderer === 'sigma'}
        <div class="flex rounded-lg border border-border overflow-hidden text-[10px]">
          <button class="px-2 py-0.5 {edgeMode === 'normal' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => edgeMode = 'normal'}>Edges</button>
          <button class="px-2 py-0.5 {edgeMode === 'bundled' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => edgeMode = 'bundled'} disabled={bundledEdges.length === 0} title="Group edges by URL directory — shows link flow between site sections">Bundled</button>
        </div>
        {/if}
        <!-- Search -->
        <div class="relative">
          <input type="text" bind:value={searchQuery} placeholder="Search URL..." class="w-36 text-xs rounded-lg border border-border bg-surface-3 px-2 py-1 text-fg-1 placeholder:text-fg-2/50 focus:outline-none focus:border-accent" />
          {#if searchQuery}
            <button class="absolute right-1.5 top-1/2 -translate-y-1/2 text-fg-2 hover:text-fg-1 text-xs" onclick={() => searchQuery = ''}>x</button>
          {/if}
        </div>
      </div>
    {/if}
  </div>

  {#if workerError}
    <div class="p-4 text-center text-fg-muted">{workerError}</div>
  {:else if graphMessage}
    <p class="text-sm text-fg-2 text-center py-8">{graphMessage}</p>
  {:else if graphNodes.length > 0 && graphMeta}
    <div class="relative">
      <!-- Renderer -->
      {#if renderer === 'sigma'}
        <SigmaGraph
          nodes={graphNodes}
          edges={graphEdges}
          meta={graphMeta}
          {colorMode}
          {edgeMode}
          {bundledEdges}
          {searchQuery}
          {searchMatchIds}
          {hiddenDepths}
          onSelectNode={handleSelectNode}
          onHoverNode={handleHoverNode}
        />
      {:else if CosmosGraph}
        <CosmosGraph
          nodes={graphNodes}
          edges={graphEdges}
          meta={graphMeta}
          {colorMode}
          {searchMatchIds}
          {hiddenDepths}
          onSelectNode={handleSelectNode}
          onHoverNode={handleHoverNode}
        />
      {:else if cosmosLoadError}
        <div class="w-full rounded-lg bg-surface-1 flex items-center justify-center" style="height: 500px;">
          <div class="text-center text-fg-2">
            <div class="text-sm text-red-400">{cosmosLoadError}</div>
          </div>
        </div>
      {:else}
        <div class="w-full rounded-lg bg-surface-1 flex items-center justify-center" style="height: 500px;">
          <div class="text-center text-fg-2">
            <div class="text-sm">Loading GPU renderer...</div>
          </div>
        </div>
      {/if}

      <!-- Legend overlay (top-right inside container) -->
      <div class="absolute top-2 right-2 rounded-lg border border-border bg-surface-3/90 p-2 text-[11px] text-fg-2 space-y-0.5 z-10">
        {#if colorMode === 'depth'}
          {#each Array.from({ length: Math.min(maxDepthVal + 1, 6) }, (_, i) => i) as d}
            <button class="flex items-center gap-1.5 w-full hover:text-fg-1 {hiddenDepths.has(d) ? 'opacity-30' : ''}" onclick={() => toggleDepth(d)}>
              <span class="w-2.5 h-2.5 rounded-full inline-block shrink-0" style="background: {DEPTH_COLORS[d]}"></span>
              Depth {d}
            </button>
          {/each}
          {#if hasUnreachable}
            <button class="flex items-center gap-1.5 w-full hover:text-fg-1 {hiddenDepths.has(null) ? 'opacity-30' : ''}" onclick={() => toggleDepth(null)}>
              <span class="w-2.5 h-2.5 rounded-full inline-block shrink-0" style="background: {UNREACHABLE_COLOR}"></span>
              Unreachable
            </button>
          {/if}
          {#if hiddenDepths.size > 0}
            <button class="text-accent text-[10px] hover:underline mt-1" onclick={showAllDepths}>Show all</button>
          {/if}
        {:else if colorMode === 'authority'}
          <div class="flex items-center gap-1.5">
            <div class="w-20 h-2.5 rounded-full" style="background: linear-gradient(to right, rgb(255,255,50), rgb(255,165,25), rgb(255,75,0))"></div>
          </div>
          <div class="flex justify-between text-[9px] text-fg-2/60 w-20">
            <span>Low</span>
            <span>High</span>
          </div>
          <div class="text-[10px] text-fg-2/70 mt-0.5">Authority Score</div>
        {:else}
          <div class="flex items-center gap-1.5">
            <div class="w-20 h-2.5 rounded-full" style="background: linear-gradient(to right, rgb(200,230,100), rgb(100,190,180), rgb(30,150,255))"></div>
          </div>
          <div class="flex justify-between text-[9px] text-fg-2/60 w-20">
            <span>Low</span>
            <span>High</span>
          </div>
          <div class="text-[10px] text-fg-2/70 mt-0.5">Hub Score</div>
        {/if}
      </div>
    </div>

    <!-- Hover tooltip (shows below graph) -->
    {#if hoveredNode && !selectedNode}
      <div class="mt-2 text-xs text-fg-2 flex flex-wrap gap-x-3">
        <span class="font-mono text-fg-1">{pathShort(hoveredNode.id)}</span>
        {#if hoveredNode.title}
          <span>{hoveredNode.title.substring(0, 60)}</span>
        {/if}
        <span>Depth: {hoveredNode.depth === null ? 'unreachable' : hoveredNode.depth}</span>
        <span>Inlinks: {hoveredNode.inDegree}</span>
        <span>Authority: {hoveredNode.authority.toFixed(1)}</span>
      </div>
    {/if}

    <!-- Selected node detail panel -->
    {#if selectedNode}
      <div class="mt-3 rounded-lg border border-border bg-surface-3 p-3 text-xs">
        <div class="flex items-start justify-between mb-2">
          <div>
            <div class="font-mono text-fg-1 font-medium">{pathShort(selectedNode.id)}</div>
            {#if selectedNode.title}
              <div class="text-fg-2 mt-0.5">{selectedNode.title}</div>
            {/if}
          </div>
          <button class="text-fg-2 hover:text-fg-1 shrink-0 ml-2" onclick={() => selectedNode = null}>x</button>
        </div>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-fg-2 mb-2">
          <span>Depth: <strong class="text-fg-1">{selectedNode.depth === null ? 'unreachable' : selectedNode.depth}</strong></span>
          <span>Authority: <strong class="text-fg-1">{selectedNode.authority.toFixed(1)}</strong></span>
          <span>Hub: <strong class="text-fg-1">{selectedNode.hub.toFixed(1)}</strong></span>
          <span>Inlinks: <strong class="text-fg-1">{selectedNode.inDegree}</strong></span>
          <span>Outlinks: <strong class="text-fg-1">{selectedNode.outDegree}</strong></span>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <div class="text-fg-2 mb-1">Inlinks ({connectedPages.inlinks.length}{selectedNode.inDegree > 10 ? '+' : ''})</div>
            {#each connectedPages.inlinks as url}
              <div class="font-mono text-fg-2 truncate">{pathShort(url)}</div>
            {/each}
            {#if connectedPages.inlinks.length === 0}
              <div class="text-fg-2/50 italic">None</div>
            {/if}
          </div>
          <div>
            <div class="text-fg-2 mb-1">Outlinks ({connectedPages.outlinks.length}{selectedNode.outDegree > 10 ? '+' : ''})</div>
            {#each connectedPages.outlinks as url}
              <div class="font-mono text-fg-2 truncate">{pathShort(url)}</div>
            {/each}
            {#if connectedPages.outlinks.length === 0}
              <div class="text-fg-2/50 italic">None</div>
            {/if}
          </div>
        </div>
      </div>
    {/if}
  {/if}
  {/if}
</div>
