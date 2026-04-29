# Link Graph Performance Spec: Enterprise-Scale Graph Rendering (1M+ URLs)

## Problem Statement

The current `LinkGraph.svelte` component uses **D3 force-directed simulation with SVG rendering**. For the kelisto.es crawl (1,644 pages), the graph creates **205K SVG DOM nodes** (1,644 circles + 203K lines). This causes:

- **300-600ms UI freeze** per search keystroke or filter toggle
- **100-150ms per hover** (updates all 203K link opacities)
- **Drag is unusable** (restarts simulation with 300+ ticks)
- **Hard cap at 2,000 pages** (`MAX_PAGES`)

**Target: support 1M+ URL enterprise crawls** with smooth interaction at 60fps.

---

## Current Bottleneck Analysis

### 1. SVG DOM Overload (Critical)

| Metric | Current (1.6K pages) | Projected (100K pages) | Target |
|--------|---------------------|------------------------|--------|
| DOM elements | 205K | ~12.5M (impossible) | 0 (GPU buffers) |
| Paint time per tick | 300-500ms | N/A (browser crashes) | <16ms (60fps) |
| Memory (DOM) | 3-4 MB | ~750 MB | ~50 MB GPU |

### 2. D3 Force Simulation — O(N²) (Critical)

| Metric | Current | At 100K nodes | Target |
|--------|---------|---------------|--------|
| Many-body | O(N²) = 1.35M pairs | O(N²) = 5B pairs | GPU-parallel |
| Thread | Main thread | Main thread (frozen) | GPU compute shader |

### 3. Data Transfer — ~39 MB JSON (High)

| Metric | Current (1.6K) | Projected (100K) | Target |
|--------|---------------|------------------|--------|
| Payload | ~39 MB | ~2.4 GB (all fields) | ~5-20 MB (graph-only) |
| Load strategy | All upfront | Impossible | Lazy + streaming |

---

## Technology Evaluation

### Rendering Technologies at Enterprise Scale

| Technology | Max Nodes | Max Edges | Layout | Bundle | Status |
|------------|-----------|-----------|--------|--------|--------|
| **SVG (current)** | ~500 | ~5K | CPU main thread | 0 KB | Unusable at scale |
| **Canvas 2D** | ~10K | ~100K | CPU (worker possible) | 0 KB | Mid-scale only |
| **Sigma.js v2 (WebGL)** | ~50K | ~500K | CPU Web Worker (FA2) | 180 KB | Good to 50K |
| **Cosmograph/cosmos.gl (WebGL)** | **~1M** | **~1M** | **GPU shader** | 250 KB | **Best for 1M+** |
| **WebGPU (GraphWaGu)** | ~200K | ~4M | GPU compute shader | N/A | Academic/experimental |

### Why Cosmograph Is the Right Choice for 1M+ URLs

**Cosmograph (@cosmograph/cosmos)** — now `@cosmos.gl/graph` v2 — is the only production-ready library that handles 1M+ nodes with:

1. **GPU-native everything**: Layout computation AND rendering happen entirely on the GPU via WebGL fragment/vertex shaders. Zero CPU bottleneck for forces.
2. **Barnes-Hut on GPU**: Experimental `useQuadtree` option with configurable `repulsionQuadtreeLevels` (5-12 depth)
3. **Real-time at scale**: "Hundreds of thousands of points and links on modern hardware" at interactive frame rates
4. **OpenJS Foundation**: Joined in 2024, ensuring long-term maintenance
5. **v2 API**: `Float32Array`-based data input (`setPointPositions`, `setLinks`) for zero-copy GPU uploads
6. **Built-in events**: `onClick`, `onNodeMouseOver`, `onNodeMouseOut`, `onZoom` — no manual hit-testing
7. **npm**: `@cosmograph/cosmos` (v1) / `@cosmos.gl/graph` (v2)

**Cosmograph vs Sigma.js for enterprise:**

