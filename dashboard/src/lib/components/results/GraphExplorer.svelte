<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { DirectedGraph } from 'graphology';
  import Sigma from 'sigma';
  import FA2Layout from 'graphology-layout-forceatlas2/worker';
  import forceAtlas2 from 'graphology-layout-forceatlas2';
  import { api } from '../../api.js';
  import { createWsClient } from '../../ws.js';
  import type { GraphNode, GraphEdge, SubgraphMeta, GraphSearchResult } from '../../types/graph.js';
  import { buildRankMaps } from '../../utils/graph.js';

  let { crawlId }: { crawlId: string } = $props();

  // State
  let visibleNodes = $state<GraphNode[]>([]);
  let visibleEdges = $state<GraphEdge[]>([]);
  let meta = $state<SubgraphMeta | null>(null);
  let expandedNodes = $state(new Set<string>());
  let breadcrumbs = $state<{ url: string; title: string }[]>([]);
  let selectedNode = $state<GraphNode | null>(null);
  let hoveredNode = $state<GraphNode | null>(null);
  let colorMode = $state<'depth' | 'authority' | 'hub' | 'pagerank'>('depth');
  let message = $state('');
  let loading = $state(false);
  let graphLoaded = $state(false);
  let analysisPending = $state(false);
  let destroyed = false;

  // Search
  let searchQuery = $state('');
  let searchResults = $state<GraphSearchResult[]>([]);
  let searchDebounce: ReturnType<typeof setTimeout> | null = null;

  // Sigma
  let container: HTMLDivElement;
  let sigmaRenderer: Sigma | null = null;
  let fa2Layout: FA2Layout | null = null;
  let graphInstance: DirectedGraph | null = null;

  // Rank maps for coloring
  let authorityRank = new Map<string, number>();
  let hubRank = new Map<string, number>();
  let pageRankMap = new Map<string, number>();
  let nodeIndex = new Map<string, GraphNode>();

  const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#1abc9c', '#e67e22', '#e74c3c', '#3498db', '#2ecc71'];
  const UNREACHABLE_COLOR = '#555555';
  const EXPANDABLE_RING_COLOR = '#fbbf24';

  function getNodeColor(node: GraphNode, mode: string): string {
    if (mode === 'authority') {
      const t = authorityRank.get(node.id) ?? 0;
      return `rgb(255,${Math.round(255 - t * 180)},${Math.round(50 - t * 50)})`;
    }
    if (mode === 'hub') {
      const t = hubRank.get(node.id) ?? 0;
      return `rgb(${Math.round(200 - t * 170)},${Math.round(230 - t * 80)},${Math.round(100 + t * 155)})`;
    }
    if (mode === 'pagerank') {
      const t = pageRankMap.get(node.id) ?? 0;
      return `rgb(${Math.round(80 + t * 175)},${Math.round(80 - t * 30)},${Math.round(200 - t * 150)})`;
    }
    return node.depth === null ? UNREACHABLE_COLOR : DEPTH_COLORS[node.depth % DEPTH_COLORS.length];
  }

  // Pre-computed max PageRank for node sizing (updated on buildGraph / expand)
  let maxPageRank = 0.001;

  function getNodeSize(node: GraphNode): number {
    const pr = node.pageRank ?? 0;
    return Math.max(4, Math.min(18, 4 + (pr / maxPageRank) * 14));
  }

  function safePath(url: string): string {
    try { return new URL(url).pathname; } catch { return url; }
  }

  // Sync color mode changes to renderer
  $effect(() => {
    if (sigmaRenderer && graphInstance) {
      // Update node colors
      for (const node of visibleNodes) {
        if (graphInstance.hasNode(node.id)) {
          graphInstance.setNodeAttribute(node.id, 'color', getNodeColor(node, colorMode));
        }
      }
      sigmaRenderer.refresh({ skipIndexation: true });
    }
  });

  // Search effect
  $effect(() => {
    const q = searchQuery;
    if (searchDebounce) clearTimeout(searchDebounce);
    if (!q || q.length < 2) {
      searchResults = [];
      return;
    }
    searchDebounce = setTimeout(async () => {
      try {
        const data = await api.searchGraphNodes(crawlId, q, 15);
        if (!destroyed) searchResults = data.results;
      } catch { /* ignore */ }
    }, 250);
  });

  async function loadInitialGraph() {
    graphLoaded = true;
    loading = true;
    message = 'Loading graph explorer...';
    try {
      const data = await api.getCrawlSubgraph(crawlId, undefined, 2, 500);
      if (destroyed) return;
      if (data.meta?.pending) {
        message = 'Link analysis is still running. Explorer will appear automatically when complete.';
        analysisPending = true;
        loading = false;
        return;
      }
      if (!data.nodes || data.nodes.length === 0) {
        message = 'No pages to visualize.';
        loading = false;
        return;
      }
      meta = data.meta;
      visibleNodes = data.nodes;
      visibleEdges = data.edges || [];
      breadcrumbs = [{ url: data.meta.rootURL, title: data.nodes.find(n => n.id === data.meta.rootURL)?.title || safePath(data.meta.rootURL) }];
      expandedNodes = new Set([data.meta.rootURL]);
      message = '';
      // Wait for DOM to render the container div before initializing Sigma
      await tick();
      buildGraph();
    } catch (err: any) {
      if (!destroyed) message = `Failed to load graph: ${err.message}`;
    }
    loading = false;
  }

  function buildGraph() {
    // Clean up existing
    stopLayout();
    sigmaRenderer?.kill();
    sigmaRenderer = null;
    graphInstance = null;

    if (visibleNodes.length === 0) return;

    const graph = new DirectedGraph();
    graphInstance = graph;

    // Build lookup maps
    nodeIndex = new Map(visibleNodes.map(n => [n.id, n]));
    ({ authorityRank, hubRank } = buildRankMaps(visibleNodes));

    // Build pagerank rank map
    const sortedByPR = [...visibleNodes].sort((a, b) => (a.pageRank ?? 0) - (b.pageRank ?? 0));
    pageRankMap = new Map();
    const maxI = Math.max(1, sortedByPR.length - 1);
    for (let i = 0; i < sortedByPR.length; i++) {
      pageRankMap.set(sortedByPR[i].id, i / maxI);
    }

    // Pre-compute max PageRank for node sizing
    maxPageRank = Math.max(0.001, ...visibleNodes.map(n => n.pageRank ?? 0));

    // Add nodes
    const spread = Math.sqrt(visibleNodes.length) * 25;
    for (const node of visibleNodes) {
      graph.addNode(node.id, {
        x: (Math.random() - 0.5) * spread,
        y: (Math.random() - 0.5) * spread,
        size: getNodeSize(node),
        color: getNodeColor(node, colorMode),
        label: node.title || safePath(node.id),
      });
    }

    // Add edges
    for (const edge of visibleEdges) {
      if (edge.source === edge.target) continue;
      if (graph.hasNode(edge.source) && graph.hasNode(edge.target) && !graph.hasDirectedEdge(edge.source, edge.target)) {
        graph.addDirectedEdge(edge.source, edge.target, {
          color: '#66666644',
          size: 0.5,
        });
      }
    }

    if (!container) return;

    // Init Sigma
    try {
      sigmaRenderer = new Sigma(graph, container, {
        defaultEdgeColor: '#66666644',
        defaultNodeColor: '#999',
        labelFont: 'ui-monospace, monospace',
        labelSize: 11,
        labelWeight: 'normal',
        labelRenderedSizeThreshold: 6,
        renderLabels: true,
        renderEdgeLabels: false,
        hideEdgesOnMove: visibleNodes.length > 500,
        minEdgeThickness: 0.5,
        stagePadding: 30,
        autoRescale: true,
        autoCenter: true,
        enableCameraZooming: true,
        enableCameraPanning: true,
        zoomingRatio: 1.5,
        // @ts-ignore
        labelColor: { color: '#e0e0e0' },
      });
    } catch (err) {
      console.error('Failed to initialize Sigma:', err);
      message = 'WebGL initialization failed.';
      return;
    }

    // Node reducer for expandable visual cue
    sigmaRenderer.setSetting('nodeReducer', (key, data) => {
      const node = nodeIndex.get(key);
      if (!node) return data;
      const res = { ...data };

      // Expandable nodes get a border ring effect (larger size + different border)
      if (node.expandable && !expandedNodes.has(key)) {
        res.borderColor = EXPANDABLE_RING_COLOR;
        res.borderSize = 2;
      }

      // Hover/selected highlighting
      if (selectedNode?.id === key) {
        res.highlighted = true;
        res.zIndex = 2;
      }

      return res;
    });

    // Click handler — expand node
    sigmaRenderer.on('clickNode', ({ node: nodeKey }) => {
      const node = nodeIndex.get(nodeKey);
      if (!node) return;
      selectedNode = node;
      if (node.expandable && !expandedNodes.has(nodeKey)) {
        expandNode(nodeKey);
      }
    });

    // Double-click to expand
    sigmaRenderer.on('doubleClickNode', ({ node: nodeKey }) => {
      const node = nodeIndex.get(nodeKey);
      if (!node) return;
      if (node.expandable && !expandedNodes.has(nodeKey)) {
        expandNode(nodeKey);
      }
    });

    // Right-click to collapse
    sigmaRenderer.on('rightClickNode', ({ node: nodeKey, event }) => {
      event.original.preventDefault();
      if (expandedNodes.has(nodeKey) && nodeKey !== breadcrumbs[0]?.url) {
        collapseNode(nodeKey);
      }
    });

    // Hover
    sigmaRenderer.on('enterNode', ({ node: nodeKey }) => {
      hoveredNode = nodeIndex.get(nodeKey) ?? null;
    });
    sigmaRenderer.on('leaveNode', () => {
      hoveredNode = null;
    });

    // Click stage to deselect
    sigmaRenderer.on('clickStage', () => {
      selectedNode = null;
    });

    // Run ForceAtlas2 layout
    runLayout();
  }

  function runLayout(duration: number = 3000) {
    if (!graphInstance) return;
    stopLayout();
    const settings = forceAtlas2.inferSettings(graphInstance);
    settings.gravity = 1;
    settings.scalingRatio = 2;
    fa2Layout = new FA2Layout(graphInstance, { settings });
    fa2Layout.start();
    setTimeout(() => stopLayout(), duration);
  }

  function stopLayout() {
    if (fa2Layout) {
      fa2Layout.kill();
      fa2Layout = null;
    }
  }

  async function expandNode(nodeURL: string) {
    if (loading || expandedNodes.has(nodeURL)) return;
    loading = true;

    try {
      const data = await api.getCrawlSubgraph(crawlId, nodeURL, 1, 300);
      if (destroyed || !graphInstance || !sigmaRenderer) return;

      // Get position of expanded node for seeded placement
      const parentPos = graphInstance.hasNode(nodeURL)
        ? { x: graphInstance.getNodeAttribute(nodeURL, 'x') as number, y: graphInstance.getNodeAttribute(nodeURL, 'y') as number }
        : { x: 0, y: 0 };

      // Batch new nodes (avoids quadratic array spreading)
      const newNodes: GraphNode[] = [];
      for (const node of data.nodes) {
        if (!nodeIndex.has(node.id)) {
          newNodes.push(node);
          nodeIndex.set(node.id, node);
          graphInstance.addNode(node.id, {
            x: parentPos.x + (Math.random() - 0.5) * 80,
            y: parentPos.y + (Math.random() - 0.5) * 80,
            size: getNodeSize(node),
            color: getNodeColor(node, colorMode),
            label: node.title || safePath(node.id),
          });
        } else {
          // Update expandable status for existing nodes
          const existing = nodeIndex.get(node.id)!;
          existing.expandable = node.expandable;
          existing.totalOutlinks = node.totalOutlinks;
        }
      }
      if (newNodes.length > 0) {
        visibleNodes = [...visibleNodes, ...newNodes];
        maxPageRank = Math.max(maxPageRank, ...newNodes.map(n => n.pageRank ?? 0));
      }

      // Batch new edges
      const newEdges: GraphEdge[] = [];
      for (const edge of data.edges) {
        if (edge.source === edge.target) continue;
        if (graphInstance.hasNode(edge.source) && graphInstance.hasNode(edge.target) &&
            !graphInstance.hasDirectedEdge(edge.source, edge.target)) {
          graphInstance.addDirectedEdge(edge.source, edge.target, {
            color: '#66666644',
            size: 0.5,
          });
          newEdges.push(edge);
        }
      }
      if (newEdges.length > 0) {
        visibleEdges = [...visibleEdges, ...newEdges];
      }

      // Mark as expanded
      const next = new Set(expandedNodes);
      next.add(nodeURL);
      expandedNodes = next;

      // Update the source node's expandable status
      const srcNode = nodeIndex.get(nodeURL);
      if (srcNode) srcNode.expandable = false;

      // Update breadcrumb
      const title = srcNode?.title || safePath(nodeURL);
      breadcrumbs = [...breadcrumbs, { url: nodeURL, title }];

      // Rebuild rank maps
      ({ authorityRank, hubRank } = buildRankMaps(visibleNodes));

      // Re-run layout briefly to integrate new nodes (pin existing)
      runLayout(2000);
      sigmaRenderer.refresh();

      // Update meta
      if (meta) {
        meta = { ...meta, nodesInView: visibleNodes.length };
      }
    } catch (err: any) {
      console.error('Failed to expand node:', err);
    } finally {
      loading = false;
    }
  }

  function collapseNode(nodeURL: string) {
    if (!graphInstance || !sigmaRenderer) return;

    // Remove all nodes that were added when expanding this node
    // Strategy: rebuild from scratch with the node removed from expanded set
    const next = new Set(expandedNodes);
    next.delete(nodeURL);
    expandedNodes = next;

    // Remove breadcrumb entries after this node
    const bcIdx = breadcrumbs.findIndex(b => b.url === nodeURL);
    if (bcIdx >= 0) {
      breadcrumbs = breadcrumbs.slice(0, bcIdx);
    }

    // Full reload from root with current expanded set
    reloadFromExpandedSet();
  }

  async function reloadFromExpandedSet() {
    loading = true;
    try {
      // Start fresh from root
      const rootURL = breadcrumbs[0]?.url || '';
      const data = await api.getCrawlSubgraph(crawlId, rootURL || undefined, 2, 500);
      if (destroyed) return;

      let allNodes = new Map<string, GraphNode>();
      let allEdges: GraphEdge[] = [];

      for (const n of data.nodes) allNodes.set(n.id, n);
      allEdges.push(...data.edges);

      // Re-expand nodes in parallel
      const expandURLs = [...expandedNodes].filter(u => u !== rootURL);
      const results = await Promise.allSettled(
        expandURLs.map(u => api.getCrawlSubgraph(crawlId, u, 1, 300))
      );
      for (const result of results) {
        if (result.status === 'fulfilled') {
          for (const n of result.value.nodes) {
            if (!allNodes.has(n.id)) allNodes.set(n.id, n);
          }
          allEdges.push(...result.value.edges);
        }
      }

      visibleNodes = Array.from(allNodes.values());
      // Deduplicate edges
      const edgeSet = new Set<string>();
      visibleEdges = allEdges.filter(e => {
        const key = `${e.source}\0${e.target}`;
        if (edgeSet.has(key)) return false;
        edgeSet.add(key);
        return true;
      });

      if (meta) meta = { ...meta, nodesInView: visibleNodes.length };
      await tick();
      buildGraph();
    } catch (err: any) {
      console.error('Failed to reload:', err);
    } finally {
      loading = false;
    }
  }

  function resetToRoot() {
    expandedNodes = new Set();
    breadcrumbs = breadcrumbs.slice(0, 1);
    selectedNode = null;
    searchQuery = '';
    searchResults = [];
    loadInitialGraph();
  }

  async function goToSearchResult(result: GraphSearchResult) {
    searchQuery = '';
    searchResults = [];
    // If already visible, just select it
    const existing = nodeIndex.get(result.id);
    if (existing && sigmaRenderer && graphInstance?.hasNode(result.id)) {
      selectedNode = existing;
      const cam = sigmaRenderer.getCamera();
      cam.animate(
        { x: graphInstance.getNodeAttribute(result.id, 'x') as number, y: graphInstance.getNodeAttribute(result.id, 'y') as number, ratio: 0.3 },
        { duration: 400 }
      );
      return;
    }
    // Otherwise, load it as a new root expansion
    loading = true;
    try {
      const data = await api.getCrawlSubgraph(crawlId, result.id, 1, 300);
      if (destroyed) return;
      // Batch merge new nodes
      const newNodes: GraphNode[] = [];
      for (const node of data.nodes) {
        if (!nodeIndex.has(node.id)) {
          nodeIndex.set(node.id, node);
          newNodes.push(node);
        }
      }
      if (newNodes.length > 0) {
        visibleNodes = [...visibleNodes, ...newNodes];
      }
      // Batch merge new edges (with dedup)
      const existingEdges = new Set(visibleEdges.map(e => `${e.source}\0${e.target}`));
      const newEdges: GraphEdge[] = [];
      for (const edge of data.edges) {
        const key = `${edge.source}\0${edge.target}`;
        if (!existingEdges.has(key)) {
          existingEdges.add(key);
          newEdges.push(edge);
        }
      }
      if (newEdges.length > 0) {
        visibleEdges = [...visibleEdges, ...newEdges];
      }
      const next = new Set(expandedNodes);
      next.add(result.id);
      expandedNodes = next;
      breadcrumbs = [...breadcrumbs, { url: result.id, title: result.title || safePath(result.id) }];
      if (meta) meta = { ...meta, nodesInView: visibleNodes.length };
      await tick();
      buildGraph();
      // Focus on the target after rebuild
      setTimeout(() => {
        if (sigmaRenderer && graphInstance?.hasNode(result.id)) {
          selectedNode = nodeIndex.get(result.id) ?? null;
          const cam = sigmaRenderer.getCamera();
          cam.animate(
            { x: graphInstance.getNodeAttribute(result.id, 'x') as number, y: graphInstance.getNodeAttribute(result.id, 'y') as number, ratio: 0.3 },
            { duration: 400 }
          );
        }
      }, 2500);
    } catch (err: any) {
      console.error('Failed to navigate to search result:', err);
    } finally {
      loading = false;
    }
  }

  function navigateBreadcrumb(index: number) {
    if (index === breadcrumbs.length - 1) return; // already here
    // Collapse everything after this breadcrumb
    const kept = new Set<string>();
    for (let i = 0; i <= index; i++) {
      kept.add(breadcrumbs[i].url);
    }
    expandedNodes = kept;
    breadcrumbs = breadcrumbs.slice(0, index + 1);
    reloadFromExpandedSet();
  }

  onMount(() => {
    const ws = createWsClient((msg) => {
      if (msg.type === 'analysis_complete' && analysisPending && graphLoaded) {
        message = 'Loading graph explorer...';
        loadInitialGraph();
      }
    });

    return () => {
      destroyed = true;
      ws.close();
      stopLayout();
      sigmaRenderer?.kill();
      if (searchDebounce) clearTimeout(searchDebounce);
    };
  });
