/**
 * Worker #2: Search Index
 *
 * Handles URL/title substring matching off the main thread.
 * Prevents search input lag on large graphs (50K+ nodes).
 */

interface SearchNode {
  id: string;
  title: string;
}

export type SearchRequest =
  | { type: 'build'; nodes: SearchNode[] }
  | { type: 'search'; query: string }
  | { type: 'clear' };

export type SearchResponse =
  | { type: 'ready'; nodeCount: number }
  | { type: 'results'; matchIds: string[] };

let urls: string[] = [];
let titles: string[] = [];
let ids: string[] = [];

self.onmessage = ({ data }: MessageEvent<SearchRequest>) => {
  if (data.type === 'clear') {
    urls = [];
    titles = [];
    ids = [];
    return;
  }

  if (data.type === 'build') {
    ids = data.nodes.map(n => n.id);
    urls = ids.map(id => {
      try { return new URL(id).pathname.toLowerCase(); } catch { return id.toLowerCase(); }
    });
    titles = data.nodes.map(n => (n.title || '').toLowerCase());

    const response: SearchResponse = { type: 'ready', nodeCount: ids.length };
    self.postMessage(response);
  }

  if (data.type === 'search') {
    const q = data.query.toLowerCase();
    if (!q) {
      const response: SearchResponse = { type: 'results', matchIds: [] };
      self.postMessage(response);
      return;
    }

    const matchIds: string[] = [];
    for (let i = 0; i < ids.length && matchIds.length < 200; i++) {
      if (urls[i].includes(q) || ids[i].toLowerCase().includes(q) || titles[i].includes(q)) {
        matchIds.push(ids[i]);
      }
    }

    const response: SearchResponse = { type: 'results', matchIds };
    self.postMessage(response);
  }
};
