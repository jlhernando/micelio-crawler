/**
 * Internal Link Intelligence engine.
 *
 * Computes advanced internal linking metrics:
 * - Click Depth from Homepage (BFS)
 * - Near-Orphan Detection (low In-Degree + deep source)
 * - Link Equity Dilution scoring
 * - HITS Hub/Authority Scores (Kleinberg's algorithm)
 * - Internal Linking Suggestions (multi-signal scoring)
 * - Betweenness Centrality (Brandes' algorithm)
 * - Closeness Centrality (BFS-based)
 * - Semantic Link Distance (embedding-based)
 * - Per-page link intelligence data aggregation
 */

import type { PageData, LinkIntelligenceData, LinkPosition, LinkSuggestion, SemanticLinkAnalysis, CrawlStats } from './types.js';

// ── Shared Graph Helpers ─────────────────────────────────

/** Result of building an adjacency list from pages' internal links. */
interface AdjacencyGraph {
  urlIndex: Map<string, number>;
  adj: number[][];
  n: number;
}

/**
 * Build a deduplicated adjacency list (outlinks) from pages' internalLinks.
 * Self-links are excluded. Shared by all graph algorithms.
 *
 * Handles trailing-slash redirects: pages are indexed by both `url` and
 * `finalUrl`, and links are looked up with trailing-slash normalization.
 * This prevents pages from appearing unreachable when the site uses
 * trailing-slash redirect policies (e.g., /about/ -> /about).
 */
function buildAdjacencyList(pages: PageData[]): AdjacencyGraph {
  const n = pages.length;
  const urlIndex = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    urlIndex.set(pages[i].url, i);
    // Also index by finalUrl for redirect resolution
    if (pages[i].finalUrl && pages[i].finalUrl !== pages[i].url) {
      // Only set if not already claimed by another page
      if (!urlIndex.has(pages[i].finalUrl)) {
        urlIndex.set(pages[i].finalUrl, i);
      }
    }
  }

  // Resolve a link URL to a page index, trying trailing-slash variants
  function resolveLink(link: string): number | undefined {
    let idx = urlIndex.get(link);
    if (idx !== undefined) return idx;
    // Try with/without trailing slash
    if (link.endsWith('/') && link.length > 1) {
      idx = urlIndex.get(link.slice(0, -1));
    } else {
      idx = urlIndex.get(link + '/');
    }
    return idx;
  }

  const adj: number[][] = new Array(n);
  for (let i = 0; i < n; i++) {
    const seen = new Set<number>();
    adj[i] = [];
    for (const link of pages[i].internalLinks) {
      const j = resolveLink(link);
      if (j !== undefined && j !== i && !seen.has(j)) {
        seen.add(j);
        adj[i].push(j);
      }
    }
  }

  return { urlIndex, adj, n };
}

// ── Click Depth ──────────────────────────────────────────

/**
 * Compute minimum click depth from the homepage to every reachable page via BFS.
 * Returns a map of URL -> click depth. Pages not reachable return undefined.
 */
export function computeClickDepth(
  pages: PageData[],
  homepageUrl: string,
  graph?: AdjacencyGraph,
): Map<string, number> {
  const depth = new Map<string, number>();
  if (pages.length === 0) return depth;

  const { urlIndex, adj, n } = graph || buildAdjacencyList(pages);

  // Find homepage index (try exact match, then normalized variants)
  let homeIdx = urlIndex.get(homepageUrl);
  if (homeIdx === undefined) {
    // Try with/without trailing slash
    const alt = homepageUrl.endsWith('/')
      ? homepageUrl.slice(0, -1)
      : homepageUrl + '/';
    homeIdx = urlIndex.get(alt);
  }
  if (homeIdx === undefined) {
    // Try with/without www
    try {
      const parsed = new URL(homepageUrl);
      if (parsed.hostname.startsWith('www.')) {
        parsed.hostname = parsed.hostname.replace(/^www\./, '');
      } else {
        parsed.hostname = 'www.' + parsed.hostname;
      }
      homeIdx = urlIndex.get(parsed.toString());
      if (homeIdx === undefined) {
        const alt2 = parsed.toString().endsWith('/')
          ? parsed.toString().slice(0, -1)
          : parsed.toString() + '/';
        homeIdx = urlIndex.get(alt2);
      }
    } catch {
      // ignore
    }
  }
  if (homeIdx === undefined) {
    // Fallback: use the first page at depth 0
    for (let i = 0; i < n; i++) {
      if (pages[i].depth === 0) {
        homeIdx = i;
        break;
      }
    }
  }
  if (homeIdx === undefined) return depth;

  // BFS from homepage
  const visited = new Uint8Array(n);
  visited[homeIdx] = 1;
  depth.set(pages[homeIdx].url, 0);

  let queue = [homeIdx];
  let d = 0;

  while (queue.length > 0) {
    d++;
    const nextQueue: number[] = [];
    for (const idx of queue) {
      for (const neighbor of adj[idx]) {
        if (!visited[neighbor]) {
          visited[neighbor] = 1;
          depth.set(pages[neighbor].url, d);
          nextQueue.push(neighbor);
        }
      }
    }
    queue = nextQueue;
  }

  return depth;
}

