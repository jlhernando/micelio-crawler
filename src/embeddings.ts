/**
 * Semantic similarity engine using text embeddings.
 * Supports OpenAI (text-embedding-3-small) and Ollama (nomic-embed-text) providers.
 */

import { formatError } from './utils.js';

export interface EmbeddingResult {
  url: string;
  vector: number[];
}

export interface SimilarPair {
  url1: string;
  url2: string;
  similarity: number;
}

export interface EmbeddingStats {
  pagesEmbedded: number;
  similarPairs: SimilarPair[];
  cannibalizationGroups: { urls: string[]; similarity: number }[];
  provider: string;
  model: string;
  dimensions: number;
  /** Raw embedding vectors keyed by URL. Used by link suggestions for semantic scoring. */
  vectors: Map<string, number[]>;
  /**
   * Pages compared pairwise: the first similarityPageCap(dimensions) embedded
   * pages (at most maxPages, fewer for very wide vectors). When it is below
   * pagesEmbedded, the rest were embedded but not searched for duplicates.
   */
  pagesCompared?: number;
}

/**
 * Bounds for the pairwise similarity step. Crawled sites can serve thousands
 * of near-identical pages, so every pair could pass the threshold: without
 * these caps the pair list (and CPU time) grows with N squared.
 */
export const SIMILARITY_LIMITS = {
  /** Pages compared pairwise (first N embedded pages); the rest are only embedded. */
  maxPages: 10_000,
  /**
   * Multiply-adds allowed for the whole pairwise step (pairs x dimensions).
   * The loop yields every sliceMs, so this only bounds total time:
   * 10,000 pages x 1,536 dims (76.8e9) took ~50 s on an M-series Mac and
   * is allowed; 3,072 dims (text-embedding-3-large) -> ~7,200 pages.
   */
  maxWork: 80_000_000_000,
  /** Longest stretch of comparisons before yielding to the event loop. */
  sliceMs: 10,
  /** Most similar neighbours kept per page. */
  topKPerPage: 10,
  /** Pairs kept overall (memory bound before sorting and grouping). */
  maxPairs: 100_000,
  /** Pairs returned in similarPairs. */
  returnedPairs: 100,
};

/** Threshold in [0, 1]; NaN or out-of-range values fall back to 0.85 / are clamped. */
export function clampThreshold(threshold: number): number {
  if (!Number.isFinite(threshold)) return 0.85;
  return Math.min(1, Math.max(0, threshold));
}

/**
 * Pages that can be compared pairwise for vectors of this width: the largest
 * n with n*(n-1)/2*dims <= maxWork, and never more than maxPages.
 */
export function similarityPageCap(dims: number): number {
  const L = SIMILARITY_LIMITS;
  if (!(dims > 0)) return L.maxPages;
  const n = Math.floor((1 + Math.sqrt(1 + (8 * L.maxWork) / dims)) / 2);
  return Math.max(2, Math.min(L.maxPages, n));
}

/** Unit-length Float32 copy of a vector (empty when the norm is 0). */
function normalize(v: number[]): Float32Array {
  let norm = 0;
  for (let i = 0; i < v.length; i++) norm += v[i] * v[i];
  norm = Math.sqrt(norm);
  if (norm === 0) return new Float32Array(0);
  const out = new Float32Array(v.length);
  for (let i = 0; i < v.length; i++) out[i] = v[i] / norm;
  return out;
}

/**
 * Pairs of pages whose cosine similarity is >= threshold, bounded by
 * SIMILARITY_LIMITS: each page keeps only its topKPerPage best neighbours and
 * the total list never exceeds maxPairs. Sorted by similarity, descending.
 * The number of pages compared also shrinks with the vector width (maxWork),
 * and the loop yields to the event loop every sliceMs so the dashboard keeps
 * answering; it stops when signal aborts.
 */
