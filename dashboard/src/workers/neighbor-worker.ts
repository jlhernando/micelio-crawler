/**
 * Worker #3: Neighbor Analysis
 *
 * Builds adjacency map from edges, returns inlinks/outlinks for clicked nodes.
 * Keeps main thread free during node click analysis on large graphs.
 */

interface Edge {
  source: string;
  target: string;
}

export type NeighborRequest =
  | { type: 'buildAdjacency'; edges: Edge[] }
  | { type: 'neighbors'; nodeId: string; limit?: number }
  | { type: 'clear' };

export type NeighborResponse =
  | { type: 'ready'; nodeCount: number }
  | { type: 'neighbors'; nodeId: string; inlinks: string[]; outlinks: string[] };

const adjacencyIn = new Map<string, Set<string>>();
const adjacencyOut = new Map<string, Set<string>>();

self.onmessage = ({ data }: MessageEvent<NeighborRequest>) => {
  if (data.type === 'clear') {
    adjacencyIn.clear();
    adjacencyOut.clear();
    return;
  }

  if (data.type === 'buildAdjacency') {
    adjacencyIn.clear();
    adjacencyOut.clear();

    for (const edge of data.edges) {
      // Outlinks
      if (!adjacencyOut.has(edge.source)) adjacencyOut.set(edge.source, new Set());
      adjacencyOut.get(edge.source)!.add(edge.target);

      // Inlinks
      if (!adjacencyIn.has(edge.target)) adjacencyIn.set(edge.target, new Set());
      adjacencyIn.get(edge.target)!.add(edge.source);
    }

    const nodeCount = new Set([...adjacencyIn.keys(), ...adjacencyOut.keys()]).size;
    const response: NeighborResponse = { type: 'ready', nodeCount };
    self.postMessage(response);
  }

  if (data.type === 'neighbors') {
    const limit = data.limit ?? 20;
    const inSet = adjacencyIn.get(data.nodeId);
    const outSet = adjacencyOut.get(data.nodeId);

    const response: NeighborResponse = {
      type: 'neighbors',
      nodeId: data.nodeId,
      inlinks: inSet ? [...inSet].slice(0, limit) : [],
      outlinks: outSet ? [...outSet].slice(0, limit) : [],
    };
    self.postMessage(response);
  }
};