// ── Near-Orphan Detection ────────────────────────────────

export interface NearOrphanInfo {
  url: string;
  inDegree: number;
  worstSourceDepth: number | null;
}

/**
 * Detect near-orphan pages: pages with low In-Degree where linking sources
 * are themselves deep (high click depth). These are technically linked but
 * practically invisible.
 *
 * Uses the pre-built deduplicated inlinks map (unique source pages per target).
 */
export function detectNearOrphans(
  pages: PageData[],
  clickDepths: Map<string, number>,
  deduplicatedInlinks: Map<string, Set<string>>,
  maxInDegree = 2,
  minSourceDepth = 4,
): NearOrphanInfo[] {
  const nearOrphans: NearOrphanInfo[] = [];
  const pageSet = new Set(pages.map(p => p.url));

  for (const page of pages) {
    if (page.statusCode !== 200) continue;
    const sourceSet = deduplicatedInlinks.get(page.url);
    if (!sourceSet) continue;
    const sources = [...sourceSet].filter(s => pageSet.has(s));
    if (sources.length === 0 || sources.length > maxInDegree) continue;

    let worstDepth = 0;
    for (const s of sources) {
      const d = clickDepths.get(s) ?? 999;
      if (d > worstDepth) worstDepth = d;
    }

    if (worstDepth >= minSourceDepth) {
      nearOrphans.push({
        url: page.url,
        inDegree: sources.length,
        worstSourceDepth: worstDepth === 999 ? null : worstDepth,
      });
    }
  }

  return nearOrphans.sort((a, b) =>
    (b.worstSourceDepth ?? 999) - (a.worstSourceDepth ?? 999)
  );
}

// ── Link Equity Dilution ─────────────────────────────────

export interface LinkDilutionInfo {
  url: string;
  outDegree: number;
  warning: 'excessive' | 'high';
}

/**
 * Find pages with excessive outgoing internal links that dilute link equity.
 */
export function computeLinkDilution(
  pages: PageData[],
): LinkDilutionInfo[] {
  const warnings: LinkDilutionInfo[] = [];

  for (const p of pages) {
    if (p.statusCode !== 200) continue;
    const outDegree = p.internalLinks.length;
    if (outDegree > 200) {
      warnings.push({ url: p.url, outDegree, warning: 'excessive' });
    } else if (outDegree > 100) {
      warnings.push({ url: p.url, outDegree, warning: 'high' });
    }
  }

  return warnings.sort((a, b) => b.outDegree - a.outDegree);
}

// ── HITS Hub/Authority Scores ─────────────────────────────

export interface HitsScores {
  hubScore: number;      // 0-10 normalized
  authorityScore: number; // 0-10 normalized
}

/**
 * Compute HITS (Hyperlink-Induced Topic Search) scores.
 * Authority = pages that many good hubs link to (pillar content).
 * Hub = pages that link to many good authorities (navigation/resource pages).
 * Uses Kleinberg's iterative algorithm with Float64Array for memory efficiency.
 */
