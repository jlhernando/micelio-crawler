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
}

const DEFAULT_MODELS: Record<string, string> = {
  openai: 'text-embedding-3-small',
  ollama: 'nomic-embed-text',
};

// Rate limiting via promise chain (like ai-analysis.ts)
const MIN_API_INTERVAL_MS = 200;
let pending: Promise<void> = Promise.resolve();

/**
 * Generate embeddings for a batch of texts using OpenAI API.
 * Max 50 texts per call (API batch limit).
 */
async function embedOpenAi(
  apiKey: string,
  model: string,
  texts: string[],
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
    signal: AbortSignal.timeout(60000),
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
async function embedOllama(model: string, text: string): Promise<number[]> {
  const response = await fetch('http://localhost:11434/api/embeddings', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model,
      prompt: text,
    }),
    signal: AbortSignal.timeout(60000),
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
      const batch = texts.slice(i, i + BATCH_SIZE);
      await new Promise<void>((resolve) => {
        pending = pending.then(async () => {
          await new Promise((r) => setTimeout(r, MIN_API_INTERVAL_MS));
          resolve();
        });
      });
      try {
        const vectors = await embedOpenAi(apiKey, resolvedModel, batch);
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
      await new Promise<void>((resolve) => {
        pending = pending.then(async () => {
          await new Promise((r) => setTimeout(r, MIN_API_INTERVAL_MS));
          resolve();
        });
      });
      try {
        const vector = await embedOllama(resolvedModel, texts[i]);
        embeddings.push(vector);
        if (vector.length > 0) dimensions = vector.length;
      } catch (err) {
        console.error(`  Embedding page ${i + 1} failed: ${formatError(err)}`);
        embeddings.push([]);
      }
      if (onProgress) onProgress(i + 1, texts.length);
    }
  }

  // Find all pairs above similarity threshold
  const pairs: SimilarPair[] = [];
  for (let i = 0; i < embeddings.length; i++) {
    if (embeddings[i].length === 0) continue;
    for (let j = i + 1; j < embeddings.length; j++) {
      if (embeddings[j].length === 0) continue;
      const sim = cosineSimilarity(embeddings[i], embeddings[j]);
      if (sim >= threshold) {
        pairs.push({
          url1: urls[i],
          url2: urls[j],
          similarity: Math.round(sim * 10000) / 10000,
        });
      }
    }
  }

  // Sort by similarity descending
  pairs.sort((a, b) => b.similarity - a.similarity);

  // Group into cannibalization clusters (union-find / transitive closure)
  const groups = buildCannibalizationGroups(pairs);

  // Build url -> vector map for downstream consumers (link suggestions)
  const vectorMap = new Map<string, number[]>();
  for (let i = 0; i < urls.length; i++) {
    if (embeddings[i].length > 0) {
      vectorMap.set(urls[i], embeddings[i]);
    }
  }

  return {
    pagesEmbedded: embeddings.filter((e) => e.length > 0).length,
    similarPairs: pairs.slice(0, 100), // Cap at 100 pairs
    cannibalizationGroups: groups,
    provider,
    model: resolvedModel,
    dimensions,
    vectors: vectorMap,
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

  // Only return groups with 2+ pages, with avg similarity
  const groups: { urls: string[]; similarity: number }[] = [];
  for (const [, members] of groupMap) {
    if (members.size < 2) continue;
    const memberArr = Array.from(members);
    // Compute average pairwise similarity within the group
    let simSum = 0,
      simCount = 0;
    for (const pair of pairs) {
      if (members.has(pair.url1) && members.has(pair.url2)) {
        simSum += pair.similarity;
        simCount++;
      }
    }
    groups.push({
      urls: memberArr,
      similarity: simCount > 0 ? Math.round((simSum / simCount) * 10000) / 10000 : 0,
    });
  }

  // Sort by group size descending
  groups.sort((a, b) => b.urls.length - a.urls.length);
  return groups.slice(0, 50); // Cap at 50 groups
}