| Feature | Cosmograph | Sigma.js v2 |
|---------|------------|-------------|
| Max nodes (smooth) | **1M** | 50K |
| Layout engine | **GPU shaders** | CPU Web Worker |
| Force computation | **Parallel GPU** | Sequential CPU (Barnes-Hut) |
| Edge rendering at 500K | **Native** | Degrades |
| Data format | Float32Array (GPU-optimal) | Graphology objects |
| Node sampling | Built-in `getSampledNodePositionsMap` | Manual |
| Zoom-dependent labels | Built-in via sampling | Built-in (labelDensity) |
| Bundle size | ~250 KB | ~260 KB (sigma+graphology+FA2) |

### WASM Evaluation (As Requested)

**Figma's Approach:**
- C++ → WASM via Emscripten, 3x faster load time
- Key lesson: keep hot loops in WASM memory, minimize JS↔WASM boundary crossings
- Recently migrating rendering from WebGL to WebGPU for compute shaders
- Applicable to Micelio: Figma's win was in their _rendering engine_, not just layout — same principle applies here

**@antv/layout-wasm (Rust+WASM):**
- ForceAtlas2: 4.6x speedup at 500 nodes, 2.3x at 2,000 nodes vs Graphology JS
- Multi-threaded via `wasm-bindgen-rayon` + SharedArrayBuffer
- Requires COOP/COEP headers

**Verdict:** WASM gives 2-5x CPU layout speedup. But Cosmograph's GPU approach gives **100x+** by running forces entirely on the GPU. WASM becomes relevant if we need a CPU fallback for GPUs that don't support Cosmograph's quadtree (e.g., some Nvidia cards on Windows with ANGLE).

---

## Multi-Worker Architecture

The user asked about leveraging multiple workers. Here's how workers fit into the architecture:

### Worker Strategy: Parallel Pipeline

```
┌──────────────────────────────────────────────────────────────┐
│                         MAIN THREAD                           │
│                                                                │
│  ┌─────────────┐    ┌──────────────────────────────────┐     │
│  │  Svelte UI   │    │  Cosmograph (WebGL Canvas)        │     │
│  │  Controls    │───▶│  GPU: Layout + Render             │     │
│  │  Detail Panel│    │  - Force simulation (GPU shaders)  │     │
│  │  Search Box  │    │  - Node/edge rendering (GPU)       │     │
│  └─────────────┘    │  - Zoom/pan (GPU)                   │     │
│                      └──────────────────────────────────┘     │
│        ▲  ▲  ▲                                                │
│        │  │  │          SharedArrayBuffer                      │
│        │  │  │         (node positions, colors)                │
└────────┼──┼──┼────────────────────────────────────────────────┘
         │  │  │
    ┌────┘  │  └────┐
    │       │       │
┌───▼──┐ ┌─▼────┐ ┌▼──────┐
│Worker│ │Worker│ │Worker │
│  #1  │ │  #2  │ │  #3   │
│Data  │ │Search│ │Stats  │
│Prep  │ │Index │ │Compute│
└──────┘ └──────┘ └───────┘
```

### Worker #1: Data Preprocessing Worker

**Purpose:** Parse and prepare graph data off the main thread.

```typescript
// graph-data-worker.ts
self.onmessage = ({ data: { pages } }) => {
  // Build node array and edge array from raw page data
  // Extract only graph-relevant fields
  // Deduplicate edges
  // Compute edge sampling if needed
  // Return Float32Array-ready data for Cosmograph

  const nodes = new Float32Array(pages.length * 2); // x, y positions
  const colors = new Float32Array(pages.length * 4); // RGBA per node
  const edges = new Uint32Array(edgeCount * 2); // source, target indices

  postMessage({ nodes, colors, edges }, [nodes.buffer, colors.buffer, edges.buffer]);
};
```

**Why a separate worker:** For 1M pages, JSON parsing + deduplication + Float32Array conversion takes 2-5 seconds. Moving this off the main thread keeps the UI responsive during data load.

### Worker #2: Search Index Worker

**Purpose:** Build and query a full-text index for URL search across 1M+ pages.

