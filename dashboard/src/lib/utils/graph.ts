import type { GraphNode } from '../types/graph.js';

/** Build percentile-rank maps (0–1) for authority and hub scores. */
export function buildRankMaps(nodeList: GraphNode[]): { authorityRank: Map<string, number>; hubRank: Map<string, number> } {
  const authorityRank = new Map<string, number>();
  const hubRank = new Map<string, number>();
  const byAuth = [...nodeList].sort((a, b) => a.authority - b.authority);
  const byHub = [...nodeList].sort((a, b) => a.hub - b.hub);
  const n = Math.max(1, nodeList.length - 1);
  for (let i = 0; i < byAuth.length; i++) authorityRank.set(byAuth[i].id, i / n);
  for (let i = 0; i < byHub.length; i++) hubRank.set(byHub[i].id, i / n);
  return { authorityRank, hubRank };
}
