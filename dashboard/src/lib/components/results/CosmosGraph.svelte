<script lang="ts">
  import { onMount } from 'svelte';
  import { Graph } from '@cosmos.gl/graph';
  import type { GraphNode, GraphEdge, GraphMeta } from '../../types/graph.js';
  import { buildRankMaps as buildRankMapsUtil } from '../../utils/graph.js';

  let {
    nodes,
    edges,
    meta,
    colorMode,
    searchMatchIds,
    hiddenDepths,
    onSelectNode,
    onHoverNode,
  }: {
    nodes: GraphNode[];
    edges: GraphEdge[];
    meta: GraphMeta;
    colorMode: 'depth' | 'authority' | 'hub';
    searchMatchIds: Set<string> | null;
    hiddenDepths: Set<number | null>;
    onSelectNode: (node: GraphNode | null) => void;
    onHoverNode: (node: GraphNode | null) => void;
  } = $props();

  let container: HTMLDivElement;
  // Not $state — intentionally plain variable. The $effect below reacts to
  // colorMode/searchMatchIds/hiddenDepths, not to cosmosInstance being set.
  let cosmosInstance: Graph | null = null;
  let selectedIndex: number | null = $state(null);
  let zoomLabel = $state('');
  let initError = $state('');

  const DEPTH_COLORS: [number, number, number][] = [
    [67, 97, 238],    // #4361ee
    [46, 196, 182],   // #2ec4b6
    [245, 166, 35],   // #f5a623
    [230, 57, 70],    // #e63946
    [155, 89, 182],   // #9b59b6
    [26, 188, 156],   // #1abc9c
    [230, 126, 34],   // #e67e22
    [231, 76, 60],    // #e74c3c
    [52, 152, 219],   // #3498db
    [46, 204, 113],   // #2ecc71
  ];
  const UNREACHABLE_RGB: [number, number, number] = [85, 85, 85];
  const DIMMED_RGB: [number, number, number] = [51, 51, 51];

  let urlToIndex = new Map<string, number>();

  // Percentile rank maps for authority/hub (computed once on mount)
  let authorityRank = new Map<string, number>(); // 0–1
  let hubRank = new Map<string, number>();         // 0–1


  function getNodeColorRgb(node: GraphNode, mode: 'depth' | 'authority' | 'hub'): [number, number, number] {
    if (mode === 'authority') {
      const t = authorityRank.get(node.id) ?? 0;
      return [255, Math.round(255 - t * 180), Math.round(50 - t * 50)];
    }
    if (mode === 'hub') {
      const t = hubRank.get(node.id) ?? 0;
      return [Math.round(200 - t * 170), Math.round(230 - t * 80), Math.round(100 + t * 155)];
    }
    if (node.depth === null) return UNREACHABLE_RGB;
    return DEPTH_COLORS[node.depth % DEPTH_COLORS.length];
  }

  function getNodeSize(node: GraphNode): number {
    const t = authorityRank.get(node.id) ?? 0;
    return Math.max(3, Math.min(12, 3 + t * 9));
  }

  // Reusable buffers — allocated once, updated in-place to avoid GC pressure
  let colorBuffer: Float32Array | null = null;
  let sizeBuffer: Float32Array | null = null;

  function buildColorArray(mode: 'depth' | 'authority' | 'hub', matchIds: Set<string> | null, hidden: Set<number | null>): Float32Array {
    if (!colorBuffer || colorBuffer.length !== nodes.length * 4) {
      colorBuffer = new Float32Array(nodes.length * 4);
    }
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      const isHidden = hidden.has(node.depth);
      const isDimmed = matchIds !== null && !matchIds.has(node.id);
      let r: number, g: number, b: number, a: number;

      if (isHidden) {
        [r, g, b] = DIMMED_RGB;
        a = 0;
      } else if (isDimmed) {
        [r, g, b] = DIMMED_RGB;
        a = 0.3;
      } else {
        [r, g, b] = getNodeColorRgb(node, mode);
        a = 1;
      }

      colorBuffer[i * 4] = r;
      colorBuffer[i * 4 + 1] = g;
      colorBuffer[i * 4 + 2] = b;
      colorBuffer[i * 4 + 3] = a;
    }
    return colorBuffer;
  }

  function buildSizeArray(hidden: Set<number | null>): Float32Array {
    if (!sizeBuffer || sizeBuffer.length !== nodes.length) {
      sizeBuffer = new Float32Array(nodes.length);
    }
    for (let i = 0; i < nodes.length; i++) {
      sizeBuffer[i] = hidden.has(nodes[i].depth) ? 0 : getNodeSize(nodes[i]);
    }
    return sizeBuffer;
  }

  // React to colorMode, searchMatchIds, hiddenDepths changes.
  // Uses render(0) to update visuals without restarting the force simulation.
  $effect(() => {
    if (!cosmosInstance) return;
    const colors = buildColorArray(colorMode, searchMatchIds, hiddenDepths);
    cosmosInstance.setPointColors(colors);
    const sizes = buildSizeArray(hiddenDepths);
    cosmosInstance.setPointSizes(sizes);
    cosmosInstance.render(0); // single-frame render, no simulation restart
  });

  onMount(() => {
    urlToIndex = new Map(nodes.map((n, i) => [n.id, i]));
    ({ authorityRank, hubRank } = buildRankMapsUtil(nodes));

    // Convert edges to Float32Array of index pairs, deduplicating (#3)
    const seen = new Set<string>();
    const edgePairs: number[] = [];
    for (const edge of edges) {
      const si = urlToIndex.get(edge.source);
      const ti = urlToIndex.get(edge.target);
      if (si !== undefined && ti !== undefined && si !== ti) {
        const key = `${si}:${ti}`;
        if (!seen.has(key)) {
          seen.add(key);
          edgePairs.push(si, ti);
        }
      }
    }
    const linksArray = new Float32Array(edgePairs);

    const colors = buildColorArray(colorMode, searchMatchIds, hiddenDepths);
    const sizes = buildSizeArray(hiddenDepths);

    // (#10) Wrap in try/catch for WebGL2 initialization failures
    try {
      cosmosInstance = new Graph(container, {
        backgroundColor: '#1a1a2e',
        pointDefaultColor: '#999999',
        pointDefaultSize: 4,
        pointSizeScale: 1,
        pointGreyoutOpacity: 0.15,
        linkDefaultColor: '#66666633',
        linkDefaultWidth: 0.5,
        linkGreyoutOpacity: 0.05,
        linkDefaultArrows: false,
        renderLinks: true,
        enableDrag: true,
        fitViewOnInit: true,
        fitViewDelay: 500,
        fitViewPadding: 0.1,
        renderHoveredPointRing: true,
        hoveredPointRingColor: '#ffffff',
        hoveredPointCursor: 'pointer',
        scalePointsOnZoom: false,
        // (#7) Use device pixel ratio, capped at 2 for GPU memory efficiency
        pixelRatio: Math.min(window.devicePixelRatio || 1, 2),
        // Simulation config
        simulationFriction: 0.85,
        simulationGravity: 0.25,
        simulationRepulsion: 1.0,
        simulationLinkSpring: 1,
        simulationLinkDistance: 10,
        simulationDecay: 5000,
        // Events
        onPointClick: (index: number) => {
          if (selectedIndex === index) {
            selectedIndex = null;
            cosmosInstance!.unselectPoints();
            onSelectNode(null);
          } else {
            selectedIndex = index;
            cosmosInstance!.selectPointByIndex(index, true);
            onSelectNode(nodes[index] ?? null);
          }
        },
        onBackgroundClick: () => {
          selectedIndex = null;
          cosmosInstance!.unselectPoints();
          onSelectNode(null);
        },
        onPointMouseOver: (index: number) => {
          onHoverNode(nodes[index] ?? null);
        },
        onPointMouseOut: () => {
          onHoverNode(null);
        },
        onZoom: () => {
          if (!cosmosInstance) return;
          const level = cosmosInstance.getZoomLevel();
          if (level < 0.5) {
            zoomLabel = 'Overview';
          } else if (level > 3) {
            zoomLabel = 'Detail';
          } else {
            zoomLabel = '';
          }
        },
        attribution: '',
      });
    } catch (err) {
      initError = `GPU renderer failed: ${err instanceof Error ? err.message : String(err)}`;
      console.error('Failed to initialize cosmos.gl:', err);
      return;
    }

    // Set initial random positions first — this creates the pointIndices GPU buffer
    // that WebGL shaders require. Without it, regl throws "missing buffer for attribute pointIndices".
    const positions = new Float32Array(nodes.length * 2);
    for (let i = 0; i < positions.length; i++) {
      positions[i] = (Math.random() - 0.5) * 1000;
    }
    cosmosInstance.setPointPositions(positions);
    cosmosInstance.setPointColors(colors);
    cosmosInstance.setPointSizes(sizes);
    cosmosInstance.setLinks(linksArray);
    cosmosInstance.render(); // start simulation + rendering

    // (#11) ResizeObserver for container resize
    const resizeObserver = new ResizeObserver(() => {
      // cosmos.gl handles canvas resize internally via its own ResizeObserver,
      // but we trigger a fit-view to re-center after layout changes
      cosmosInstance?.fitView(100);
    });
    resizeObserver.observe(container);

    return () => {
      resizeObserver.disconnect();
      cosmosInstance?.destroy();
      cosmosInstance = null;
      colorBuffer = null;
      sizeBuffer = null;
    };
  });
</script>

<div class="relative">
  {#if initError}
    <div class="w-full rounded-lg bg-surface-1 flex items-center justify-center" style="height: 500px;">
      <div class="text-center text-fg-2">
        <div class="text-sm text-red-400">{initError}</div>
        <div class="text-xs mt-2 text-fg-2/60">Your browser may not support WebGL2</div>
      </div>
    </div>
  {:else}
    <div bind:this={container} class="w-full rounded-lg bg-surface-1" style="height: 500px;"></div>
    {#if zoomLabel}
      <div class="absolute bottom-2 left-2 text-[10px] font-mono px-2 py-0.5 rounded bg-surface-3/80 text-fg-2/70 border border-border/50 pointer-events-none transition-opacity duration-300">
        {zoomLabel}
      </div>
    {/if}
  {/if}
</div>