```typescript
// search-worker.ts
import Fuse from 'fuse.js'; // or custom trie

let index: Fuse<{id: string; title: string}>;

self.onmessage = ({ data }) => {
  if (data.type === 'build') {
    // Build search index from node data (1M entries)
    index = new Fuse(data.nodes, { keys: ['id', 'title'], threshold: 0.3 });
    postMessage({ type: 'ready' });
  }
  if (data.type === 'search') {
    const results = index.search(data.query).slice(0, 100);
    const matchIds = new Set(results.map(r => r.item.id));
    postMessage({ type: 'results', matchIds: [...matchIds] });
  }
};
```

**Why a separate worker:** Searching 1M URLs with fuzzy matching takes 50-200ms. Running this in a worker prevents search input lag.

### Worker #3: Analytics/Stats Worker

**Purpose:** Compute derived statistics (connected components, cluster detection, centrality updates) without blocking rendering.

```typescript
// stats-worker.ts
self.onmessage = ({ data }) => {
  if (data.type === 'neighbors') {
    // Find all neighbors of a clicked node
    // Compute inlinks/outlinks from edge array
    const inlinks = [];
    const outlinks = [];
    for (let i = 0; i < data.edges.length; i += 2) {
      if (data.edges[i] === data.nodeIndex) outlinks.push(data.edges[i + 1]);
      if (data.edges[i + 1] === data.nodeIndex) inlinks.push(data.edges[i]);
    }
    postMessage({ type: 'neighbors', inlinks, outlinks });
  }
};
```

### SharedArrayBuffer for Zero-Copy Data Sharing

For maximum performance with 1M+ nodes, use SharedArrayBuffer to share node position data between workers without copying:

```typescript
// Main thread
const sab = new SharedArrayBuffer(nodeCount * 8); // 2 floats per node (x, y)
const positions = new Float32Array(sab);

// Pass to Cosmograph (reads positions directly)
// Pass to workers (they can read/write positions without postMessage overhead)

// Worker reads positions without copying
self.onmessage = ({ data: { sharedBuffer } }) => {
  const positions = new Float32Array(sharedBuffer);
  // Access positions[i*2] and positions[i*2+1] for node i
};
```

**Requires COOP/COEP headers:**
```typescript
res.setHeader('Cross-Origin-Opener-Policy', 'same-origin');
res.setHeader('Cross-Origin-Embedder-Policy', 'require-corp');
```

### Service Worker: Graph Data Caching

Use a Service Worker to cache the graph API response, enabling instant revisits:

```typescript
// sw.ts — Graph cache strategy
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);
  if (url.pathname.match(/\/api\/crawl\/[^/]+\/graph$/)) {
    event.respondWith(
      caches.open('graph-data-v1').then(async (cache) => {
        const cached = await cache.match(event.request);
        if (cached) return cached; // Instant on revisit

        const response = await fetch(event.request);
        cache.put(event.request, response.clone());
        return response;
      })
    );
  }
});
```

**Impact:** First visit: 2-5 sec load. Subsequent visits: **<100ms** (from cache).

### Worker Limits and Overhead

| Factor | Guideline |
|--------|-----------|
| Max useful workers | 4-6 (matches typical CPU cores) |
| Worker creation time | 50-100ms per worker |
| postMessage overhead | <100ms for objects up to 100KB |
| Transferable objects | ArrayBuffer transfer is **instant** (ownership transfer, not copy) |
| SharedArrayBuffer | Zero-copy, but requires COOP/COEP headers |

**Practical approach:** 3 workers (data prep, search, analytics) + GPU for layout/rendering. The GPU does the heavy lifting; workers handle data transformation and search.

---

## Recommended Architecture

### Tiered Rendering Strategy

| Crawl Size | Renderer | Layout | Workers |
|------------|----------|--------|---------|
| **< 5K pages** | Sigma.js (WebGL) | ForceAtlas2 Web Worker | 1 (layout) |
| **5K - 50K pages** | Sigma.js (WebGL) | ForceAtlas2 Worker + edge sampling | 2 (layout + search) |
| **50K+ pages** | **Cosmograph (GPU)** | **GPU force shaders** | 3 (data prep + search + stats) |

**Auto-detection in LinkGraph.svelte:**
```typescript
const renderer = pages.length > 50_000 ? 'cosmograph' : 'sigma';
```

