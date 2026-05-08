import type { PageData } from './types.js';

/**
 * Compute Internal PageRank scores for crawled pages.
 *
 * Uses the standard iterative PageRank algorithm:
 *   PR(i) = (1-d)/N + d * SUM(PR(j) / L(j)) for all j linking to i
 *
 * where d = damping factor, N = total pages, L(j) = outlink count of page j.
 * Dangling nodes (pages with no outlinks) distribute rank evenly.
 * Scores are normalized to a 0–10 scale.
 */
export function computePageRank(
  pages: PageData[],
  damping = 0.85,
  maxIterations = 100,
  epsilon = 0.0001,
): Map<string, number> {
  const urls = pages.map((p) => p.url);
  const n = urls.length;
  if (n === 0) return new Map();

  const urlIndex = new Map<string, number>();
  const setIfMissing = (url: string | undefined, i: number) => {
    if (url && !urlIndex.has(url)) urlIndex.set(url, i);
  };
  const setVariants = (url: string | undefined, i: number) => {
    if (!url) return;
    setIfMissing(url, i);
    if (url.endsWith('/') && url.length > 1) {
      setIfMissing(url.slice(0, -1), i);
    } else {
      setIfMissing(`${url}/`, i);
    }
  };

  for (let i = 0; i < n; i++) {
    urlIndex.set(pages[i].url, i);
  }
  for (let i = 0; i < n; i++) {
    setIfMissing(pages[i].finalUrl, i);
    setVariants(pages[i].url, i);
    setVariants(pages[i].finalUrl, i);
  }

  // Build adjacency: outlinks[i] = list of indices that page i links to (internal only, deduplicated)
  const outlinks: number[][] = new Array(n);
  for (let i = 0; i < n; i++) {
    const seen = new Set<number>();
    for (const link of pages[i].internalLinks) {
      const j = urlIndex.get(link);
      if (j !== undefined && j !== i) seen.add(j);
    }
    outlinks[i] = Array.from(seen);
  }

  // Iterative PageRank
  let rank = new Float64Array(n).fill(1 / n);
  const base = (1 - damping) / n;

  for (let iter = 0; iter < maxIterations; iter++) {
    const next = new Float64Array(n).fill(base);

    // Dangling node rank (pages with 0 outlinks)
    let danglingSum = 0;
    for (let i = 0; i < n; i++) {
      if (outlinks[i].length === 0) danglingSum += rank[i];
    }
    const danglingContrib = (damping * danglingSum) / n;
    for (let i = 0; i < n; i++) next[i] += danglingContrib;

    // Distribute rank via outlinks
    for (let i = 0; i < n; i++) {
      const outs = outlinks[i];
      if (outs.length === 0) continue;
      const share = (damping * rank[i]) / outs.length;
      for (const j of outs) next[j] += share;
    }

    // Check convergence
    let delta = 0;
    for (let i = 0; i < n; i++) delta += Math.abs(next[i] - rank[i]);
    rank = next;
    if (delta < epsilon) break;
  }

  // Normalize to 0–10 scale
  let maxRank = 0;
  for (let i = 0; i < n; i++) if (rank[i] > maxRank) maxRank = rank[i];

  const result = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    const score = maxRank > 0 ? (rank[i] / maxRank) * 10 : 0;
    result.set(urls[i], Math.round(score * 100) / 100);
  }
  return result;
}