export async function findSimilarPairs(
  urls: string[],
  vectors: number[][],
  threshold: number,
  signal?: AbortSignal,
): Promise<{ pairs: SimilarPair[]; compared: number; total: number }> {
  const t = clampThreshold(threshold);
  const L = SIMILARITY_LIMITS;
  const idx: number[] = [];
  const unit: Float32Array[] = [];
  const firstDims = vectors.find((v) => v.length > 0)?.length ?? 0;
  const cap = similarityPageCap(firstDims);
  let total = 0; // embedded pages that could be compared
  for (let i = 0; i < vectors.length; i++) {
    if (vectors[i].length === 0) continue;
    if (idx.length >= cap) {
      total++;
      continue;
    }
    const u = normalize(vectors[i]);
    if (u.length === 0) continue;
    idx.push(i);
    unit.push(u);
    total++;
  }
  const n = unit.length;
  // Per-page best neighbours (both directions), as small sorted arrays.
  const best: { j: number; sim: number }[][] = Array.from({ length: n }, () => []);
  const offer = (a: number, b: number, sim: number) => {
    const list = best[a];
    if (list.length === L.topKPerPage && list[list.length - 1].sim >= sim) return;
    let k = list.length;
    while (k > 0 && list[k - 1].sim < sim) k--;
    list.splice(k, 0, { j: b, sim });
    if (list.length > L.topKPerPage) list.pop();
  };
  let sliceStart = performance.now();
  let aborted = false;
  for (let a = 0; a < n && !aborted; a++) {
    if (signal?.aborted) break;
    const ua = unit[a];
    for (let b = a + 1; b < n; b++) {
      if ((b & 255) === 0 && performance.now() - sliceStart >= L.sliceMs) {
        await new Promise((r) => setImmediate(r));
        sliceStart = performance.now();
        if (signal?.aborted) { aborted = true; break; }
      }
      const ub = unit[b];
      if (ub.length !== ua.length) continue;
      // Four running sums: about twice as fast as one in V8
      const len = ua.length;
      const len4 = len - (len % 4);
      let s0 = 0, s1 = 0, s2 = 0, s3 = 0;
      for (let d = 0; d < len4; d += 4) {
        s0 += ua[d] * ub[d];
        s1 += ua[d + 1] * ub[d + 1];
        s2 += ua[d + 2] * ub[d + 2];
        s3 += ua[d + 3] * ub[d + 3];
      }
      for (let d = len4; d < len; d++) s0 += ua[d] * ub[d];
      const dot = s0 + s1 + s2 + s3;
      if (dot >= t) {
        offer(a, b, dot);
        offer(b, a, dot);
      }
    }
    if (performance.now() - sliceStart >= L.sliceMs) {
      await new Promise((r) => setImmediate(r));
      sliceStart = performance.now();
    }
  }
  const seen = new Set<string>();
  const pairs: SimilarPair[] = [];
  for (let a = 0; a < n && pairs.length < L.maxPairs; a++) {
    for (const { j: b, sim } of best[a]) {
      const lo = Math.min(a, b), hi = Math.max(a, b);
      const key = `${lo}:${hi}`;
      if (seen.has(key)) continue;
      seen.add(key);
      pairs.push({
        url1: urls[idx[lo]],
        url2: urls[idx[hi]],
        similarity: Math.round(Math.min(1, sim) * 10000) / 10000,
      });
      if (pairs.length >= L.maxPairs) break;
    }
  }
  pairs.sort((x, y) => y.similarity - x.similarity);
  return { pairs, compared: n, total };
}

const DEFAULT_MODELS: Record<string, string> = {
  openai: 'text-embedding-3-small',
  ollama: 'nomic-embed-text',
};

// Rate limiting via promise chain (like ai-analysis.ts)
const MIN_API_INTERVAL_MS = 200;
let pending: Promise<void> = Promise.resolve();

/** Timeout signal that also aborts with the caller's signal. */
function withTimeout(ms: number, signal?: AbortSignal): AbortSignal {
  const timeout = AbortSignal.timeout(ms);
  return signal ? AbortSignal.any([timeout, signal]) : timeout;
}

/**
 * Generate embeddings for a batch of texts using OpenAI API.
 * Max 50 texts per call (API batch limit).
 */
