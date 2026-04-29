export interface GraphNode {
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

export interface GraphEdge {
  source: string;
  target: string;
}

export interface GraphMeta {
  maxDepth: number;
  maxAuthority: number;
  maxHub: number;
  maxInDegree: number;
  totalEdgesBeforeSampling: number;
  samplingApplied: boolean;
  nodeCapped?: boolean;
  totalNodes?: number;
}

export interface BundledEdge {
  sourcePrefix: string;
  targetPrefix: string;
  count: number;
}