Both renderers share the same Svelte control UI (search, color mode, depth filter, detail panel). Only the rendering backend changes.

### Architecture Diagram (Enterprise Mode — Cosmograph)

```
┌─────────────────────────────────────────────────────────────────┐
│                         LinkGraph.svelte                          │
│                                                                   │
│  ┌──────────────┐    ┌────────────────────────────────────┐     │
│  │  Controls     │    │  <canvas> — Cosmograph              │     │
│  │  (Svelte)     │───▶│  GPU: ForceAtlas2 + WebGL Render    │     │
│  │  Search       │    │  - 1M nodes as GL_POINTS            │     │
│  │  Color Mode   │    │  - Edges as GL_LINES                │     │
│  │  Depth Filter │    │  - Barnes-Hut quadtree on GPU       │     │
│  │  Node Detail  │    │  - Zoom/pan/hover built-in          │     │
│  └──────────────┘    └────────────────────────────────────┘     │
│       │  ▲                      ▲                                │
│       │  │                      │ Float32Array                   │
│       │  │               ┌──────┴──────┐                        │
│       │  │               │ Worker #1    │                        │
│       │  │               │ Data Prep    │                        │
│       │  │               │ JSON→F32Array│                        │
│       │  │               └─────────────┘                        │
│       │  │                                                       │
│       │  └────────── ┌──────────────┐                           │
│       │              │ Worker #2     │                           │
│       │              │ Search Index  │                           │
│       │              │ Fuse.js/Trie  │                           │
│       │              └──────────────┘                           │
│       │                                                          │
│       └──────────── ┌──────────────┐                            │
│                     │ Worker #3     │                            │
│                     │ Stats/Neighbors│                           │
│                     │ Click analysis │                           │
│                     └──────────────┘                            │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  Service Worker — Cache /api/crawl/:id/graph responses    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Data Flow

```
1. User clicks "Links" tab
2. Service Worker checks cache → if hit, serve instantly
3. If miss: GET /api/crawl/:id/graph (lazy-load, gzip compressed)
   → Returns { nodes: [{id, depth, authority, hub, ...}], edges: [{source, target}] }
   → Server-side edge sampling for crawls >50K pages
4. Worker #1 (Data Prep): JSON → Float32Array conversion
   → Transfers ArrayBuffers to main thread (zero-copy)
5. Initialize Cosmograph with Float32Array data
   → GPU begins force simulation immediately
   → Rendering starts within 1-2 frames
6. Worker #2 (Search): Builds Fuse.js index in background
7. User interactions handled by Cosmograph natively (GPU-accelerated)
8. Node clicks → Worker #3 computes neighbors from edge array
9. Service Worker caches the response for instant revisits
```

---

## Implementation Plan

### Phase 1: Backend — Graph API Endpoint

**New: `GET /api/crawl/:id/graph`**

```typescript
interface GraphResponse {
  nodes: Array<{
    id: string;           // URL
    depth: number | null;
    authority: number;
    hub: number;
    centrality: number;
    closeness: number;
    inDegree: number;
    outDegree: number;
    title: string;
  }>;
  edges: Array<{
    source: string;
    target: string;
  }>;
  meta: {
    maxDepth: number;
    maxAuthority: number;
    maxHub: number;
    totalEdgesBeforeSampling: number;
    samplingApplied: boolean;
  };
}
```

**Server-side edge sampling (for crawls >50K pages):**
```typescript
function buildGraphResponse(pages: PageData[], maxEdgesPerNode = 15): GraphResponse {
  const nodeMap = new Map<string, GraphNode>();
  const edgeSet = new Set<string>();
  const edges: Edge[] = [];

  // Build nodes
  for (const p of pages) {
    const li = p.linkIntelligence;
    nodeMap.set(p.url, {
      id: p.url,
      depth: li?.clickDepth ?? null,
      authority: li?.authorityScore ?? 0,
      hub: li?.hubScore ?? 0,
      centrality: li?.betweennessCentrality ?? 0,
      closeness: li?.closenessCentrality ?? 0,
      inDegree: li?.inDegree ?? 0,
      outDegree: li?.outDegree ?? 0,
      title: typeof p.title === 'string' ? p.title : p.title?.text || '',
    });
  }

  // Build edges with sampling
  for (const p of pages) {
    const links = (p.internalLinks || [])
      .filter(l => nodeMap.has(l.url))
      .slice(0, maxEdgesPerNode); // Top-K per node

    for (const link of links) {
      const key = `${p.url}\t${link.url}`;
      if (!edgeSet.has(key)) {
        edgeSet.add(key);
        edges.push({ source: p.url, target: link.url });
      }
    }
  }

  return {
    nodes: [...nodeMap.values()],
    edges,
    meta: { maxDepth: ..., maxAuthority: ..., maxHub: ..., totalEdgesBeforeSampling: ..., samplingApplied: ... }
  };
}
```

**Payload sizes:**

| Crawl Size | Nodes Payload | Edges (sampled) | Total (gzip) |
|------------|---------------|-----------------|--------------|
| 1K pages | 200 KB | 1 MB | ~200 KB |
| 10K pages | 2 MB | 10 MB | ~2 MB |
| 100K pages | 20 MB | 100 MB → 30 MB (sampled) | ~5 MB |
| 1M pages | 200 MB | ~150 MB (sampled 15/node) | ~30 MB |

For 1M+ pages, consider streaming JSON (`Transfer-Encoding: chunked`) or binary format (Apache Arrow via Cosmograph's native support).

### Phase 2: Frontend — Dual Renderer

**Install dependencies:**
```bash
# Cosmograph (GPU renderer for 50K+)
npm install @cosmos.gl/graph