export function computeHits(
  pages: PageData[],
  maxIterations = 50,
  epsilon = 0.0001,
  graph?: AdjacencyGraph,
): Map<string, HitsScores> {
  const { adj: outlinks, n } = graph || buildAdjacencyList(pages);
  if (n === 0) return new Map();

  // Derive inlinks from outlinks
  const inlinks: number[][] = new Array(n);
  for (let i = 0; i < n; i++) inlinks[i] = [];
  for (let i = 0; i < n; i++) {
    for (const j of outlinks[i]) {
      inlinks[j].push(i);
    }
  }

  // Initialize scores
  let auth = new Float64Array(n).fill(1);
  let hub = new Float64Array(n).fill(1);

  for (let iter = 0; iter < maxIterations; iter++) {
    // Update authority scores: auth(i) = sum of hub(j) for all j linking to i
    const newAuth = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      for (const j of inlinks[i]) {
        newAuth[i] += hub[j];
      }
    }

    // Update hub scores: hub(i) = sum of auth(j) for all j that i links to
    const newHub = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      for (const j of outlinks[i]) {
        newHub[i] += auth[j];
      }
    }

    // Normalize (L2 norm)
    let authNorm = 0, hubNorm = 0;
    for (let i = 0; i < n; i++) {
      authNorm += newAuth[i] * newAuth[i];
      hubNorm += newHub[i] * newHub[i];
    }
    authNorm = Math.sqrt(authNorm);
    hubNorm = Math.sqrt(hubNorm);

    if (authNorm > 0) for (let i = 0; i < n; i++) newAuth[i] /= authNorm;
    if (hubNorm > 0) for (let i = 0; i < n; i++) newHub[i] /= hubNorm;

    // Check convergence
    let delta = 0;
    for (let i = 0; i < n; i++) {
      delta += Math.abs(newAuth[i] - auth[i]) + Math.abs(newHub[i] - hub[i]);
    }

    auth = newAuth;
    hub = newHub;
    if (delta < epsilon) break;
  }

  // Normalize to 0-10 scale
  let maxAuth = 0, maxHub = 0;
  for (let i = 0; i < n; i++) {
    if (auth[i] > maxAuth) maxAuth = auth[i];
    if (hub[i] > maxHub) maxHub = hub[i];
  }

  const result = new Map<string, HitsScores>();
  for (let i = 0; i < n; i++) {
    result.set(pages[i].url, {
      authorityScore: maxAuth > 0 ? Math.round((auth[i] / maxAuth) * 1000) / 100 : 0,
      hubScore: maxHub > 0 ? Math.round((hub[i] / maxHub) * 1000) / 100 : 0,
    });
  }

  return result;
}

// ── Internal Linking Suggestions ─────────────────────────

/**
 * Cosine similarity between two vectors.
 */
function cosineSimilarity(a: number[], b: number[]): number {
  if (a.length !== b.length || a.length === 0) return 0;
  let dotProduct = 0, normA = 0, normB = 0;
  for (let i = 0; i < a.length; i++) {
    dotProduct += a[i] * b[i];
    normA += a[i] * a[i];
    normB += b[i] * b[i];
  }
  const denominator = Math.sqrt(normA) * Math.sqrt(normB);
  return denominator > 0 ? dotProduct / denominator : 0;
}

export interface LinkSuggestionsOptions {
  maxSuggestions: number;          // default: 50
  minSemanticSimilarity: number;   // default: 0.3
  maxSemanticSimilarity: number;   // default: 0.85
  targetMaxInDegree: number;       // default: 3 (only suggest for pages with at most this many inlinks)
  sourceMinWordCount: number;      // default: 200 (only suggest adding links to content pages)
}

const DEFAULT_SUGGESTION_OPTIONS: LinkSuggestionsOptions = {
  maxSuggestions: 50,
  minSemanticSimilarity: 0.3,
  maxSemanticSimilarity: 0.85,
  targetMaxInDegree: 3,
  sourceMinWordCount: 200,
};

/**
 * Generate internal linking suggestions based on multiple signals:
 * 1. Semantic similarity (from embeddings)
 * 2. Target in-degree (lower = more deserving of links)
 * 3. Target PageRank (higher = more worth linking to)
 * 4. Click depth reduction potential
 *
 * Without embeddings, only graph-based signals are used.
 */