async function embedOpenAi(
  apiKey: string,
  model: string,
  texts: string[],
  signal?: AbortSignal,
): Promise<number[][]> {
  const response = await fetch('https://api.openai.com/v1/embeddings', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify({
      model,
      input: texts,
    }),
    signal: withTimeout(60000, signal),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`OpenAI Embeddings API error: ${response.status} ${text.substring(0, 200)}`);
  }

  const data = await response.json();
  // Sort by index to maintain order
  const sorted = data.data.sort((a: { index: number }, b: { index: number }) => a.index - b.index);
  return sorted.map((item: { embedding: number[] }) => item.embedding);
}

/**
 * Generate embeddings for a single text using Ollama API.
 * Ollama does not support batch embedding, so we call one at a time.
 */
async function embedOllama(model: string, text: string, signal?: AbortSignal): Promise<number[]> {
  const response = await fetch('http://localhost:11434/api/embeddings', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model,
      prompt: text,
    }),
    signal: withTimeout(60000, signal),
  });

  if (!response.ok) {
    const responseBody = await response.text();
    throw new Error(`Ollama Embeddings API error: ${response.status} ${responseBody.substring(0, 200)}`);
  }

  const data = await response.json();
  return data.embedding;
}

/**
 * Compute cosine similarity between two vectors. Returns value in [-1, 1].
 * For text embeddings, values are typically in [0, 1].
 */
export function cosineSimilarity(a: number[], b: number[]): number {
  if (a.length !== b.length) return 0;
  let dot = 0,
    normA = 0,
    normB = 0;
  for (let i = 0; i < a.length; i++) {
    dot += a[i] * b[i];
    normA += a[i] * a[i];
    normB += b[i] * b[i];
  }
  const denom = Math.sqrt(normA) * Math.sqrt(normB);
  return denom === 0 ? 0 : dot / denom;
}

/**
 * Generate embeddings for all pages and compute similarity pairs.
 */
export async function computeEmbeddings(
  pages: { url: string; bodyText: string }[],
  provider: 'openai' | 'ollama',
  apiKey: string,
  model: string,
  threshold: number,
  onProgress?: (done: number, total: number) => void,
  signal?: AbortSignal,
): Promise<EmbeddingStats> {
  const resolvedModel = model || DEFAULT_MODELS[provider] || DEFAULT_MODELS.openai;

  // Truncate body text for embedding (8000 chars ~2000 tokens)
  const MAX_TEXT_LEN = 8000;
  const texts = pages.map((p) => p.bodyText.substring(0, MAX_TEXT_LEN));
  const urls = pages.map((p) => p.url);

  // Generate embeddings
  const embeddings: number[][] = [];
  let dimensions = 0;

  if (provider === 'openai') {
    // Batch up to 25 texts per API call (conservative to stay within token limits)
    const BATCH_SIZE = 25;
    for (let i = 0; i < texts.length; i += BATCH_SIZE) {
      if (signal?.aborted) break; // no more paid calls once cancelled
      const batch = texts.slice(i, i + BATCH_SIZE);
      await new Promise<void>((resolve) => {
        pending = pending.then(async () => {
          await new Promise((r) => setTimeout(r, MIN_API_INTERVAL_MS));
          resolve();
        });
      });
      try {
        const vectors = await embedOpenAi(apiKey, resolvedModel, batch, signal);
        embeddings.push(...vectors);
        if (vectors.length > 0) dimensions = vectors[0].length;
      } catch (err) {
        console.error(`  Embedding batch ${Math.floor(i / BATCH_SIZE) + 1} failed: ${formatError(err)}`);
        // Fill with empty vectors for failed batch
        for (let j = 0; j < batch.length; j++) embeddings.push([]);
      }
      if (onProgress) onProgress(Math.min(i + BATCH_SIZE, texts.length), texts.length);
    }
  } else {
    // Ollama: one at a time
    for (let i = 0; i < texts.length; i++) {
      if (signal?.aborted) break;
      await new Promise<void>((resolve) => {
        pending = pending.then(async () => {
          await new Promise((r) => setTimeout(r, MIN_API_INTERVAL_MS));
          resolve();
        });
      });
      try {
        const vector = await embedOllama(resolvedModel, texts[i], signal);
        embeddings.push(vector);
        if (vector.length > 0) dimensions = vector.length;
      } catch (err) {
        console.error(`  Embedding page ${i + 1} failed: ${formatError(err)}`);
        embeddings.push([]);
      }
      if (onProgress) onProgress(i + 1, texts.length);
    }
  }

  // Similar pairs, bounded (top-K neighbours per page, page and pair caps)
  const { pairs, compared, total } = await findSimilarPairs(urls, embeddings, threshold, signal);
  if (compared < total) {
    console.warn(
      `  Similarity: compared ${compared} of ${total} embedded pages ` +
      `(limit for ${dimensions}-dimension vectors); the rest are not checked for duplicates`,
    );
  }

  // Group into cannibalization clusters (union-find / transitive closure)
  const groups = buildCannibalizationGroups(pairs);

  // Build url -> vector map for downstream consumers (link suggestions)
  const vectorMap = new Map<string, number[]>();
  for (let i = 0; i < embeddings.length; i++) {
    if (embeddings[i].length > 0) {
      vectorMap.set(urls[i], embeddings[i]);
    }
  }

  return {
    pagesEmbedded: embeddings.filter((e) => e.length > 0).length,
    similarPairs: pairs.slice(0, SIMILARITY_LIMITS.returnedPairs),
    cannibalizationGroups: groups,
    provider,
    model: resolvedModel,
    dimensions,
    vectors: vectorMap,
    pagesCompared: compared,
  };
}