# Sigma.js (WebGL renderer for <50K) — optional, for lighter crawls
npm install sigma graphology graphology-layout-forceatlas2
```

**Cosmograph integration (enterprise mode):**

```typescript
import { Graph } from '@cosmos.gl/graph';

// Create canvas element
const canvas = document.createElement('canvas');
container.appendChild(canvas);

// Initialize Cosmograph
const graph = new Graph(canvas, {
  backgroundColor: '#1a1a2e',
  nodeColor: (i: number) => nodeColors[i],
  nodeSize: (i: number) => 3 + nodes[i].authority * 2,
  linkColor: '#66666644',
  linkWidth: 0.5,
  linkArrows: false,
  fitViewOnInit: true,
  // Force simulation config
  simulation: {
    repulsion: 0.1,
    linkSpring: 1.0,
    linkDistance: 2,
    gravity: 0.1,
    friction: 0.85,
    decay: 1000,
  },
  // Events
  events: {
    onClick: (node, index, pos) => { selectedNode = nodes[index]; },
    onNodeMouseOver: (node, index) => { hoveredNode = nodes[index]; },
    onNodeMouseOut: () => { hoveredNode = null; },
  },
});

// Set data (Float32Array for performance)
graph.setPointPositions(new Float32Array(nodeCount * 2)); // random initial
graph.setLinks(edgeIndices); // Uint32Array of [source, target, source, target, ...]
graph.setPointColors(colorArray); // Float32Array RGBA
graph.setPointSizes(sizeArray); // Float32Array
```

**Color mode switching (GPU-optimized):**
```typescript
function applyColorMode(mode: 'depth' | 'authority' | 'hub') {
  const colors = new Float32Array(nodeCount * 4);
  for (let i = 0; i < nodeCount; i++) {
    const [r, g, b] = computeColor(nodes[i], mode);
    colors[i * 4] = r;
    colors[i * 4 + 1] = g;
    colors[i * 4 + 2] = b;
    colors[i * 4 + 3] = 1.0;
  }
  graph.setPointColors(colors);
  // GPU updates instantly — no DOM manipulation
}
```

**Depth filter (hide nodes by depth):**
```typescript
function applyDepthFilter(hiddenDepths: Set<number>) {
  const sizes = new Float32Array(nodeCount);
  for (let i = 0; i < nodeCount; i++) {
    sizes[i] = hiddenDepths.has(nodes[i].depth) ? 0 : 3 + nodes[i].authority * 2;
  }
  graph.setPointSizes(sizes);
  // Setting size to 0 effectively hides the node
}
```

### Phase 3: Workers

**Worker #1 — Data Prep:**
```typescript
// workers/graph-data-worker.ts
self.onmessage = ({ data: { graphResponse } }) => {
  const { nodes, edges, meta } = graphResponse;

  // Build index map: URL → index
  const urlToIndex = new Map<string, number>();
  nodes.forEach((n, i) => urlToIndex.set(n.id, i));

  // Convert edges to index pairs
  const edgeIndices = new Uint32Array(edges.length * 2);
  let validEdges = 0;
  for (const edge of edges) {
    const si = urlToIndex.get(edge.source);
    const ti = urlToIndex.get(edge.target);
    if (si !== undefined && ti !== undefined) {
      edgeIndices[validEdges * 2] = si;
      edgeIndices[validEdges * 2 + 1] = ti;
      validEdges++;
    }
  }

  // Transfer (zero-copy)
  const finalEdges = edgeIndices.slice(0, validEdges * 2);
  postMessage(
    { nodes, edgeIndices: finalEdges, urlToIndex: Object.fromEntries(urlToIndex), meta },
    [finalEdges.buffer]
  );
};
```

**Worker #2 — Search:**
```typescript
// workers/search-worker.ts
let urls: string[] = [];
let titles: string[] = [];