export function generateLinkSuggestions(
  pages: PageData[],
  embeddings: Map<string, number[]> | null,
  clickDepths: Map<string, number>,
  pageRanks: Map<string, number>,
  deduplicatedInlinks: Map<string, Set<string>>,
  options: Partial<LinkSuggestionsOptions> = {},
): LinkSuggestion[] {
  const opts = { ...DEFAULT_SUGGESTION_OPTIONS, ...options };
  const n = pages.length;
  if (n === 0) return [];

  // Build existing link set for fast lookup
  const existingLinks = new Set<string>();
  for (const page of pages) {
    for (const link of page.internalLinks) {
      existingLinks.add(`${page.url}|${link}`);
    }
  }

  // Find candidate target pages (low In-Degree, indexable, 200 OK)
  const targetCandidates = pages.filter(p =>
    p.statusCode === 200 &&
    p.indexability.indexable &&
    (deduplicatedInlinks.get(p.url)?.size || 0) <= opts.targetMaxInDegree
  );

  // Find candidate source pages (content pages with enough text)
  const sourceCandidates = pages.filter(p =>
    p.statusCode === 200 &&
    p.wordCount >= opts.sourceMinWordCount
  );

  // Performance cap for large sites: limit candidates
  const maxSourceCandidates = 500;
  const maxTargetCandidates = 200;
  let sources = sourceCandidates;
  let targets = targetCandidates;

  if (sources.length > maxSourceCandidates) {
    // Sort by PageRank descending, take top N
    sources = sources
      .map(p => ({ page: p, pr: pageRanks.get(p.url) || 0 }))
      .sort((a, b) => b.pr - a.pr)
      .slice(0, maxSourceCandidates)
      .map(x => x.page);
  }
  if (targets.length > maxTargetCandidates) {
    // Sort by in-degree ascending, take bottom N (most needing links)
    targets = targets
      .map(p => ({ page: p, inDeg: deduplicatedInlinks.get(p.url)?.size || 0 }))
      .sort((a, b) => a.inDeg - b.inDeg)
      .slice(0, maxTargetCandidates)
      .map(x => x.page);
  }

  const suggestions: LinkSuggestion[] = [];

  for (const target of targets) {
    for (const source of sources) {
      if (source.url === target.url) continue;
      if (existingLinks.has(`${source.url}|${target.url}`)) continue;

      // Compute semantic similarity if embeddings available
      let semanticSim: number | null = null;
      if (embeddings) {
        const srcVec = embeddings.get(source.url);
        const tgtVec = embeddings.get(target.url);
        if (srcVec && tgtVec) {
          semanticSim = cosineSimilarity(srcVec, tgtVec);
          // Skip if below minimum or above maximum (cannibalization range)
          if (semanticSim < opts.minSemanticSimilarity) continue;
          if (semanticSim > opts.maxSemanticSimilarity) continue;
        }
      }

      // Compute composite score
      const targetInDeg = deduplicatedInlinks.get(target.url)?.size || 0;
      const targetPR = pageRanks.get(target.url) || 0;
      const targetCD = clickDepths.get(target.url) ?? null;
      const sourceCD = clickDepths.get(source.url) ?? null;

      // Depth reduction: if source is shallower, linking would reduce target's click depth
      const depthReduction = (targetCD !== null && sourceCD !== null)
        ? Math.max(0, targetCD - sourceCD - 1)
        : null;

      // Score components (each normalized to 0-25 range, total max 100)
      let score = 0;

      // 1. Semantic relevance (0-25 points) -- most important
      if (semanticSim !== null) {
        // Sweet spot: 0.4-0.75 is ideal (related but not duplicate)
        const idealRange = semanticSim >= 0.4 && semanticSim <= 0.75;
        score += idealRange ? semanticSim * 33 : semanticSim * 20;
      }

      // 2. Target needs links (0-25 points) -- lower inDegree = higher score
      const inDegScore = Math.max(0, 25 - (targetInDeg * 5));
      score += inDegScore;

      // 3. Target is valuable (0-25 points) -- higher PageRank = more worth linking
      score += Math.min(25, targetPR * 2.5);

      // 4. Depth improvement potential (0-25 points)
      if (depthReduction !== null && depthReduction > 0) {
        score += Math.min(25, depthReduction * 8);
      }

      if (score >= 20) {  // Minimum threshold
        suggestions.push({
          sourceUrl: source.url,
          targetUrl: target.url,
          score: Math.round(score * 10) / 10,
          reason: buildSuggestionReason(semanticSim, targetInDeg, targetCD, depthReduction, targetPR),
          signals: {
            semanticSimilarity: semanticSim,
            targetInDegree: targetInDeg,
            targetClickDepth: targetCD,
            depthReduction,
            targetPageRank: targetPR,
          },
        });
      }
    }
  }

  // Sort by score descending and cap
  suggestions.sort((a, b) => b.score - a.score);
  return suggestions.slice(0, opts.maxSuggestions);
}

function buildSuggestionReason(
  similarity: number | null,
  inDegree: number,
  clickDepth: number | null,
  depthReduction: number | null,
  pageRank: number,
): string {
  const parts: string[] = [];
  if (similarity !== null && similarity >= 0.4) {
    parts.push(`topically related (${Math.round(similarity * 100)}% similarity)`);
  }
  if (inDegree <= 1) {
    parts.push(`target has only ${inDegree} inlink${inDegree === 1 ? '' : 's'}`);
  } else if (inDegree <= 3) {
    parts.push(`target has only ${inDegree} inlinks`);
  }
  if (depthReduction !== null && depthReduction >= 2) {
    parts.push(`would reduce click depth by ${depthReduction}`);
  }
  if (clickDepth !== null && clickDepth > 3) {
    parts.push(`target is ${clickDepth} clicks deep`);
  }
  if (pageRank >= 5) {
    parts.push(`high-value target (PR ${pageRank.toFixed(1)})`);
  }
  return parts.join('; ') || 'potential linking opportunity';
}

// ── Betweenness Centrality ────────────────────────────────