/**
 * Build cannibalization groups using union-find for transitive closure.
 * If A~B and B~C, then {A, B, C} form a group.
 */
function buildCannibalizationGroups(
  pairs: SimilarPair[],
): { urls: string[]; similarity: number }[] {
  if (pairs.length === 0) return [];

  // Union-Find
  const parent = new Map<string, string>();

  function find(x: string): string {
    if (!parent.has(x)) parent.set(x, x);
    let root = x;
    while (parent.get(root) !== root) root = parent.get(root)!;
    // Path compression
    let cur = x;
    while (cur !== root) {
      const next = parent.get(cur)!;
      parent.set(cur, root);
      cur = next;
    }
    return root;
  }

  function union(a: string, b: string): void {
    const ra = find(a);
    const rb = find(b);
    if (ra !== rb) parent.set(ra, rb);
  }

  for (const pair of pairs) {
    union(pair.url1, pair.url2);
  }

  // Collect groups
  const groupMap = new Map<string, Set<string>>();
  const allUrls = new Set<string>();
  for (const pair of pairs) {
    allUrls.add(pair.url1);
    allUrls.add(pair.url2);
  }
  for (const url of allUrls) {
    const root = find(url);
    if (!groupMap.has(root)) groupMap.set(root, new Set());
    groupMap.get(root)!.add(url);
  }

  // Average similarity per group in one pass over the pairs
  const simSum = new Map<string, number>();
  const simCount = new Map<string, number>();
  for (const pair of pairs) {
    const root = find(pair.url1);
    simSum.set(root, (simSum.get(root) ?? 0) + pair.similarity);
    simCount.set(root, (simCount.get(root) ?? 0) + 1);
  }

  // Only return groups with 2+ pages, with avg similarity
  const groups: { urls: string[]; similarity: number }[] = [];
  for (const [root, members] of groupMap) {
    if (members.size < 2) continue;
    const count = simCount.get(root) ?? 0;
    groups.push({
      urls: Array.from(members),
      similarity: count > 0 ? Math.round(((simSum.get(root) ?? 0) / count) * 10000) / 10000 : 0,
    });
  }

  // Sort by group size descending
  groups.sort((a, b) => b.urls.length - a.urls.length);
  return groups.slice(0, 50); // Cap at 50 groups
}
