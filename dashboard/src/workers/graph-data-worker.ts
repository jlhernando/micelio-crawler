/**
 * Worker #1: Graph Data Preprocessing
 *
 * Builds edge maps (inlinks/outlinks) and URL→index mapping off the main thread.
 * For large crawls (50K+), this prevents main thread jank during data loading.
 */

interface GraphNode {
  id: string;
  depth: number | null;
  authority: number;
  hub: number;
  centrality: number;
  closeness: number;
  inDegree: number;
  outDegree: number;
  title: string;
}

interface GraphEdge {
  source: string;
  target: string;
}

export interface BundledEdge {
  sourcePrefix: string;
  targetPrefix: string;
  count: number;
}

export type GraphDataRequest =
  | { type: 'process'; nodes: GraphNode[]; edges: GraphEdge[] }
  | { type: 'bundle'; edges: GraphEdge[]; minCount: number }
  | { type: 'clear' };

export type GraphDataResponse =
  | { type: 'processed'; inlinks: Record<string, string[]>; outlinks: Record<string, string[]>; urlToIndex: Record<string, number>; edgeCount: number }
  | { type: 'bundled'; bundles: BundledEdge[] };

function getPathPrefix(url: string): string {
  try {
    const segments = new URL(url).pathname.split('/').filter(Boolean);
    return '/' + (segments[0] || '');
  } catch {
    return '/';
  }
}

self.onmessage = ({ data }: MessageEvent<GraphDataRequest>) => {
  if (data.type === 'clear') {
    return;
  }

  if (data.type === 'process') {
    const { nodes, edges } = data;

    const urlToIndex: Record<string, number> = {};
    for (let i = 0; i < nodes.length; i++) {
      urlToIndex[nodes[i].id] = i;
    }

    const inlinks: Record<string, string[]> = {};
    const outlinks: Record<string, string[]> = {};

    for (const edge of edges) {
      if (!outlinks[edge.source]) outlinks[edge.source] = [];
      outlinks[edge.source].push(edge.target);
      if (!inlinks[edge.target]) inlinks[edge.target] = [];
      inlinks[edge.target].push(edge.source);
    }

    self.postMessage({
      type: 'processed',
      inlinks,
      outlinks,
      urlToIndex,
      edgeCount: edges.length,
    } satisfies GraphDataResponse);
  }

  if (data.type === 'bundle') {
    const { edges, minCount } = data;
    const bundleMap = new Map<string, number>();

    for (const edge of edges) {
      const sp = getPathPrefix(edge.source);
      const tp = getPathPrefix(edge.target);
      if (sp === tp) continue; // skip intra-prefix edges
      const key = `${sp}\0${tp}`;
      bundleMap.set(key, (bundleMap.get(key) || 0) + 1);
    }

    const bundles: BundledEdge[] = [];
    for (const [key, count] of bundleMap) {
      if (count < minCount) continue;
      const [sourcePrefix, targetPrefix] = key.split('\0');
      bundles.push({ sourcePrefix, targetPrefix, count });
    }

    bundles.sort((a, b) => b.count - a.count);

    self.postMessage({
      type: 'bundled',
      bundles,
    } satisfies GraphDataResponse);
  }
};