</script>

<div class="rounded-xl border border-border bg-surface-2 p-5">
  {#if !graphLoaded}
    <div class="flex flex-col items-center justify-center py-8 text-center">
      <h3 class="text-sm font-medium text-fg-2 mb-2">Link Explorer</h3>
      <p class="text-xs text-fg-2/70 mb-4">Interactive drill-down visualization. Start from the homepage and explore one level at a time.</p>
      <button
        type="button"
        class="px-5 py-2 rounded-lg bg-accent text-white font-medium hover:bg-accent/90 transition-colors cursor-pointer text-sm"
        onclick={loadInitialGraph}
      >
        Open Explorer
      </button>
    </div>
  {:else}
    <!-- Header -->
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <h3 class="text-sm font-medium text-fg-2">Link Explorer</h3>
        {#if meta}
          {@const pct = Math.min(100, (visibleNodes.length / Math.max(1, meta.totalNodesInGraph)) * 100)}
          <span class="text-[10px] text-fg-2/50 font-mono">
            {visibleNodes.length.toLocaleString()} in view / {meta.totalNodesInGraph.toLocaleString()} total ({pct.toFixed(1)}%)
          </span>
        {/if}
        {#if loading}
          <div class="w-3.5 h-3.5 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
        {/if}
      </div>
      {#if !message}
        <div class="flex items-center gap-2">
          <!-- Color modes -->
          <div class="flex rounded-lg border border-border overflow-hidden text-[10px]">
            <button class="px-2 py-0.5 {colorMode === 'depth' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'depth'}>Depth</button>
            <button class="px-2 py-0.5 {colorMode === 'authority' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'authority'}>Authority</button>
            <button class="px-2 py-0.5 {colorMode === 'hub' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'hub'}>Hub</button>
            <button class="px-2 py-0.5 {colorMode === 'pagerank' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => colorMode = 'pagerank'}>PageRank</button>
          </div>
          <!-- Search -->
          <div class="relative">
            <input type="text" bind:value={searchQuery} placeholder="Search node..." class="w-40 text-xs rounded-lg border border-border bg-surface-3 px-2 py-1 text-fg-1 placeholder:text-fg-2/50 focus:outline-none focus:border-accent" />
            {#if searchQuery}
              <button class="absolute right-1.5 top-1/2 -translate-y-1/2 text-fg-2 hover:text-fg-1 text-xs" onclick={() => { searchQuery = ''; searchResults = []; }}>x</button>
            {/if}
            {#if searchResults.length > 0}
              <div class="absolute top-full mt-1 right-0 w-72 max-h-60 overflow-y-auto rounded-lg border border-border bg-surface-3 shadow-lg z-50">
                {#each searchResults as result}
                  <button
                    class="w-full text-left px-3 py-2 text-xs hover:bg-surface-1 border-b border-border/50 last:border-b-0"
                    onclick={() => goToSearchResult(result)}
                  >
                    <div class="font-mono text-fg-1 truncate">{safePath(result.id)}</div>
                    {#if result.title}
                      <div class="text-fg-2/70 truncate mt-0.5">{result.title}</div>
                    {/if}
                    <div class="flex gap-2 mt-0.5 text-fg-2/50">
                      <span>Depth {result.depth}</span>
                      <span>PR {result.pageRank.toFixed(3)}</span>
                    </div>
                  </button>
                {/each}
              </div>
            {/if}
          </div>
          <!-- Reset -->
          <button
            class="px-2 py-0.5 text-[10px] rounded-lg border border-border bg-surface-3 text-fg-2 hover:bg-surface-1"
            onclick={resetToRoot}
          >
            Reset
          </button>
        </div>
      {/if}
    </div>

    <!-- Coverage bar -->
    {#if meta && !message}
      {@const pct = Math.min(100, (visibleNodes.length / Math.max(1, meta.totalNodesInGraph)) * 100)}
      <div class="w-full h-1.5 rounded-full bg-surface-1 mb-3 overflow-hidden">
        <div class="h-full rounded-full bg-accent transition-all duration-500" style="width: {pct}%"></div>
      </div>
    {/if}

    <!-- Breadcrumbs -->
    {#if breadcrumbs.length > 0 && !message}
      <div class="flex items-center gap-1 mb-3 text-[10px] text-fg-2 overflow-x-auto">
        {#each breadcrumbs as bc, i}
          {#if i > 0}
            <span class="text-fg-2/40">/</span>
          {/if}
          <button
            class="hover:text-accent font-mono truncate max-w-[160px] {i === breadcrumbs.length - 1 ? 'text-fg-1 font-medium' : ''}"
            onclick={() => navigateBreadcrumb(i)}
            title={bc.url}
          >
            {bc.title || safePath(bc.url)}
          </button>
        {/each}
      </div>
    {/if}

    <!-- Graph container -->
    {#if message}
      <p class="text-sm text-fg-2 text-center py-8">{message}</p>
    {:else}
      <div class="relative">
        <div bind:this={container} class="w-full rounded-lg bg-surface-1" style="height: 500px;"></div>

        <!-- Legend -->
        <div class="absolute top-2 right-2 rounded-lg border border-border bg-surface-3/90 p-2 text-[11px] text-fg-2 space-y-0.5 z-10">
          {#if colorMode === 'depth'}
            {#each Array.from({ length: Math.min((meta?.hops ?? 3) + 2, 6) }, (_, i) => i) as d}
              <div class="flex items-center gap-1.5">
                <span class="w-2.5 h-2.5 rounded-full inline-block shrink-0" style="background: {DEPTH_COLORS[d]}"></span>
                Depth {d}
              </div>
            {/each}
          {:else if colorMode === 'authority'}
            <div class="flex items-center gap-1.5">
              <div class="w-20 h-2.5 rounded-full" style="background: linear-gradient(to right, rgb(255,255,50), rgb(255,165,25), rgb(255,75,0))"></div>
            </div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20">
              <span>Low</span><span>High</span>
            </div>
            <div class="text-[10px] text-fg-2/70 mt-0.5">Authority Score</div>
          {:else if colorMode === 'hub'}
            <div class="flex items-center gap-1.5">
              <div class="w-20 h-2.5 rounded-full" style="background: linear-gradient(to right, rgb(200,230,100), rgb(100,190,180), rgb(30,150,255))"></div>
            </div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20">
              <span>Low</span><span>High</span>
            </div>
            <div class="text-[10px] text-fg-2/70 mt-0.5">Hub Score</div>
          {:else}
            <div class="flex items-center gap-1.5">
              <div class="w-20 h-2.5 rounded-full" style="background: linear-gradient(to right, rgb(80,80,200), rgb(170,60,150), rgb(255,50,50))"></div>
            </div>
            <div class="flex justify-between text-[9px] text-fg-2/60 w-20">
              <span>Low</span><span>High</span>
            </div>
            <div class="text-[10px] text-fg-2/70 mt-0.5">PageRank</div>
          {/if}
          <div class="flex items-center gap-1.5 mt-1 pt-1 border-t border-border/50">
            <span class="w-2.5 h-2.5 rounded-full inline-block shrink-0 border-2" style="border-color: {EXPANDABLE_RING_COLOR}; background: transparent;"></span>
            <span class="text-[10px]">Expandable</span>
          </div>
        </div>

        <!-- Interaction hints -->
        <div class="absolute bottom-2 left-2 rounded-lg border border-border bg-surface-3/90 px-2 py-1 text-[10px] text-fg-2/60 z-10">
          Click node to expand · Right-click to collapse
        </div>
      </div>

      <!-- Hover tooltip -->
      {#if hoveredNode && !selectedNode}
        <div class="mt-2 text-xs text-fg-2 flex flex-wrap gap-x-3">
          <span class="font-mono text-fg-1">{safePath(hoveredNode.id)}</span>
          {#if hoveredNode.title}
            <span>{hoveredNode.title.substring(0, 60)}</span>
          {/if}
          <span>Depth: {hoveredNode.depth === null ? 'unreachable' : hoveredNode.depth}</span>
          <span>Outlinks: {hoveredNode.totalOutlinks ?? hoveredNode.outDegree}</span>
          {#if hoveredNode.expandable}
            <span class="text-yellow-400">Click to expand</span>
          {/if}
        </div>
      {/if}

      <!-- Selected node detail panel -->
      {#if selectedNode}
        <div class="mt-3 rounded-lg border border-border bg-surface-3 p-3 text-xs">
          <div class="flex items-start justify-between mb-2">
            <div>
              <div class="font-mono text-fg-1 font-medium">{safePath(selectedNode.id)}</div>
              {#if selectedNode.title}
                <div class="text-fg-2 mt-0.5">{selectedNode.title}</div>
              {/if}
            </div>
            <div class="flex items-center gap-1 shrink-0 ml-2">
              {#if selectedNode.expandable && !expandedNodes.has(selectedNode.id)}
                <button
                  class="px-2 py-0.5 rounded bg-accent/20 text-accent text-[10px] hover:bg-accent/30"
                  onclick={() => expandNode(selectedNode!.id)}
                  disabled={loading}
                >
                  Expand ({selectedNode.totalOutlinks ?? '?'} links)
                </button>
              {/if}
              {#if expandedNodes.has(selectedNode.id) && selectedNode.id !== breadcrumbs[0]?.url}
                <button
                  class="px-2 py-0.5 rounded bg-red-500/20 text-red-400 text-[10px] hover:bg-red-500/30"
                  onclick={() => collapseNode(selectedNode!.id)}
                >
                  Collapse
                </button>
              {/if}
              <button class="text-fg-2 hover:text-fg-1" onclick={() => selectedNode = null}>x</button>
            </div>
          </div>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-fg-2">
            <span>Depth: <strong class="text-fg-1">{selectedNode.depth === null ? 'unreachable' : selectedNode.depth}</strong></span>
            <span>PageRank: <strong class="text-fg-1">{(selectedNode.pageRank ?? 0).toFixed(4)}</strong></span>
            <span>Authority: <strong class="text-fg-1">{selectedNode.authority.toFixed(2)}</strong></span>
            <span>Hub: <strong class="text-fg-1">{selectedNode.hub.toFixed(2)}</strong></span>
            <span>Centrality: <strong class="text-fg-1">{selectedNode.centrality.toFixed(4)}</strong></span>
            <span>Inlinks: <strong class="text-fg-1">{selectedNode.inDegree}</strong></span>
            <span>Outlinks: <strong class="text-fg-1">{selectedNode.totalOutlinks ?? selectedNode.outDegree}</strong></span>
          </div>
        </div>
      {/if}
    {/if}
  {/if}
</div>