/**
 * Compute betweenness centrality using Brandes' algorithm.
 * Measures how often a page lies on the shortest path between any two other pages.
 * High betweenness = critical "bridge" page.
 *
 * Performance: O(N*M) full, O(sampleSize*M) sampled.
 * For sites >5K pages, uses random sampling (500 nodes).
 * For sites >20K pages, should be skipped (caller decides).
 */
export function computeBetweennessCentrality(
  pages: PageData[],
  maxNodes = 5000,
  graph?: AdjacencyGraph,
): Map<string, number> {
  const { adj, n } = graph || buildAdjacencyList(pages);
  if (n === 0) return new Map();

  const useSampling = n > maxNodes;
  const sampleSize = useSampling ? Math.min(500, n) : n;

  const bc = new Float64Array(n);

  // Source nodes: all (full) or unique random sample via partial Fisher-Yates shuffle
  let sourceNodes: number[];
  if (useSampling) {
    const indices = Array.from({ length: n }, (_, i) => i);
    for (let i = n - 1; i > 0 && i >= n - sampleSize; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [indices[i], indices[j]] = [indices[j], indices[i]];
    }
    sourceNodes = indices.slice(n - sampleSize);
  } else {
    sourceNodes = Array.from({ length: n }, (_, i) => i);
  }

  for (const s of sourceNodes) {
    const stack: number[] = [];
    const pred: number[][] = new Array(n);
    for (let i = 0; i < n; i++) pred[i] = [];

    const sigma = new Float64Array(n);
    sigma[s] = 1;

    const dist = new Int32Array(n).fill(-1);
    dist[s] = 0;

    const queue: number[] = [s];
    let qi = 0;

    // BFS
    while (qi < queue.length) {
      const v = queue[qi++];
      stack.push(v);
      for (const w of adj[v]) {
        if (dist[w] < 0) {
          dist[w] = dist[v] + 1;
          queue.push(w);
        }
        if (dist[w] === dist[v] + 1) {
          sigma[w] += sigma[v];
          pred[w].push(v);
        }
      }
    }

    // Back-propagation of dependencies
    const delta = new Float64Array(n);
    while (stack.length > 0) {
      const w = stack.pop()!;
      for (const v of pred[w]) {
        delta[v] += (sigma[v] / sigma[w]) * (1 + delta[w]);
      }
      if (w !== s) {
        bc[w] += delta[w];
      }
    }
  }

  // If sampled, scale up proportionally
  if (useSampling) {
    const scale = n / sampleSize;
    for (let i = 0; i < n; i++) bc[i] *= scale;
  }

  // Normalize to 0-10 scale
  let maxBC = 0;
  for (let i = 0; i < n; i++) if (bc[i] > maxBC) maxBC = bc[i];

  const result = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    result.set(pages[i].url, maxBC > 0 ? Math.round((bc[i] / maxBC) * 1000) / 100 : 0);
  }

  return result;
}

// ── Closeness Centrality ─────────────────────────────────

/**
 * Compute closeness centrality: how quickly a page can reach all other pages.
 * Uses simplified closeness: reachable / totalDist, then normalizes to 0-10.
 *
 * For sites >maxNodes pages, computes closeness only from a sample of source
 * nodes, then uses average incoming closeness as an approximation.
 *
 * Performance: O(N * (N + M)) full, O(sampleSize * (N + M)) sampled.
 */
export function computeClosenessCentrality(
  pages: PageData[],
  maxNodes = 5000,
  graph?: AdjacencyGraph,
): Map<string, number> {
  const { adj, n } = graph || buildAdjacencyList(pages);
  if (n === 0) return new Map();

  const useSampling = n > maxNodes;
  const sampleSize = useSampling ? Math.min(500, n) : n;

  // Select source nodes: all (full) or unique random sample (Fisher-Yates)
  let sourceNodes: number[];
  if (useSampling) {
    const indices = Array.from({ length: n }, (_, i) => i);
    for (let i = n - 1; i > 0 && i >= n - sampleSize; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [indices[i], indices[j]] = [indices[j], indices[i]];
    }
    sourceNodes = indices.slice(n - sampleSize);
  } else {
    sourceNodes = Array.from({ length: n }, (_, i) => i);
  }

  const rawCloseness = new Float64Array(n);

  for (const s of sourceNodes) {
    // BFS from node s
    const dist = new Int32Array(n).fill(-1);
    dist[s] = 0;
    const queue: number[] = [s];
    let qi = 0;
    let totalDist = 0;
    let reachable = 0;

    while (qi < queue.length) {
      const v = queue[qi++];
      for (const w of adj[v]) {
        if (dist[w] < 0) {
          dist[w] = dist[v] + 1;
          totalDist += dist[w];
          reachable++;
          queue.push(w);
        }
      }
    }

    // Simplified closeness: reachable / totalDist
    rawCloseness[s] = reachable > 0 && totalDist > 0 ? reachable / totalDist : 0;
  }

  // Normalize to 0-10 scale
  let maxCC = 0;
  for (let i = 0; i < n; i++) if (rawCloseness[i] > maxCC) maxCC = rawCloseness[i];

  const result = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    result.set(pages[i].url, maxCC > 0 ? Math.round((rawCloseness[i] / maxCC) * 1000) / 100 : 0);
  }

  return result;
}