self.onmessage = ({ data }) => {
  if (data.type === 'build') {
    urls = data.nodes.map((n: any) => n.id);
    titles = data.nodes.map((n: any) => n.title || '');
    postMessage({ type: 'ready' });
  }
  if (data.type === 'search') {
    const q = data.query.toLowerCase();
    const matches: number[] = [];
    for (let i = 0; i < urls.length && matches.length < 200; i++) {
      if (urls[i].toLowerCase().includes(q) || titles[i].toLowerCase().includes(q)) {
        matches.push(i);
      }
    }
    postMessage({ type: 'results', matches });
  }
};
```

**Worker #3 — Neighbor Analysis:**
```typescript
// workers/stats-worker.ts
let adjacency: Map<number, { inlinks: number[]; outlinks: number[] }>;

self.onmessage = ({ data }) => {
  if (data.type === 'buildAdjacency') {
    adjacency = new Map();
    const edges = data.edgeIndices;
    for (let i = 0; i < edges.length; i += 2) {
      const s = edges[i], t = edges[i + 1];
      if (!adjacency.has(s)) adjacency.set(s, { inlinks: [], outlinks: [] });
      if (!adjacency.has(t)) adjacency.set(t, { inlinks: [], outlinks: [] });
      adjacency.get(s)!.outlinks.push(t);
      adjacency.get(t)!.inlinks.push(s);
    }
    postMessage({ type: 'ready' });
  }
  if (data.type === 'neighbors') {
    const entry = adjacency?.get(data.nodeIndex);
    postMessage({
      type: 'neighbors',
      inlinks: entry?.inlinks.slice(0, 20) || [],
      outlinks: entry?.outlinks.slice(0, 20) || [],
    });
  }
};
```

### Phase 4: Service Worker Cache

```typescript
// Register in dashboard/src/app.html or main entry
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/graph-sw.js');
}
```

### Phase 5: Optional Enhancements

#### 5a. WASM Layout Fallback
For GPUs that don't support Cosmograph's quadtree (some Nvidia on Windows with ANGLE):
```bash
npm install @antv/layout-wasm
```
Use as CPU fallback with Sigma.js renderer.

#### 5b. Streaming JSON for 1M+ Pages
```typescript
// Server: stream graph data as NDJSON
res.setHeader('Transfer-Encoding', 'chunked');
for (const node of nodes) {
  res.write(JSON.stringify(node) + '\n');
}
```
Client parses incrementally, feeding Cosmograph as data arrives.

#### 5c. Hierarchical Edge Bundling
Group edges by URL path prefix:
- `/blog/*` → `/products/*` = single bundled edge (thickness = count)
- Reduces visual clutter for dense enterprise sites

#### 5d. Semantic Zoom Levels
- **Zoom out**: Show clusters only (group by path prefix), aggregated edges
- **Zoom mid**: Show individual pages, sampled edges
- **Zoom in**: Show all edges for visible nodes, labels

---

## Performance Targets

| Metric | Current (SVG) | Sigma (<50K) | Cosmograph (1M+) |
|--------|--------------|-------------|------------------|
| **Max pages** | 2,000 | 50,000 | **1,000,000+** |
| **Render time** | 300-500ms | <16ms (60fps) | **<16ms (60fps)** |
| **Hover response** | 100-150ms | <16ms | **<16ms** |
| **Search** | 300-600ms | <50ms | **<50ms** (worker) |
| **Initial load** | 39 MB, 10s | 2-5 MB, 1-2s | **5-30 MB, 2-5s** |
| **Layout convergence** | 15-50s (CPU) | 5-10s (worker) | **1-5s (GPU)** |
| **Drag** | Unusable | 60fps | **60fps** |
| **Memory** | 50-80 MB | ~20 MB | **~100-200 MB** |
| **Revisit load** | Same as first | Same | **<100ms** (SW cache) |

---

## Migration Path

```
Phase 1: Backend Graph API ✅ COMPLETED
├── ✅ Add GET /api/crawl/:id/graph endpoint (src/ui-api.ts)
├── ✅ Server-side edge sampling (maxEdgesPerNode = 15, threshold: 5K pages)
├── ✅ Gzip compression (jsonCompressed helper)
├── ✅ COOP/COEP headers (for SharedArrayBuffer)
└── ✅ Lazy-load on Links tab click (LinkGraph fetches from /graph)

Phase 2: Dual Renderer ✅ COMPLETED
├── ✅ Install sigma 3.0.2 + graphology 0.26.0 + graphology-layout-forceatlas2 0.10.1
├── ✅ Implement Sigma.js renderer (SigmaGraph.svelte — WebGL, ForceAtlas2 Web Worker)
├── ✅ Auto-detect based on crawl size (threshold: 50K → Cosmos)
├── ✅ Shared Svelte control UI (search, color, filter, detail) in LinkGraph.svelte
├── ✅ Remove D3 SVG rendering
├── ✅ Remove MAX_PAGES cap
├── ✅ CosmosGraph.svelte — full @cosmos.gl/graph v2 GPU renderer (Phase 2b)
├── ✅ Lazy-loaded via dynamic import (code-split: 347KB main + 369KB Cosmos chunk)
├── ✅ Shared graph types extracted to dashboard/src/lib/types/graph.ts
└── ✅ Removed D3 dependency entirely

Phase 3: Worker Pipeline ✅ COMPLETED
├── ✅ Worker #1: Data preprocessing + edge bundling (graph-data-worker.ts)
├── ✅ Worker #2: Search index — substring match (search-worker.ts)
├── ✅ Worker #3: Neighbor analysis — adjacency map (neighbor-worker.ts)
└── Workers shared between Sigma and Cosmos renderers

Phase 4: Service Worker Cache ✅ COMPLETED
├── ✅ Cache graph API responses (graph-sw.js)
├── ✅ Cache-first strategy with version-based eviction
└── ✅ Instant revisits (<100ms)

Phase 5: Enhancements (partially complete)
├── ◻️ 5a: WASM layout fallback (@antv/layout-wasm) — for GPUs without quadtree
├── ◻️ 5b: Streaming JSON for 1M+ pages
├── ✅ 5c: Hierarchical edge bundling (path-prefix grouping)
├── ✅ 5d: Semantic zoom levels (overview/normal/detail)
└── ◻️ Apache Arrow format support (Cosmograph native)
```

### Implementation Notes

**Phase 1 — Commits:**
- `efd13fc` — Add /api/crawl/:id/graph endpoint for lightweight graph visualization

**Phase 2 — Commits:**
- `6e174d4` — Replace D3 SVG with Sigma.js WebGL for link graph rendering

**Phase 2b — Commits:**
- `1859a04` — Add GPU-accelerated graph renderer with cosmos.gl (Phase 2b)
- Full @cosmos.gl/graph v2.6.4 integration with Float32Array data pipeline
- Lazy-loaded via dynamic import (code-split: 347KB main + 369KB Cosmos)
- Edge deduplication, WebGL2 error handling, ResizeObserver, adaptive DPI
- Shared graph types extracted to dashboard/src/lib/types/graph.ts

**Architecture (current):**
```
LinkGraph.svelte (orchestrator)
├── Fetches GET /api/crawl/:id/graph
├── Manages shared state (colorMode, searchQuery, hiddenDepths, selectedNode)
├── Renders control UI (depth legend, search, color mode buttons, detail panel)
├── Workers: data-prep (#1), search (#2), neighbor (#3) — shared by both renderers
└── Delegates rendering to:
    ├── SigmaGraph.svelte (<50K nodes) — WebGL + ForceAtlas2 Web Worker
    └── CosmosGraph.svelte (≥50K nodes) — @cosmos.gl/graph v2, GPU layout + render
        └── Lazy-loaded via dynamic import() — only fetched when needed
```

---

## Dependencies

| Package | Version | Size | Purpose |
|---------|---------|------|---------|
| `@cosmos.gl/graph` | ^2.6.4 | ~369 KB | GPU graph renderer (enterprise, lazy-loaded) |
| `sigma` | ^2.4 | ~180 KB | WebGL graph renderer (lightweight) |
| `graphology` | ^0.25 | ~50 KB | Graph data structure (for Sigma) |
| `graphology-layout-forceatlas2` | ^0.10 | ~30 KB | CPU layout + Web Worker (for Sigma) |

**Total: ~510 KB** for both renderers. Only one is loaded per view (code-split).

---

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Cosmograph quadtree fails on some GPUs (Nvidia+ANGLE) | Medium | High | Fallback to Sigma.js automatically |
| SharedArrayBuffer requires COOP/COEP headers | Low | Medium | Already needed for WASM; add to server |
| 1M node JSON payload too large (>100 MB) | Medium | High | Streaming JSON + Apache Arrow format |
| Cosmograph API changes (v1→v2 migration ongoing) | Medium | Medium | Pin version, minimal wrapper |
| Service Worker stale cache | Low | Low | Stale-while-revalidate + version key |
| GPU memory exhaustion for very large graphs | Low | High | Progressive loading, show warning |

---

## References

- [Cosmograph/cosmos.gl](https://cosmograph.app/) — GPU-accelerated, 1M+ nodes (OpenJS Foundation)
- [cosmos.gl v2 API](https://cosmosgl.github.io/graph) — v2 documentation
- [@cosmos.gl/graph npm](https://www.npmjs.com/package/@cosmos.gl/graph) — npm package
- [Sigma.js v2](https://www.sigmajs.org/) — WebGL graph renderer, 50K+ nodes
- [Graphology](https://graphology.github.io/) — Graph data structure with O(1) neighbor lookup
- [graphology-layout-forceatlas2](https://graphology.github.io/standard-library/layout-forceatlas2.html) — ForceAtlas2 with Web Worker
- [@antv/layout-wasm](https://github.com/antvis/layout) — Rust→WASM layout, 2-5x speedup
- [Figma's WASM blog](https://www.figma.com/blog/webassembly-cut-figmas-load-time-by-3x/) — 3x load time improvement
- [Figma's WebGPU migration](https://www.figma.com/blog/figma-rendering-powered-by-webgpu/) — WebGPU rendering
- [GraphWaGu](https://github.com/harp-lab/GraphWaGu) — WebGPU compute shaders, 200K nodes
- [BatchLayout](https://github.com/khaled-rahman/BatchLayout) — Batch-parallel force layout (shared memory)
- [ParaGraphL](https://nblintao.github.io/ParaGraphL/) — WebGL GPGPU for force layout, 75x speedup
- [Linkurious WASM evaluation](https://dev.to/linkuriousdev/to-wasm-or-not-to-wasm-3803) — WASM not worth it for graph viz
- [Is postMessage slow?](https://surma.dev/things/is-postmessage-slow/) — Worker message passing benchmarks
- [How to Visualize a Graph with a Million Nodes](https://nightingaledvs.com/how-to-visualize-a-graph-with-a-million-nodes/) — Cosmograph case study
