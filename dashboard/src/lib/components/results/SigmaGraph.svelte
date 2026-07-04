<script lang="ts">
  import { onMount } from 'svelte';
  import { DirectedGraph } from 'graphology';
  import Sigma from 'sigma';
  import type { NodeDisplayData } from 'sigma/types';
  import FA2Layout from 'graphology-layout-forceatlas2/worker';
  import forceAtlas2 from 'graphology-layout-forceatlas2';
  import type { GraphNode, GraphEdge, GraphMeta, BundledEdge } from '../../types/graph.js';
  import { buildRankMaps } from '../../utils/graph.js';

  let {
    nodes,
    edges,
    meta,
    colorMode,
    edgeMode,
    bundledEdges,
    searchQuery,
    searchMatchIds,
    hiddenDepths,
    onSelectNode,
    onHoverNode,
  }: {
    nodes: GraphNode[];
    edges: GraphEdge[];
    meta: GraphMeta;
    colorMode: 'depth' | 'authority' | 'hub';
    edgeMode: 'normal' | 'bundled';
    bundledEdges: BundledEdge[];
    searchQuery: string;
    searchMatchIds: Set<string> | null;
    hiddenDepths: Set<number | null>;
    onSelectNode: (node: GraphNode | null) => void;
    onHoverNode: (node: GraphNode | null) => void;
  } = $props();

  let container: HTMLDivElement;
  let hoveredNodeKey: string | null = $state(null);
  let selectedNodeKey: string | null = $state(null);

  const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#1abc9c', '#e67e22', '#e74c3c', '#3498db', '#2ecc71'];
  const UNREACHABLE_COLOR = '#555555';

  // Semantic zoom thresholds
  const ZOOM_OVERVIEW_RATIO = 1.5;   // camera ratio above this = zoomed out
  const ZOOM_DETAIL_RATIO = 0.4;     // camera ratio below this = zoomed in
  const IMPORTANCE_LABEL_CUTOFF = 0.7;  // overview: hide labels below this
  const IMPORTANCE_DIM_CUTOFF = 0.3;    // overview: dim color below this
  const IMPORTANCE_EDGE_CUTOFF = 0.5;   // overview: hide edges below this

  // Node index by key for fast lookup
  let nodeIndex = new Map<string, GraphNode>();

  // Percentile rank maps for authority/hub (computed once on mount)
  let authorityRank = new Map<string, number>(); // 0–1
  let hubRank = new Map<string, number>();         // 0–1


  function getNodeColor(node: GraphNode, mode: 'depth' | 'authority' | 'hub'): string {
    if (mode === 'authority') {
      const t = authorityRank.get(node.id) ?? 0;
      // YlOrRd-ish gradient: yellow → orange → red
      const r = Math.round(255);
      const g = Math.round(255 - t * 180);
      const b = Math.round(50 - t * 50);
      return `rgb(${r},${g},${b})`;
    }
    if (mode === 'hub') {
      const t = hubRank.get(node.id) ?? 0;
      // YlGnBu-ish gradient: yellow-green → blue
      const r = Math.round(200 - t * 170);
      const g = Math.round(230 - t * 80);
      const b = Math.round(100 + t * 155);
      return `rgb(${r},${g},${b})`;
    }
    // depth mode
    return node.depth === null ? UNREACHABLE_COLOR : DEPTH_COLORS[node.depth % DEPTH_COLORS.length];
  }

  function getNodeSize(node: GraphNode, mode: 'depth' | 'authority' | 'hub'): number {
    if (mode === 'hub') {
      const t = hubRank.get(node.id) ?? 0;
      return Math.max(3, Math.min(12, 3 + t * 9));
    }
    if (mode === 'authority') {
      const t = authorityRank.get(node.id) ?? 0;
      return Math.max(3, Math.min(12, 3 + t * 9));
    }
    // depth mode: size by inDegree (neutral structural metric)
    const maxIn = meta.maxInDegree || 1;
    return Math.max(3, Math.min(12, 3 + (node.inDegree / maxIn) * 9));
  }

  function safePath(url: string): string {
    try { return new URL(url).pathname; } catch { return url; }
  }

  // Keep a reference to renderer and layout for reactivity and cleanup
  let sigmaRenderer: Sigma | null = null;
  let fa2Layout: FA2Layout | null = null;
  let graphInstance: DirectedGraph | null = null;

  // Plain JS state bridge: $effect syncs reactive props into these so
  // that Sigma callbacks (which run outside Svelte's reactive context)
  // always see current values.
  let _searchQuery = '';
  let _searchMatchIds: Set<string> | null = null;
  let _hiddenDepths = new Set<number | null>();
  let _edgeMode: 'normal' | 'bundled' = 'normal';
  let _colorMode: 'depth' | 'authority' | 'hub' = 'depth';

  // Track which bundle meta-nodes/edges we've added so we can remove them
  const BUNDLE_NODE_PREFIX = '__bundle__';
  const BUNDLE_EDGE_PREFIX = '__bedge__';
  let activeBundleKeys = new Set<string>();

  // Cached prefix positions (computed once after layout, reused on toggle)
  let cachedPrefixPositions: Map<string, { x: number; y: number; count: number }> | null = null;

  // Semantic zoom state
  type ZoomLevel = 'overview' | 'normal' | 'detail';
  let _zoomLevel: ZoomLevel = 'normal';
  let _cameraRatio = 1;
  let zoomLabel = $state('');

  // Pre-computed node importance (authority percentile rank)
  let nodeImportanceRank = new Map<string, number>(); // 0-1, 1 = most important

  // Sync colorMode into plain JS var and refresh Sigma so the node reducer picks it up
  $effect(() => {
    _colorMode = colorMode;
    if (sigmaRenderer) sigmaRenderer.refresh({ skipIndexation: true });
  });

  // Sync reactive props into plain JS vars and refresh Sigma
  $effect(() => {
    _searchQuery = searchQuery;
    _searchMatchIds = searchMatchIds;
    _hiddenDepths = hiddenDepths;
    if (sigmaRenderer) sigmaRenderer.refresh({ skipIndexation: true });
  });

  // Sync edge mode: add/remove bundle meta-nodes and meta-edges
  $effect(() => {
    // Read reactive props FIRST so they're always tracked as dependencies
    const mode = edgeMode;
    const bundles = bundledEdges;
    _edgeMode = mode;
    if (!sigmaRenderer || !graphInstance) return;

    // Remove existing bundle nodes/edges
    for (const key of activeBundleKeys) {
      if (graphInstance.hasEdge(key)) graphInstance.dropEdge(key);
    }
    for (const key of activeBundleKeys) {
      if (graphInstance.hasNode(key)) graphInstance.dropNode(key);
    }
    activeBundleKeys.clear();

    if (mode === 'bundled' && bundles.length > 0) {
      // Compute prefix positions (cached to avoid O(n) on repeated toggles)
      if (!cachedPrefixPositions) {
        cachedPrefixPositions = new Map();
        graphInstance.forEachNode((key, attrs) => {
          const node = nodeIndex.get(key);
          if (!node) return;
          let prefix: string;
          try {
            const segments = new URL(node.id).pathname.split('/').filter(Boolean);
            prefix = '/' + (segments[0] || '');
          } catch { prefix = '/'; }

          const entry = cachedPrefixPositions!.get(prefix);
          if (entry) {
            entry.x += attrs.x as number;
            entry.y += attrs.y as number;
            entry.count++;
          } else {
            cachedPrefixPositions!.set(prefix, { x: attrs.x as number, y: attrs.y as number, count: 1 });
          }
        });
      }

      // Add bundle meta-nodes at centroid of their prefix group
      for (const [prefix, pos] of cachedPrefixPositions) {
        const nodeKey = BUNDLE_NODE_PREFIX + prefix;
        if (graphInstance.hasNode(nodeKey)) continue;
        const size = Math.max(8, Math.min(25, 5 + Math.log2(pos.count) * 4));
        graphInstance.addNode(nodeKey, {
          x: pos.x / pos.count,
          y: pos.y / pos.count,
          size,
          color: '#4361ee',
          label: `${prefix} (${pos.count})`,
        });
        activeBundleKeys.add(nodeKey);
      }

      // Add bundle meta-edges
      const maxCount = Math.max(1, ...bundles.map(b => b.count));
      for (const bundle of bundles) {
        const srcKey = BUNDLE_NODE_PREFIX + bundle.sourcePrefix;
        const tgtKey = BUNDLE_NODE_PREFIX + bundle.targetPrefix;
        if (!graphInstance.hasNode(srcKey) || !graphInstance.hasNode(tgtKey)) continue;
        const edgeKey = BUNDLE_EDGE_PREFIX + bundle.sourcePrefix + '\0' + bundle.targetPrefix;
        if (graphInstance.hasEdge(edgeKey)) continue;
        const thickness = 1 + (bundle.count / maxCount) * 5;
        graphInstance.addDirectedEdgeWithKey(edgeKey, srcKey, tgtKey, {
          color: '#4361ee66',
          size: thickness,
        });
        activeBundleKeys.add(edgeKey);
      }
    }

    sigmaRenderer.refresh();
  });

  onMount(() => {
    const graph = new DirectedGraph();
    graphInstance = graph;

    // Build node index and percentile rank maps
    nodeIndex = new Map(nodes.map(n => [n.id, n]));
    ({ authorityRank, hubRank } = buildRankMaps(nodes));

    // Add nodes with random initial positions (required for ForceAtlas2)
    const spread = Math.sqrt(nodes.length) * 20;
    for (const node of nodes) {
      graph.addNode(node.id, {
        x: (Math.random() - 0.5) * spread,
        y: (Math.random() - 0.5) * spread,
        size: getNodeSize(node, colorMode),
        color: getNodeColor(node, colorMode),
        label: node.title || safePath(node.id),
      });
    }

    // Add edges (deduplicated, skip self-links, cap per-node outDegree for layout)
    const maxLayoutEdges = nodes.length > 500 ? 3 : 15;
    const outCount = new Map<string, number>();
    for (const edge of edges) {
      if (edge.source === edge.target) continue;
      const srcOut = outCount.get(edge.source) || 0;
      if (srcOut >= maxLayoutEdges) continue;
      if (graph.hasNode(edge.source) && graph.hasNode(edge.target) && !graph.hasDirectedEdge(edge.source, edge.target)) {
        graph.addDirectedEdge(edge.source, edge.target, {
          color: '#66666633',
          size: 0.4,
        });
        outCount.set(edge.source, srcOut + 1);
      }
    }

    // Initialize Sigma renderer (WebGL)
    try {
      sigmaRenderer = new Sigma(graph, container, {
        defaultEdgeColor: '#66666633',
        defaultNodeColor: '#999',
        labelFont: 'ui-monospace, monospace',
        labelSize: 11,
        labelWeight: 'normal',
        labelRenderedSizeThreshold: 8,
        renderLabels: true,
        renderEdgeLabels: false,
        hideEdgesOnMove: nodes.length > 2000,
        hideLabelsOnMove: nodes.length > 2000,
        minEdgeThickness: 0.5,
        stagePadding: 30,
        autoRescale: true,
        autoCenter: true,
        enableCameraZooming: true,
        enableCameraPanning: true,
        zoomingRatio: 1.5,
        // @ts-ignore - labelColor may not be in older type defs
        labelColor: { color: '#e0e0e0' },
      });
    } catch (err) {
      console.error('Failed to initialize WebGL renderer:', err);
      return;
    }

    // Pre-compute importance ranks for semantic zoom
    const sortedByAuth = [...nodes].sort((a, b) => b.authority - a.authority || b.centrality - a.centrality);
    for (let i = 0; i < sortedByAuth.length; i++) {
      nodeImportanceRank.set(sortedByAuth[i].id, 1 - i / Math.max(1, sortedByAuth.length - 1));
    }

    // Semantic zoom: listen to camera changes
    const camera = sigmaRenderer.getCamera();
    let zoomThrottleTimer: ReturnType<typeof setTimeout> | null = null;
    camera.on('updated', (state: { ratio: number }) => {
      _cameraRatio = state.ratio;
      if (zoomThrottleTimer) return;
      zoomThrottleTimer = setTimeout(() => {
        zoomThrottleTimer = null;
        const prev = _zoomLevel;
        if (_cameraRatio > ZOOM_OVERVIEW_RATIO) _zoomLevel = 'overview';
        else if (_cameraRatio <= ZOOM_DETAIL_RATIO) _zoomLevel = 'detail';
        else _zoomLevel = 'normal';

        if (prev !== _zoomLevel) {
          zoomLabel = _zoomLevel === 'overview' ? 'Overview' : _zoomLevel === 'detail' ? 'Detail' : '';
          sigmaRenderer!.refresh({ skipIndexation: true });
        }
      }, 60);
    });

    // Pre-compute hovered neighbor set for fast O(1) lookups in reducers
    let hoveredNeighborSet = new Set<string>();

    // Node reducer for dynamic styling (search, depth filter, hover, zoom, bundles)
    // Optimized: avoids object spread when no mutations needed, caches lookups
    sigmaRenderer.setSetting('nodeReducer', (key: string, data: Partial<NodeDisplayData>) => {
      // Bundle meta-nodes: show only in bundled mode
      if (key.startsWith(BUNDLE_NODE_PREFIX)) {
        if (_edgeMode !== 'bundled') return { ...data, hidden: true };
        return { ...data, forceLabel: true };
      }

      const node = nodeIndex.get(key);
      if (!node) return data;

      // Always compute color/size from current mode (graphology stores initial values only)
      const baseColor = getNodeColor(node, _colorMode);
      const baseSize = getNodeSize(node, _colorMode);

      // Early exit: if nothing interactive is active, return with correct color/size
      const hasInteraction = hoveredNodeKey || selectedNodeKey || _searchQuery || _edgeMode === 'bundled' || _zoomLevel !== 'normal' || _hiddenDepths.size > 0;
      if (!hasInteraction) return { ...data, color: baseColor, size: baseSize };

      const res = { ...data, color: baseColor, size: baseSize };
      const importance = nodeImportanceRank.get(key) ?? 0;

      // Depth filter
      if (_hiddenDepths.has(node.depth)) {
        res.hidden = true;
        return res;
      }

      // Semantic zoom: adjust node appearance based on zoom level
      if (_zoomLevel === 'overview') {
        if (importance < IMPORTANCE_LABEL_CUTOFF) {
          res.label = '';
          res.size = (res.size ?? 3) * 0.6;
          if (importance < IMPORTANCE_DIM_CUTOFF) {
            res.color = '#333333';
          }
        } else {
          res.size = (res.size ?? 3) * 1.5;
          res.zIndex = 1;
        }
      } else if (_zoomLevel === 'detail' && nodes.length <= 2000) {
        res.forceLabel = true;
      }

      // Search filter (uses worker-computed match set, falls back to inline match)
      if (_searchQuery) {
        if (_searchMatchIds) {
          if (!_searchMatchIds.has(key)) {
            res.color = '#333333';
            res.label = '';
            res.zIndex = 0;
          } else {
            res.highlighted = true;
            res.zIndex = 1;
            res.label = data.label;
          }
        } else {
          const q = _searchQuery.toLowerCase();
          const path = safePath(node.id);
          if (!path.toLowerCase().includes(q) && !node.id.toLowerCase().includes(q)) {
            res.color = '#333333';
            res.label = '';
            res.zIndex = 0;
          } else {
            res.highlighted = true;
            res.zIndex = 1;
            res.label = data.label;
          }
        }
      }

      // Hover highlighting — uses pre-computed neighbor set for O(1) lookup
      if (hoveredNodeKey) {
        if (key === hoveredNodeKey) {
          res.highlighted = true;
          res.zIndex = 2;
        } else if (hoveredNeighborSet.has(key)) {
          res.zIndex = 1;
        } else {
          res.color = '#333333';
          res.label = '';
          res.zIndex = 0;
        }
      }

      // Selected node
      if (selectedNodeKey === key) {
        res.highlighted = true;
        res.zIndex = 2;
      }

      // Bundled mode: dim real nodes except hovered/selected + neighbors
      if (_edgeMode === 'bundled') {
        const isRelevant = key === hoveredNodeKey || key === selectedNodeKey || hoveredNeighborSet.has(key);
        if (!isRelevant) {
          res.color = '#444444';
          res.size = (res.size ?? 3) * 0.5;
          res.label = '';
          res.zIndex = 0;
        }
      }

      return res;
    });

    // Edge reducer for hover/search/filter/zoom/bundles
    sigmaRenderer.setSetting('edgeReducer', (key, data) => {
      const res = { ...data };
      const extremities = graph.extremities(key);
      const [source, target] = extremities;

      // Bundle meta-edges: only show in bundled mode
      if (key.startsWith(BUNDLE_EDGE_PREFIX)) {
        if (_edgeMode !== 'bundled') { res.hidden = true; return res; }
        return res;
      }

      // Bundled mode: hide all regular edges
      if (_edgeMode === 'bundled') {
        res.hidden = true;
        return res;
      }

      // Hide edges connected to hidden-depth nodes
      const sNode = nodeIndex.get(source);
      const tNode = nodeIndex.get(target);
      if (sNode && _hiddenDepths.has(sNode.depth)) { res.hidden = true; return res; }
      if (tNode && _hiddenDepths.has(tNode.depth)) { res.hidden = true; return res; }

      // Semantic zoom: edge visibility
      if (_zoomLevel === 'overview') {
        // Overview: only show edges between important nodes
        const sImportance = nodeImportanceRank.get(source) ?? 0;
        const tImportance = nodeImportanceRank.get(target) ?? 0;
        if (sImportance < IMPORTANCE_EDGE_CUTOFF && tImportance < IMPORTANCE_EDGE_CUTOFF) {
          res.hidden = true;
          return res;
        }
        res.color = '#44444422';
        res.size = 0.3;
      } else if (_zoomLevel === 'detail') {
        // Detail: make edges more visible
        res.color = '#66666655';
        res.size = 0.6;
      }

      // Hover: highlight connected edges
      if (hoveredNodeKey) {
        if (source === hoveredNodeKey || target === hoveredNodeKey) {
          res.color = '#4361ee';
          res.size = 1.5;
          res.zIndex = 1;
        } else {
          res.hidden = true;
        }
      }

      return res;
    });

    // Events
    sigmaRenderer.on('enterNode', ({ node }) => {
      hoveredNodeKey = node;
      // Pre-compute neighbor set for O(1) lookup in reducers
      hoveredNeighborSet = new Set(graph.neighbors(node));
      const nodeData = nodeIndex.get(node) || null;
      onHoverNode(nodeData);
      sigmaRenderer!.refresh({ skipIndexation: true });
    });

    sigmaRenderer.on('leaveNode', () => {
      hoveredNodeKey = null;
      hoveredNeighborSet.clear();
      onHoverNode(null);
      sigmaRenderer!.refresh({ skipIndexation: true });
    });

    sigmaRenderer.on('clickNode', ({ node }) => {
      if (selectedNodeKey === node) {
        selectedNodeKey = null;
        onSelectNode(null);
      } else {
        selectedNodeKey = node;
        onSelectNode(nodeIndex.get(node) || null);
      }
      sigmaRenderer!.refresh({ skipIndexation: true });
    });

    sigmaRenderer.on('clickStage', () => {
      selectedNodeKey = null;
      onSelectNode(null);
      sigmaRenderer!.refresh({ skipIndexation: true });
    });

    // Start ForceAtlas2 layout in Web Worker
    const settings = forceAtlas2.inferSettings(graph);
    fa2Layout = new FA2Layout(graph, {
      settings: {
        ...settings,
        barnesHutOptimize: nodes.length > 500,
        barnesHutTheta: 0.8,
        gravity: 0.005,
        scalingRatio: (settings.scalingRatio ?? 1) * 15,
        slowDown: Math.max(2, Math.log10(nodes.length) * 2),
        linLogMode: true,
        strongGravityMode: false,
        outboundAttractionDistribution: true,
        adjustSizes: true,
      },
    });
    fa2Layout.start();

    // Stop layout after convergence (longer for better spread)
    const layoutDuration = Math.min(15000, 4000 + nodes.length * 3);
    const layoutTimer = setTimeout(() => {
      fa2Layout?.stop();
    }, layoutDuration);

    return () => {
      clearTimeout(layoutTimer);
      if (zoomThrottleTimer) clearTimeout(zoomThrottleTimer);
      try { fa2Layout?.kill(); } catch {}
      try { sigmaRenderer?.kill(); } catch {}
      graphInstance = null;
      hoveredNeighborSet.clear();
      nodeImportanceRank.clear();
      nodeIndex.clear();
    };
  });
</script>

<div class="relative">
  <div bind:this={container} class="w-full rounded-lg bg-surface-1" style="height: 500px;"></div>
  {#if zoomLabel}
    <div class="absolute bottom-2 left-2 text-[10px] font-mono px-2 py-0.5 rounded bg-surface-3/80 text-fg-2/70 border border-border/50 pointer-events-none transition-opacity duration-300">
      {zoomLabel}
    </div>
  {/if}
</div>