// ── Semantic Link Distance ───────────────────────────────

/**
 * Analyze the semantic relevance of internal links using embedding vectors.
 * Flags weak links (topically unrelated) and strong links (high topical match).
 * Requires embedding vectors to be available.
 */
export function analyzeSemanticLinkDistance(
  pages: PageData[],
  embeddings: Map<string, number[]>,
  weakThreshold = 0.15,
  strongThreshold = 0.6,
): SemanticLinkAnalysis {
  let totalLinks = 0;
  let totalSim = 0;
  const weakLinks: { source: string; target: string; similarity: number }[] = [];
  const strongLinks: { source: string; target: string; similarity: number }[] = [];

  for (const page of pages) {
    const srcVec = embeddings.get(page.url);
    if (!srcVec) continue;

    // Deduplicate targets within a page
    const seenTargets = new Set<string>();
    for (const link of page.internalLinks) {
      if (seenTargets.has(link)) continue;
      seenTargets.add(link);

      const tgtVec = embeddings.get(link);
      if (!tgtVec) continue;

      const sim = cosineSimilarity(srcVec, tgtVec);
      totalLinks++;
      totalSim += sim;

      if (sim < weakThreshold) {
        weakLinks.push({ source: page.url, target: link, similarity: Math.round(sim * 1000) / 1000 });
      } else if (sim > strongThreshold) {
        strongLinks.push({ source: page.url, target: link, similarity: Math.round(sim * 1000) / 1000 });
      }
    }
  }

  // Sort: weakest first, strongest first
  weakLinks.sort((a, b) => a.similarity - b.similarity);
  strongLinks.sort((a, b) => b.similarity - a.similarity);

  return {
    totalLinks,
    avgSemSimilarity: totalLinks > 0 ? Math.round((totalSim / totalLinks) * 1000) / 1000 : 0,
    weakLinks: weakLinks.slice(0, 50),
    weakLinksCount: weakLinks.length,
    strongLinks: strongLinks.slice(0, 50),
    strongLinksCount: strongLinks.length,
  };
}

// ── Aggregate Link Intelligence Data per page ────────────

/**
 * Compute all link intelligence metrics and attach to each page.
 * Returns stats for the report.
 *
 * Step 1: Click depth, near-orphans, link dilution, link position classification
 * Step 2: HITS hub/authority scores, internal linking suggestions
 * Step 3: Betweenness/closeness centrality, semantic link distance
 */
export function computeLinkIntelligence(
  pages: PageData[],
  homepageUrl: string,
  options: {
    embeddings?: Map<string, number[]> | null;
    pageRanks?: Map<string, number>;
    maxSuggestions?: number;
    noCentrality?: boolean;
  } = {},
): CrawlStats['linkIntelligenceStats'] {
  if (pages.length === 0) return null;

  // 0. Build shared adjacency graph (used by click depth, HITS, centrality)
  const graph = buildAdjacencyList(pages);

  // 1. Click depth (reuses shared graph)
  const clickDepths = computeClickDepth(pages, homepageUrl, graph);

  // 2. Build unified inlink structures (deduplicated by source page + target pair)
  // BUG-1 fix: Count unique source pages, not anchor instances
  const deduplicatedInlinks = new Map<string, Set<string>>(); // target -> Set<source>
  const inlinkPositions = new Map<string, Record<LinkPosition, number>>();
  // Build a URL resolution map: raw link URL -> canonical page URL
  // Handles trailing-slash redirects (e.g., /about/ -> /about)
  const urlResolver = new Map<string, string>();
  for (const page of pages) {
    urlResolver.set(page.url, page.url);
    if (page.finalUrl && page.finalUrl !== page.url) {
      urlResolver.set(page.finalUrl, page.url);
    }
    // Also map trailing-slash variants
    const altUrl = page.url.endsWith('/') && page.url.length > 1
      ? page.url.slice(0, -1) : page.url + '/';
    if (!urlResolver.has(altUrl)) urlResolver.set(altUrl, page.url);
    if (page.finalUrl) {
      const altFinal = page.finalUrl.endsWith('/') && page.finalUrl.length > 1
        ? page.finalUrl.slice(0, -1) : page.finalUrl + '/';
      if (!urlResolver.has(altFinal)) urlResolver.set(altFinal, page.url);
    }
  }

  for (const page of pages) {
    // Track which targets we've already counted from this source page
    const seenTargets = new Set<string>();
    for (const anchor of page.anchors) {
      if (!anchor.isInternal) continue;
      // Resolve anchor href to canonical page URL (handles trailing-slash redirects)
      const resolvedTarget = urlResolver.get(anchor.href);
      if (!resolvedTarget) continue;

      // Deduplicated inlink counting (unique source pages)
      if (!deduplicatedInlinks.has(resolvedTarget)) {
        deduplicatedInlinks.set(resolvedTarget, new Set());
      }
      deduplicatedInlinks.get(resolvedTarget)!.add(page.url);

      // Position tracking: count each anchor instance for position distribution
      // but only count unique (source, target) for inDegree
      if (!inlinkPositions.has(resolvedTarget)) {
        inlinkPositions.set(resolvedTarget, { content: 0, navigation: 0, footer: 0, sidebar: 0, header: 0, other: 0 });
      }
      // Only count position once per (source, target) pair
      if (!seenTargets.has(resolvedTarget)) {
        seenTargets.add(resolvedTarget);
        const pos = inlinkPositions.get(resolvedTarget)!;
        pos[anchor.position] = (pos[anchor.position] || 0) + 1;
      }
    }
  }

  // 3. Near-orphans (using unified deduplicated inlinks)
  const nearOrphans = detectNearOrphans(pages, clickDepths, deduplicatedInlinks);
  const nearOrphanUrls = new Set(nearOrphans.map(no => no.url));

  // 4. Link dilution
  const dilutionWarnings = computeLinkDilution(pages);

  // 5. Global link position distribution
  const globalPositions: Record<string, number> = { content: 0, navigation: 0, footer: 0, sidebar: 0, header: 0, other: 0 };
  for (const page of pages) {
    for (const anchor of page.anchors) {
      if (anchor.isInternal) {
        globalPositions[anchor.position] = (globalPositions[anchor.position] || 0) + 1;
      }
    }
  }

  // 6. Pages with no content-area inlinks
  const pagesWithNoContentLinks: string[] = [];
  for (const page of pages) {
    if (page.statusCode !== 200) continue;
    const positions = inlinkPositions.get(page.url);
    if (!positions || positions.content === 0) {
      // Only flag if page has some inlinks (otherwise it's an orphan, handled elsewhere)
      const inDeg = deduplicatedInlinks.get(page.url)?.size || 0;
      if (inDeg > 0) {
        pagesWithNoContentLinks.push(page.url);
      }
    }
  }

  // 7. Step 2: HITS Hub/Authority Scores
  const hitsScores = computeHits(pages, 50, 0.0001, graph);

  // 8. Step 2: Internal Linking Suggestions
  const pageRanks = options.pageRanks || new Map<string, number>();
  const linkSuggestions = generateLinkSuggestions(
    pages,
    options.embeddings || null,
    clickDepths,
    pageRanks,
    deduplicatedInlinks,
    { maxSuggestions: options.maxSuggestions || 50 },
  );

  // 9. Step 3: Betweenness & Closeness Centrality
  const MAX_CENTRALITY_PAGES = 20000;
  const skipCentrality = options.noCentrality || pages.length > MAX_CENTRALITY_PAGES;
  let betweennessScores = new Map<string, number>();
  let closenessScores = new Map<string, number>();
  let centralitySkipReason = '';

  if (skipCentrality) {
    centralitySkipReason = options.noCentrality
      ? 'skipped (--li-no-centrality)'
      : `skipped (${pages.length.toLocaleString()} pages exceeds ${MAX_CENTRALITY_PAGES.toLocaleString()} limit)`;
  } else {
    betweennessScores = computeBetweennessCentrality(pages, 5000, graph);
    closenessScores = computeClosenessCentrality(pages, 5000, graph);
  }

  // 10. Step 3: Semantic Link Distance
  let semanticLinkAnalysis: SemanticLinkAnalysis | null = null;
  if (options.embeddings && options.embeddings.size > 0) {
    semanticLinkAnalysis = analyzeSemanticLinkDistance(pages, options.embeddings);
  }

  // 11. Attach per-page data (including HITS + centrality scores)
  for (const page of pages) {
    const cd = clickDepths.get(page.url) ?? null;
    const inDeg = deduplicatedInlinks.get(page.url)?.size || 0;
    const outDeg = page.internalLinks.length;
    const positions = inlinkPositions.get(page.url) || { content: 0, navigation: 0, footer: 0, sidebar: 0, header: 0, other: 0 };
    const hits = hitsScores.get(page.url);

    page.linkIntelligence = {
      clickDepth: cd,
      inDegree: inDeg,
      outDegree: outDeg,
      isNearOrphan: nearOrphanUrls.has(page.url),
      linkDilutionFactor: outDeg > 0 ? Math.round((1 / outDeg) * 10000) / 10000 : 1,
      hubScore: hits?.hubScore ?? 0,
      authorityScore: hits?.authorityScore ?? 0,
      betweennessCentrality: betweennessScores.get(page.url) ?? 0,
      closenessCentrality: closenessScores.get(page.url) ?? 0,
      contentLinksCount: positions.content,
      navLinksCount: positions.navigation,
      footerLinksCount: positions.footer,
      sidebarLinksCount: positions.sidebar,
      headerLinksCount: positions.header,
      otherLinksCount: positions.other,
    };
  }

  // 12. Click depth stats (PERF-1 fix: loop-based max instead of Math.max spread)
  const clickDepthValues = Array.from(clickDepths.values());
  let sumDepths = 0;
  let maxClickDepth = 0;
  for (const d of clickDepthValues) {
    sumDepths += d;
    if (d > maxClickDepth) maxClickDepth = d;
  }
  const avgClickDepth = clickDepthValues.length > 0
    ? Math.round((sumDepths / clickDepthValues.length) * 10) / 10
    : 0;

  const clickDepthDistribution: Record<number, number> = {};
  for (const d of clickDepthValues) {
    clickDepthDistribution[d] = (clickDepthDistribution[d] || 0) + 1;
  }

  const allUnreachablePages = pages
    .filter(p => p.statusCode === 200 && !clickDepths.has(p.url))
    .map(p => p.url);

  // 13. HITS top lists
  const topAuthorities = [...hitsScores.entries()]
    .sort(([, a], [, b]) => b.authorityScore - a.authorityScore)
    .slice(0, 10)
    .filter(([, s]) => s.authorityScore > 0)
    .map(([url, s]) => ({ url, score: s.authorityScore }));

  const topHubs = [...hitsScores.entries()]
    .sort(([, a], [, b]) => b.hubScore - a.hubScore)
    .slice(0, 10)
    .filter(([, s]) => s.hubScore > 0)
    .map(([url, s]) => ({ url, score: s.hubScore }));

  // 14. Centrality top lists
  const topBridges = [...betweennessScores.entries()]
    .sort(([, a], [, b]) => b - a)
    .slice(0, 10)
    .filter(([, s]) => s > 0)
    .map(([url, score]) => ({ url, score }));

  const singlePointOfFailure = [...betweennessScores.entries()]
    .filter(([, s]) => s > 8.0)
    .map(([url]) => url);

  const mostConnected = [...closenessScores.entries()]
    .sort(([, a], [, b]) => b - a)
    .slice(0, 10)
    .filter(([, s]) => s > 0)
    .map(([url, score]) => ({ url, score }));

  // Most isolated: lowest closeness, excluding unreachable (score 0)
  const mostIsolated = [...closenessScores.entries()]
    .filter(([, s]) => s > 0)
    .sort(([, a], [, b]) => a - b)
    .slice(0, 10)
    .map(([url, score]) => ({ url, score }));

  return {
    avgClickDepth,
    maxClickDepth,
    clickDepthDistribution,
    unreachablePages: allUnreachablePages.slice(0, 50),
    unreachablePagesCount: allUnreachablePages.length,
    nearOrphans: nearOrphans.slice(0, 50),
    nearOrphansCount: nearOrphans.length,
    dilutionWarnings: dilutionWarnings.slice(0, 50),
    dilutionWarningsCount: dilutionWarnings.length,
    linkPositionDistribution: globalPositions,
    pagesWithNoContentLinks: pagesWithNoContentLinks.slice(0, 50),
    pagesWithNoContentLinksCount: pagesWithNoContentLinks.length,
    // Step 2: HITS
    topAuthorities,
    topHubs,
    // Step 2: Link Suggestions
    linkSuggestions,
    linkSuggestionsCount: linkSuggestions.length,
    // Step 3: Centrality
    topBridges,
    singlePointOfFailure,
    mostConnected,
    mostIsolated,
    centralitySkipped: skipCentrality,
    centralitySkipReason,
    // Step 3: Semantic Link Distance
    semanticLinkAnalysis,
  };
}
